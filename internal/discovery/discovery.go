// Package discovery orchestrates authenticated upstream model refreshes.
package discovery

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lan/meta-gateway/internal/adapters"
	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
	"github.com/lan/meta-gateway/internal/webhook"
)

type ErrorKind string

const (
	ErrorNotFound    ErrorKind = "not_found"
	ErrorUnavailable ErrorKind = "unavailable"
	ErrorUpstream    ErrorKind = "upstream"
	ErrorInternal    ErrorKind = "internal"
)

type Error struct {
	Kind     ErrorKind
	Category string
}

func (e *Error) Error() string { return fmt.Sprintf("discovery failed: %s", e.Category) }

type RefreshResult struct {
	ChannelID      int64     `json:"channel_id"`
	Adapter        string    `json:"adapter"`
	Models         []string  `json:"models"`
	LatencyMs      int       `json:"latency_ms"`
	CheckedAt      time.Time `json:"checked_at"`
	CreatedRoutes  int       `json:"created_routes"`
	CreatedMembers int       `json:"created_members"`
	EnabledMembers int       `json:"enabled_members"`
	DeletedMembers int       `json:"deleted_members"`
	DeletedRoutes  int       `json:"deleted_routes"`
}

type ProbeResult struct {
	ChannelID int64     `json:"channel_id"`
	Adapter   string    `json:"adapter"`
	Models    []string  `json:"models"`
	LatencyMs int       `json:"latency_ms"`
	CheckedAt time.Time `json:"checked_at"`
}

type RefreshItem struct {
	ChannelID int64          `json:"channel_id"`
	Result    *RefreshResult `json:"result,omitempty"`
	Error     string         `json:"error,omitempty"`
}

type RefreshSummary struct {
	Items        []RefreshItem `json:"items"`
	SuccessCount int           `json:"success_count"`
	FailureCount int           `json:"failure_count"`
}

type Service struct {
	db       *store.DB
	enc      *crypto.Encrypter
	registry *adapters.Registry
	now      func() time.Time

	// recoveryMu guards the passive-recovery probe configuration.
	recoveryMu       sync.RWMutex
	recoveryEnabled  bool
	recoveryInterval time.Duration
	// notifier delivers operational webhooks (auto-disable/recovery).
	notifier *webhook.Notifier
}

// SetRecoveryConfig hot-applies the passive-recovery probe configuration.
func (s *Service) SetRecoveryConfig(enabled bool, interval time.Duration) {
	s.recoveryMu.Lock()
	defer s.recoveryMu.Unlock()
	s.recoveryEnabled = enabled
	s.recoveryInterval = interval
}

// recoveryConfig returns the current passive-recovery probe configuration.
func (s *Service) recoveryConfig() (bool, time.Duration) {
	s.recoveryMu.RLock()
	defer s.recoveryMu.RUnlock()
	return s.recoveryEnabled, s.recoveryInterval
}

func New(db *store.DB, enc *crypto.Encrypter, registry *adapters.Registry) *Service {
	return &Service{db: db, enc: enc, registry: registry, now: time.Now}
}

// SetWebhookNotifier installs the operational webhook notifier (nil disables).
func (s *Service) SetWebhookNotifier(notifier *webhook.Notifier) {
	s.notifier = notifier
}

func (s *Service) Probe(ctx context.Context, channelID int64) (*ProbeResult, error) {
	channel, err := s.db.Channel.GetByID(channelID)
	if err != nil {
		return nil, internalError("channel_lookup")
	}
	if channel == nil {
		return nil, &Error{Kind: ErrorNotFound, Category: "channel_not_found"}
	}
	if channel.Status != domain.StatusEnabled && channel.Status != domain.StatusAutoDisabled {
		return nil, unavailableError("channel_disabled")
	}
	if channel.SiteID == nil {
		return nil, unavailableError("site_unavailable")
	}
	site, err := s.db.Site.GetByID(*channel.SiteID)
	if err != nil {
		return nil, internalError("site_lookup")
	}
	if site == nil || site.Status != domain.StatusEnabled {
		return nil, unavailableError("site_unavailable")
	}
	adapter, ok := s.registry.Resolve(channel.TypeHint, site.Platform)
	if !ok {
		return nil, unavailableError("unsupported_adapter")
	}
	credentials, err := s.resolveAPIKeyPool(channel, site.ID)
	if err != nil {
		return nil, err
	}
	if len(credentials) == 0 {
		checkedAt := s.now()
		_ = s.db.Channel.RecordProbeFailure(channel.ID, checkedAt, domain.CategoryCredentialUnavailable)
		_ = s.db.HealthHistory.Append(channel.ID, domain.ProbeKindProbe, false, 0, domain.CategoryCredentialUnavailable, checkedAt)
		return nil, unavailableError(domain.CategoryCredentialUnavailable)
	}
	baseURL := channel.BaseURL
	if baseURL == "" {
		baseURL = site.BaseURL
	}
	started := s.now()
	models, lastErr, lastCredential, fatal := s.probeModels(ctx, adapter, baseURL, credentials)
	if fatal != nil {
		if errors.Is(fatal, context.Canceled) || errors.Is(fatal, context.DeadlineExceeded) {
			return nil, fatal
		}
		checkedAt := s.now()
		_ = s.db.Channel.RecordProbeFailure(channel.ID, checkedAt, domain.CategoryInvalidBaseURL)
		_ = s.db.HealthHistory.Append(channel.ID, domain.ProbeKindProbe, false, 0, domain.CategoryInvalidBaseURL, checkedAt)
		return nil, unavailableError(domain.CategoryInvalidBaseURL)
	}
	if lastErr != nil && isTransientListError(lastErr) {
		// Cloudflare-protected public sites often drop a single request
		// (challenge, TLS, timeout). Retry once before recording a failure so
		// a flaky upstream does not flip the badge to "unreachable" for one
		// bad sample. Auth rejections are not retried.
		select {
		case <-ctx.Done():
			return nil, lastErr
		case <-time.After(1200 * time.Millisecond):
		}
		models, lastErr, lastCredential, fatal = s.probeModels(ctx, adapter, baseURL, credentials)
		if fatal != nil {
			if errors.Is(fatal, context.Canceled) || errors.Is(fatal, context.DeadlineExceeded) {
				return nil, fatal
			}
			checkedAt := s.now()
			_ = s.db.Channel.RecordProbeFailure(channel.ID, checkedAt, domain.CategoryInvalidBaseURL)
			_ = s.db.HealthHistory.Append(channel.ID, domain.ProbeKindProbe, false, 0, domain.CategoryInvalidBaseURL, checkedAt)
			return nil, unavailableError(domain.CategoryInvalidBaseURL)
		}
	}
	if lastErr != nil {
		if errors.Is(lastErr, context.Canceled) || errors.Is(lastErr, context.DeadlineExceeded) {
			return nil, lastErr
		}
		category := domain.CategoryUpstreamFailure
		var adapterErr *adapters.Error
		if errors.As(lastErr, &adapterErr) {
			category = string(adapterErr.Kind)
			// 401/403 on /v1/models with a user access_token is expected on many New API hosts.
			if adapterErr.Kind == adapters.ErrorStatus &&
				(adapterErr.Status == 401 || adapterErr.Status == 403) {
				kind := ""
				if lastCredential != nil {
					kind = strings.ToLower(strings.TrimSpace(lastCredential.Kind))
				}
				if kind == "access_token" || kind == "session" {
					category = domain.CategoryUserTokenNotForModels
				} else {
					category = domain.CategoryUpstreamUnauthorized
				}
			}
		}
		checkedAt := s.now()
		_ = s.db.Channel.RecordProbeFailure(channel.ID, checkedAt, category)
		_ = s.db.HealthHistory.Append(channel.ID, domain.ProbeKindProbe, false, 0, category, checkedAt)
		return nil, &Error{Kind: ErrorUpstream, Category: category}
	}
	checkedAt := s.now()
	latency := int(checkedAt.Sub(started).Milliseconds())
	// A healthy probe restores an auto-disabled channel and reports recovery.
	recovered, _ := s.db.Channel.RecoverAutoDisabled(channel.ID)
	if recovered && s.notifier != nil {
		s.notifier.Notify(context.Background(), webhook.ChannelRecovered, channel.ID, channel.Name, "probe ok")
	}
	_ = s.db.Channel.RecordProbeSuccess(channel.ID, checkedAt)
	_ = s.db.HealthHistory.Append(channel.ID, domain.ProbeKindProbe, true, latency, "", checkedAt)
	return &ProbeResult{
		ChannelID: channel.ID,
		Adapter:   adapter.Name(),
		Models:    models,
		LatencyMs: latency,
		CheckedAt: checkedAt,
	}, nil
}

// probeModels tries every credential in the pool against /v1/models. Returns
// the first successful model list, or the last error. fatal carries errors that
// must abort immediately (invalid base URL, context cancellation) — these are
// never retried.
func (s *Service) probeModels(ctx context.Context, adapter adapters.ModelAdapter, baseURL string, credentials []domain.Credential) (models []string, lastErr error, lastCredential *domain.Credential, fatal error) {
	for index := range credentials {
		credential := credentials[index]
		lastCredential = &credential
		plaintext, decryptErr := s.enc.Decrypt(string(credential.SecretEnc))
		if decryptErr != nil || len(plaintext) == 0 {
			lastErr = unavailableError(domain.CategoryCredentialUnavailable)
			continue
		}
		listed, listErr := adapter.ListModels(ctx, baseURL, string(plaintext))
		for i := range plaintext {
			plaintext[i] = 0
		}
		if listErr == nil {
			return listed, nil, lastCredential, nil
		}
		lastErr = listErr
		if errors.Is(listErr, context.Canceled) || errors.Is(listErr, context.DeadlineExceeded) {
			return nil, nil, nil, listErr
		}
		var adapterErr *adapters.Error
		if errors.As(listErr, &adapterErr) && adapterErr.Kind == adapters.ErrorInvalidURL {
			return nil, nil, nil, listErr
		}
	}
	return nil, lastErr, lastCredential, nil
}

// isTransientListError reports whether a model-list failure is worth one
// retry: transport-level failures only (timeout, TLS, connection reset, CF
// challenge). Auth and payload rejections are excluded.
func isTransientListError(err error) bool {
	var adapterErr *adapters.Error
	if errors.As(err, &adapterErr) {
		return adapterErr.Kind == adapters.ErrorTransport
	}
	return false
}

// resolveAPIKeyPool returns enabled api_key credentials for discovery, bound key first.
func (s *Service) resolveAPIKeyPool(channel *domain.Channel, siteID int64) ([]domain.Credential, error) {
	seen := make(map[int64]struct{})
	var pool []domain.Credential
	appendIfUsable := func(credential *domain.Credential) {
		if credential == nil {
			return
		}
		if _, exists := seen[credential.ID]; exists {
			return
		}
		if credential.SiteID != siteID || credential.Status != domain.StatusEnabled || len(credential.SecretEnc) == 0 {
			return
		}
		if !strings.EqualFold(credential.Kind, "api_key") {
			return
		}
		seen[credential.ID] = struct{}{}
		pool = append(pool, *credential)
	}
	if channel.CredentialID != nil {
		bound, err := s.db.Credential.GetByID(*channel.CredentialID)
		if err != nil {
			return nil, internalError("credential_lookup")
		}
		appendIfUsable(bound)
	}
	siteKeys, err := s.db.Credential.ListEnabledAPIKeysBySite(siteID)
	if err != nil {
		return nil, internalError("credential_lookup")
	}
	for index := range siteKeys {
		appendIfUsable(&siteKeys[index])
	}
	return pool, nil
}

func (s *Service) Refresh(ctx context.Context, channelID int64) (*RefreshResult, error) {
	probe, err := s.Probe(ctx, channelID)
	if err != nil {
		return nil, err
	}
	reconciled, err := s.db.DiscoveredModel.Reconcile(ctx, store.ReconcileInput{
		ChannelID: probe.ChannelID,
		Models:    probe.Models,
		Source:    probe.Adapter,
		LatencyMs: probe.LatencyMs,
		CheckedAt: probe.CheckedAt,
	})
	if err != nil {
		return nil, internalError("persistence_failure")
	}
	return &RefreshResult{
		ChannelID: probe.ChannelID, Adapter: probe.Adapter, Models: probe.Models,
		LatencyMs: probe.LatencyMs, CheckedAt: probe.CheckedAt,
		CreatedRoutes: reconciled.CreatedRoutes, CreatedMembers: reconciled.CreatedMembers,
		EnabledMembers: reconciled.EnabledMembers, DeletedMembers: reconciled.DeletedMembers,
	}, nil
}

func (s *Service) RefreshAll(ctx context.Context) (*RefreshSummary, error) {
	channels, err := s.db.Channel.ListProbeable()
	if err != nil {
		return nil, internalError("channel_list")
	}
	sort.Slice(channels, func(i, j int) bool { return channels[i].ID < channels[j].ID })
	summary := &RefreshSummary{Items: make([]RefreshItem, 0, len(channels))}
	for _, channel := range channels {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// Auto-disabled channels are probed for passive recovery without
		// touching their model reconciliation state.
		if channel.Status == domain.StatusAutoDisabled {
			item := RefreshItem{ChannelID: channel.ID}
			if _, probeErr := s.Probe(ctx, channel.ID); probeErr != nil {
				item.Error = "recovery_probe_failed"
				summary.FailureCount++
			} else {
				// Probe restores the channel via RecoverAutoDisabled on success.
				summary.SuccessCount++
			}
			summary.Items = append(summary.Items, item)
			continue
		}
		result, refreshErr := s.Refresh(ctx, channel.ID)
		item := RefreshItem{ChannelID: channel.ID, Result: result}
		if refreshErr != nil {
			var discoveryErr *Error
			if errors.As(refreshErr, &discoveryErr) {
				item.Error = discoveryErr.Category
			} else {
				item.Error = "refresh_failed"
			}
			summary.FailureCount++
		} else {
			summary.SuccessCount++
		}
		summary.Items = append(summary.Items, item)
	}
	return summary, nil
}

// RunRecoveryLoop periodically probes auto-disabled channels and restores them
// when the upstream answers again. The enabled flag and interval come from the
// recovery configuration (hot-reloadable via SetRecoveryConfig).
func (s *Service) RunRecoveryLoop(ctx context.Context) {
	lastRun := time.Time{}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			enabled, interval := s.recoveryConfig()
			if !enabled || interval <= 0 {
				lastRun = time.Time{} // re-arm when re-enabled
				continue
			}
			if time.Since(lastRun) < interval {
				continue
			}
			lastRun = time.Now()
			probeCtx, cancel := context.WithTimeout(ctx, time.Minute)
			recovered := s.probeAutoDisabledOnce(probeCtx)
			cancel()
			if recovered > 0 {
				log.Printf("discovery: recovery probe restored %d auto-disabled channel(s)", recovered)
			}
		}
	}
}

// probeAutoDisabledOnce probes every auto-disabled channel once and returns how
// many recovered (Probe restores them internally on success).
func (s *Service) probeAutoDisabledOnce(ctx context.Context) int {
	channels, err := s.db.Channel.ListAutoDisabled()
	if err != nil {
		log.Printf("discovery: recovery list: %v", err)
		return 0
	}
	recovered := 0
	for _, channel := range channels {
		if ctx.Err() != nil {
			break
		}
		if _, probeErr := s.Probe(ctx, channel.ID); probeErr == nil {
			recovered++
		}
	}
	return recovered
}

func unavailableError(category string) error {
	return &Error{Kind: ErrorUnavailable, Category: category}
}

func internalError(category string) error {
	return &Error{Kind: ErrorInternal, Category: category}
}

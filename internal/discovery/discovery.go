// Package discovery orchestrates authenticated upstream model refreshes.
package discovery

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/lan/meta-gateway/internal/adapters"
	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
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
}

func New(db *store.DB, enc *crypto.Encrypter, registry *adapters.Registry) *Service {
	return &Service{db: db, enc: enc, registry: registry, now: time.Now}
}

func (s *Service) Probe(ctx context.Context, channelID int64) (*ProbeResult, error) {
	channel, err := s.db.Channel.GetByID(channelID)
	if err != nil {
		return nil, internalError("channel_lookup")
	}
	if channel == nil {
		return nil, &Error{Kind: ErrorNotFound, Category: "channel_not_found"}
	}
	if channel.Status != domain.StatusEnabled {
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
		_ = s.db.Channel.RecordProbeFailure(channel.ID, s.now(), "credential_unavailable")
		return nil, unavailableError("credential_unavailable")
	}
	baseURL := channel.BaseURL
	if baseURL == "" {
		baseURL = site.BaseURL
	}
	started := s.now()
	var models []string
	var lastErr error
	var lastCredential *domain.Credential
	for index := range credentials {
		credential := credentials[index]
		lastCredential = &credential
		plaintext, decryptErr := s.enc.Decrypt(string(credential.SecretEnc))
		if decryptErr != nil || len(plaintext) == 0 {
			lastErr = unavailableError("credential_unavailable")
			continue
		}
		listed, listErr := adapter.ListModels(ctx, baseURL, string(plaintext))
		for i := range plaintext {
			plaintext[i] = 0
		}
		if listErr == nil {
			models = listed
			lastErr = nil
			break
		}
		lastErr = listErr
		if errors.Is(listErr, context.Canceled) || errors.Is(listErr, context.DeadlineExceeded) {
			return nil, listErr
		}
		// Non-retryable adapter failures stop the pool early.
		var adapterErr *adapters.Error
		if errors.As(listErr, &adapterErr) && adapterErr.Kind == adapters.ErrorInvalidURL {
			_ = s.db.Channel.RecordProbeFailure(channel.ID, s.now(), "invalid_base_url")
			return nil, unavailableError("invalid_base_url")
		}
	}
	if lastErr != nil {
		if errors.Is(lastErr, context.Canceled) || errors.Is(lastErr, context.DeadlineExceeded) {
			return nil, lastErr
		}
		category := "upstream_failure"
		var adapterErr *adapters.Error
		if errors.As(lastErr, &adapterErr) {
			if adapterErr.Kind == adapters.ErrorInvalidURL {
				_ = s.db.Channel.RecordProbeFailure(channel.ID, s.now(), "invalid_base_url")
				return nil, unavailableError("invalid_base_url")
			}
			category = string(adapterErr.Kind)
			// 401/403 on /v1/models with a user access_token is expected on many New API hosts.
			if adapterErr.Kind == adapters.ErrorStatus &&
				(adapterErr.Status == 401 || adapterErr.Status == 403) {
				kind := ""
				if lastCredential != nil {
					kind = strings.ToLower(strings.TrimSpace(lastCredential.Kind))
				}
				if kind == "access_token" || kind == "session" {
					category = "user_token_not_for_models"
				} else {
					category = "upstream_unauthorized"
				}
			}
		}
		_ = s.db.Channel.RecordProbeFailure(channel.ID, s.now(), category)
		return nil, &Error{Kind: ErrorUpstream, Category: category}
	}
	checkedAt := s.now()
	latency := int(checkedAt.Sub(started).Milliseconds())
	_ = s.db.Channel.RecordProbeSuccess(channel.ID, checkedAt)
	// A healthy probe restores an auto-disabled channel.
	_ = s.db.Channel.RecoverAutoDisabled(channel.ID)
	return &ProbeResult{
		ChannelID: channel.ID,
		Adapter:   adapter.Name(),
		Models:    models,
		LatencyMs: latency,
		CheckedAt: checkedAt,
	}, nil
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
	channels, err := s.db.Channel.ListEnabled()
	if err != nil {
		return nil, internalError("channel_list")
	}
	sort.Slice(channels, func(i, j int) bool { return channels[i].ID < channels[j].ID })
	summary := &RefreshSummary{Items: make([]RefreshItem, 0, len(channels))}
	for _, channel := range channels {
		if err := ctx.Err(); err != nil {
			return nil, err
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

func unavailableError(category string) error {
	return &Error{Kind: ErrorUnavailable, Category: category}
}

func internalError(category string) error {
	return &Error{Kind: ErrorInternal, Category: category}
}

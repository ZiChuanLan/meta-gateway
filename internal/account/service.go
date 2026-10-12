// Package account probes upstream user identity and syncs site API keys for relay.
package account

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
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

func (e *Error) Error() string { return fmt.Sprintf("account failed: %s", e.Category) }

type ProbeResult struct {
	ChannelID      int64     `json:"channel_id"`
	CredentialID   int64     `json:"credential_id"`
	Username       string    `json:"username"`
	DisplayName    string    `json:"display_name,omitempty"`
	PlatformUserID int64     `json:"platform_user_id,omitempty"`
	Quota          *int64    `json:"quota,omitempty"`
	UsedQuota      *int64    `json:"used_quota,omitempty"`
	LatencyMs      int       `json:"latency_ms"`
	CheckedAt      time.Time `json:"checked_at"`
}

type SyncKeysRequest struct {
	// AttachToChannel defaults to true when omitted (nil). Set false to import keys without rebinding the channel.
	AttachToChannel *bool `json:"attach_to_channel"`
	// SplitByGroup defaults to false when omitted (nil). When true, creates one connection per token group.
	// Default is false so multiple keys stay aggregated as a site-level key pool on one connection.
	SplitByGroup *bool `json:"split_by_group"`
	MaxKeys      int   `json:"max_keys"`
}

type SyncKeyItem struct {
	Name         string `json:"name,omitempty"`
	Group        string `json:"group,omitempty"`
	CredentialID int64  `json:"credential_id,omitempty"`
	// UpstreamTokenID is the token id on the upstream site (when known), so
	// callers can locate the item created by a specific create-key call.
	UpstreamTokenID int64  `json:"upstream_token_id,omitempty"`
	Enabled         bool   `json:"enabled,omitempty"`
	Status          string `json:"status"`
	Category        string `json:"category,omitempty"`
}

type SyncKeysResult struct {
	ChannelID          int64 `json:"channel_id"`
	Listed             int   `json:"listed"`
	CreatedCredentials int   `json:"created_credentials"`
	ReusedCredentials  int   `json:"reused_credentials"`
	SkippedMasked      int   `json:"skipped_masked"`
	// DeletedCredentials counts local api_key credentials whose upstream token
	// no longer exists (pruned during sync).
	DeletedCredentials int `json:"deleted_credentials"`
	// EmptyList means upstream returned zero tokens (not necessarily expired).
	EmptyList bool `json:"empty_list,omitempty"`
	// Category explains overall outcome for UI (optional).
	Category             string `json:"category,omitempty"`
	Message              string `json:"message,omitempty"`
	AttachedCredentialID int64  `json:"attached_credential_id,omitempty"`
	// Group-split connection outcomes (one channel per New API group).
	CreatedChannels int                `json:"created_channels,omitempty"`
	UpdatedChannels int                `json:"updated_channels,omitempty"`
	GroupChannels   []GroupChannelItem `json:"group_channels,omitempty"`
	Items           []SyncKeyItem      `json:"items"`
}

// CreateKeyRequest describes a token to create on the upstream via the account's
// access_token / session credential.
type CreateKeyRequest struct {
	Name           string `json:"name"`
	Group          string `json:"group"`
	UnlimitedQuota bool   `json:"unlimited_quota"`
}

// CreateKeyResult is the outcome of creating and attaching a new API key.
type CreateKeyResult struct {
	CredentialID int64  `json:"credential_id"`
	Name         string `json:"name"`
	Group        string `json:"group"`
	Category     string `json:"category"`
	Message      string `json:"message"`
}

// GroupChannelItem is one connection ensured for a token group.
type GroupChannelItem struct {
	Group        string `json:"group"`
	ChannelID    int64  `json:"channel_id"`
	CredentialID int64  `json:"credential_id"`
	Name         string `json:"name"`
	Status       string `json:"status"` // created | reused | updated
}

type Service struct {
	db       *store.DB
	enc      *crypto.Encrypter
	registry *adapters.Registry
	now      func() time.Time
	// notifier delivers operational alerts (token expired / balance low).
	notifier atomic.Pointer[webhook.Notifier]

	// lifeCtx bounds every refresh this service starts. Stop cancels it and
	// waits, so a shutdown never leaves an upstream probe running (the old
	// detached context.Background() had no owner).
	lifeCtx    context.Context
	lifeCancel context.CancelFunc

	// refreshWorkMu serializes the actual upstream refresh (sync and async).
	refreshWorkMu sync.Mutex
	// refreshGateMu is a short guard for the single-flight gate and Stop; it is
	// never held across upstream I/O, so a read never blocks behind a refresh.
	refreshGateMu sync.Mutex
	refreshing    bool
	stopped       bool
	refreshWG     sync.WaitGroup

	snapMu      sync.Mutex
	snapshots   map[int64]FinanceItem
	loaded      bool
	refreshedAt time.Time
}

func New(db *store.DB, enc *crypto.Encrypter, registry *adapters.Registry) *Service {
	lifeCtx, lifeCancel := context.WithCancel(context.Background())
	return &Service{
		db: db, enc: enc, registry: registry, now: time.Now,
		lifeCtx: lifeCtx, lifeCancel: lifeCancel,
		snapshots: map[int64]FinanceItem{},
	}
}

// Stop cancels any background finance refresh and waits for it to finish. It
// is idempotent and is registered as a router stopper.
func (s *Service) Stop() {
	s.refreshGateMu.Lock()
	if s.stopped {
		s.refreshGateMu.Unlock()
		return
	}
	s.stopped = true
	cancel := s.lifeCancel
	s.refreshGateMu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.refreshWG.Wait()
}

// lifeContext returns the service lifecycle context, falling back to a plain
// context for a zero-value Service built directly in tests.
func (s *Service) lifeContext() context.Context {
	if s.lifeCtx != nil {
		return s.lifeCtx
	}
	return context.Background()
}

// SetNotifier attaches the operational alert notifier (token expired / low
// balance events).
func (s *Service) SetNotifier(n *webhook.Notifier) {
	s.notifier.Store(n)
}

func (s *Service) Probe(ctx context.Context, channelID int64) (*ProbeResult, error) {
	started := s.now()
	resolved, err := s.resolveUserTarget(channelID)
	if err != nil {
		return nil, err
	}
	defer zeroAccountInput(&resolved.input)
	self, err := resolved.adapter.ProbeSelf(ctx, resolved.input)
	if err != nil && isTransientProbeError(err) {
		// Cloudflare-protected public sites frequently hiccup on a single
		// request (challenge, TLS, timeout). Retry once before recording a
		// failure so a flaky upstream does not flip the badge to "token
		// invalid / unreachable" for one bad sample. Auth rejections (401/403)
		// are not retried — a truly dead token stays dead.
		select {
		case <-ctx.Done():
			return nil, mapAdapterError(err)
		case <-time.After(1200 * time.Millisecond):
		}
		self, err = resolved.adapter.ProbeSelf(ctx, resolved.input)
	}
	if err != nil {
		probeErr := mapAdapterError(err)
		checkedAt := s.now()
		_ = s.db.Channel.RecordAccountProbeFailure(channelID, checkedAt, probeCategory(probeErr))
		_ = s.db.HealthHistory.Append(channelID, domain.ProbeKindAccount, false, 0, probeCategory(probeErr), checkedAt)
		// Verdict-driven state machine: 429 parks the channel until the rate
		// window passes (Retry-After when the upstream provides one); 401/403
		// mark the credential dead. Alerts are per-verdict and throttled.
		var typed *Error
		if errors.As(probeErr, &typed) {
			switch typed.Category {
			case domain.CategoryRateLimited:
				until := s.now().Add(defaultRateLimitPause)
				if retryAfter := retryAfterFrom(err); retryAfter > 0 && retryAfter < maxRateLimitPause {
					until = s.now().Add(retryAfter)
				}
				_ = s.db.Channel.RecordRateLimited(channelID, until)
				if notifier := s.notifier.Load(); notifier != nil {
					notifier.SendAlert(ctx, webhook.AlertWarning, "连接触发限流", fmt.Sprintf("连接 #%d (%s) 被上游限流，暂停至 %s。", channelID, resolved.channel.Name, until.Format(time.RFC3339)))
				}
			case domain.CategoryAccountBanned:
				if notifier := s.notifier.Load(); notifier != nil {
					notifier.SendAlert(ctx, webhook.AlertError, "账号疑似封禁", fmt.Sprintf("连接 #%d (%s) 返回 403，账号可能已被上游封禁。", channelID, resolved.channel.Name))
				}
			case domain.CategoryUpstreamUnauthorized:
				if notifier := s.notifier.Load(); notifier != nil {
					notifier.SendAlert(ctx, webhook.AlertError, "访问令牌失效", fmt.Sprintf("连接 #%d (%s) 的访问令牌已失效，请重新生成后更新凭据。", channelID, resolved.channel.Name))
				}
			}
		}
		return nil, probeErr
	}
	if self.PlatformUserID > 0 {
		_ = s.persistPlatformUserID(resolved.credential, self.PlatformUserID)
	}
	checkedAt := s.now()
	latency := int(checkedAt.Sub(started).Milliseconds())
	if latency < 0 {
		latency = 0
	}
	_ = s.db.Channel.RecordAccountProbeSuccess(channelID, checkedAt)
	_ = s.db.HealthHistory.Append(channelID, domain.ProbeKindAccount, true, latency, "", checkedAt)
	// A successful probe lifts any previous rate-limit pause.
	_ = s.db.Channel.ClearRateLimit(channelID)
	return &ProbeResult{
		ChannelID:      channelID,
		CredentialID:   resolved.credential.ID,
		Username:       self.Username,
		DisplayName:    self.DisplayName,
		PlatformUserID: self.PlatformUserID,
		Quota:          self.Quota,
		UsedQuota:      self.UsedQuota,
		LatencyMs:      latency,
		CheckedAt:      checkedAt,
	}, nil
}

// ProbeAllItem is one channel's access-token check result within a bulk run.
type ProbeAllItem struct {
	ChannelID   int64  `json:"channel_id"`
	ChannelName string `json:"channel_name"`
	OK          bool   `json:"ok"`
	Username    string `json:"username,omitempty"`
	Error       string `json:"error,omitempty"`
}

// ProbeAll checks the access token of every enabled channel that has a user
// credential, and reports per-channel success/failure. Used by the bulk
// "check all access tokens" action in the Connections page.
func (s *Service) ProbeAll(ctx context.Context) ([]ProbeAllItem, error) {
	channels, err := s.db.Channel.ListEnabled()
	if err != nil {
		return nil, internalError("channel_list")
	}
	items := make([]ProbeAllItem, 0, len(channels))
	for index := range channels {
		channel := channels[index]
		item := ProbeAllItem{
			ChannelID:   channel.ID,
			ChannelName: channel.Name,
		}
		probe, probeErr := s.Probe(ctx, channel.ID)
		if probeErr != nil {
			if errors.Is(probeErr, context.Canceled) || errors.Is(probeErr, context.DeadlineExceeded) {
				return nil, probeErr
			}
			item.Error = errorCategory(probeErr)
		} else {
			item.OK = true
			item.Username = probe.Username
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *Service) SyncKeys(ctx context.Context, channelID int64, request SyncKeysRequest) (*SyncKeysResult, error) {
	if request.MaxKeys <= 0 {
		request.MaxKeys = 20
	}
	if request.MaxKeys > 100 {
		request.MaxKeys = 100
	}
	attachToChannel := true
	if request.AttachToChannel != nil {
		attachToChannel = *request.AttachToChannel
	}

	resolved, err := s.resolveUserTarget(channelID)
	if err != nil {
		return nil, err
	}
	// Many New-API forks require New-Api-User (and siblings) for /api/token.
	// Resolve user id from /api/user/self when meta is empty.
	if resolved.input.PlatformUserID <= 0 {
		if self, selfErr := resolved.adapter.ProbeSelf(ctx, resolved.input); selfErr == nil && self.PlatformUserID > 0 {
			resolved.input.PlatformUserID = self.PlatformUserID
			_ = s.persistPlatformUserID(resolved.credential, self.PlatformUserID)
		}
	}
	keys, err := resolved.adapter.ListAPIKeys(ctx, resolved.input, 0, request.MaxKeys)
	if err != nil {
		zeroString(&resolved.input.Secret)
		return nil, mapAdapterError(err)
	}
	result := &SyncKeysResult{
		ChannelID: channelID,
		Listed:    len(keys),
		Items:     make([]SyncKeyItem, 0, len(keys)),
	}
	if len(keys) == 0 {
		result.EmptyList = true
		result.Category = "empty_token_list"
		result.Message = "upstream returned no API tokens (site may hide keys, use a different list API, or account has none)"
		zeroString(&resolved.input.Secret)
		return result, nil
	}

	existing, err := s.db.Credential.ListBySite(resolved.site.ID)
	if err != nil {
		zeroString(&resolved.input.Secret)
		return nil, internalError("credential_list")
	}

	var attachID int64
	for index, key := range keys {
		if index >= request.MaxKeys {
			break
		}
		name := strings.TrimSpace(key.Name)
		if name == "" {
			name = fmt.Sprintf("token-%d", key.ID)
		}
		secret := strings.TrimSpace(key.Secret)
		if secret == "" && key.ID > 0 {
			revealed, revealErr := resolved.adapter.RevealAPIKey(ctx, resolved.input, key.ID)
			if revealErr == nil {
				secret = strings.TrimSpace(revealed)
			}
		}
		if secret == "" {
			result.SkippedMasked++
			result.Items = append(result.Items, SyncKeyItem{
				Name: name, Group: key.Group, UpstreamTokenID: key.ID, Status: "skipped_masked", Category: "key_masked",
			})
			continue
		}

		matchedID, matchErr := s.findMatchingAPIKey(existing, secret)
		if matchErr != nil {
			zeroString(&secret)
			zeroString(&resolved.input.Secret)
			return nil, matchErr
		}
		if matchedID > 0 {
			result.ReusedCredentials++
			// Best-effort: refresh meta name/group on reuse when empty.
			if matched, getErr := s.db.Credential.GetByID(matchedID); getErr == nil && matched != nil {
				metaPayload := map[string]any{"name": name, "group": key.Group}
				if key.ID > 0 {
					metaPayload["upstream_token_id"] = key.ID
				}
				if metaBytes, metaErr := json.Marshal(metaPayload); metaErr == nil {
					matched.MetaJSON = string(metaBytes)
					_ = s.db.Credential.Update(matched)
				}
			}
			result.Items = append(result.Items, SyncKeyItem{
				Name: name, Group: key.Group, CredentialID: matchedID,
				UpstreamTokenID: key.ID, Enabled: true, Status: "reused", Category: "existing_api_key",
			})
			if attachID == 0 {
				attachID = matchedID
			}
			zeroString(&secret)
			continue
		}

		encSecret, encErr := s.enc.Encrypt([]byte(secret))
		zeroString(&secret)
		if encErr != nil {
			zeroString(&resolved.input.Secret)
			return nil, internalError("encryption_failed")
		}
		metaPayload := map[string]any{
			"name":  name,
			"group": key.Group,
		}
		if key.ID > 0 {
			metaPayload["upstream_token_id"] = key.ID
		}
		metaBytes, _ := json.Marshal(metaPayload)
		// New API token status: 1 = enabled, 2 = disabled. Default enabled when unknown.
		status := domain.StatusEnabled
		if key.Status == 2 {
			status = domain.StatusDisabled
		}
		created := &domain.Credential{
			SiteID:    resolved.site.ID,
			Kind:      "api_key",
			SecretEnc: []byte(encSecret),
			MetaJSON:  string(metaBytes),
			Status:    status,
		}
		newID, createErr := s.db.Credential.Create(created)
		if createErr != nil {
			zeroString(&resolved.input.Secret)
			return nil, internalError("credential_create")
		}
		created.ID = newID
		existing = append(existing, *created)
		result.CreatedCredentials++
		result.Items = append(result.Items, SyncKeyItem{
			Name: name, Group: key.Group, CredentialID: newID,
			UpstreamTokenID: key.ID, Enabled: status == domain.StatusEnabled,
			Status: "created", Category: "api_key_imported",
		})
		if attachID == 0 {
			attachID = newID
		}
	}

	// Prune local api_key credentials whose upstream token no longer exists.
	// Compare against the FULL upstream list (up to 100), not the truncated
	// MaxKeys slice, so keys beyond the import cap are never misjudged as
	// deleted.
	if fullKeys, listErr := resolved.adapter.ListAPIKeys(ctx, resolved.input, 0, 100); listErr == nil {
		deleted, removedItems, deleteErr := s.deleteOrphanAPIKeys(resolved, fullKeys, existing)
		if deleteErr != nil {
			zeroString(&resolved.input.Secret)
			return nil, deleteErr
		}
		result.DeletedCredentials = deleted
		result.Items = append(result.Items, removedItems...)
	}
	zeroString(&resolved.input.Secret)

	if attachID == 0 && result.SkippedMasked > 0 && result.CreatedCredentials == 0 && result.ReusedCredentials == 0 {
		result.Category = "keys_masked"
		result.Message = "upstream listed tokens but secrets were masked and could not be revealed; paste the key manually or use a site that exposes full keys"
	} else if attachID > 0 || result.DeletedCredentials > 0 {
		result.Category = "keys_attached"
		result.Message = "API key ready for relay"
		if result.DeletedCredentials > 0 {
			result.Message = fmt.Sprintf("API key sync done; removed %d key(s) deleted upstream", result.DeletedCredentials)
		}
	}

	if attachToChannel && attachID > 0 {
		channel := resolved.channel
		// Only re-point when current bind is missing or not an api_key.
		shouldAttach := channel.CredentialID == nil
		if channel.CredentialID != nil {
			bound, getErr := s.db.Credential.GetByID(*channel.CredentialID)
			if getErr != nil {
				return nil, internalError("credential_lookup")
			}
			if bound == nil || !strings.EqualFold(bound.Kind, "api_key") {
				shouldAttach = true
			} else {
				attachID = bound.ID
			}
		}
		if shouldAttach {
			channel.CredentialID = &attachID
			if updateErr := s.db.Channel.Update(channel); updateErr != nil {
				return nil, internalError("channel_update")
			}
		}
		result.AttachedCredentialID = attachID
	}

	// Default: aggregate keys on the site (one connection, multi-key pool). Opt-in to split.
	splitByGroup := false
	if request.SplitByGroup != nil {
		splitByGroup = *request.SplitByGroup
	}
	if splitByGroup {
		if splitErr := s.ensureGroupChannels(resolved, result); splitErr != nil {
			return nil, splitErr
		}
		if result.CreatedChannels > 0 || result.UpdatedChannels > 0 {
			result.Category = "keys_split_by_group"
			result.Message = fmt.Sprintf(
				"API keys ready; connections by group: created %d, updated %d",
				result.CreatedChannels, result.UpdatedChannels,
			)
		}
	}
	return result, nil
}

// GetPricing fetches the site-wide model price table for a channel's account.
func (s *Service) GetPricing(ctx context.Context, channelID int64) ([]adapters.ModelPrice, error) {
	resolved, err := s.resolveUserTarget(channelID)
	if err != nil {
		return nil, err
	}
	defer zeroAccountInput(&resolved.input)
	prices, err := resolved.adapter.Pricing(ctx, resolved.input)
	if err != nil {
		return nil, mapAdapterError(err)
	}
	return prices, nil
}

// FinanceItem is one channel's account finances: the observed balance (in
// site credit), the quota-per-unit conversion, the normalized quotes, and the
// legacy price table kept for compatibility. BalanceKnown is false whenever
// the upstream did not report a quota — a missing balance must never be
// presented as a zero one.
type FinanceItem struct {
	ChannelID     int64                                `json:"channel_id"`
	AccountID     int64                                `json:"account_id,omitempty"`
	SiteID        int64                                `json:"site_id,omitempty"`
	UpstreamGroup string                               `json:"upstream_group,omitempty"`
	Balance       int64                                `json:"balance"`
	BalanceKnown  bool                                 `json:"balance_known"`
	BalanceStatus string                               `json:"balance_status"`
	QuotaTotal    int64                                `json:"quota_total,omitempty"`
	QuotaUsed     int64                                `json:"quota_used,omitempty"`
	QuotaPerUnit  int64                                `json:"quota_per_unit"`
	Unit          domain.QuoteUnit                     `json:"unit"`
	ObservedAt    string                               `json:"observed_at,omitempty"`
	RefreshError  string                               `json:"refresh_error,omitempty"`
	Prices        map[string]adapters.ModelPrice       `json:"prices"`
	Quotes        map[string]domain.PriceQuoteSnapshot `json:"quotes,omitempty"`
}

// FinanceSnapshot is the console's finance read model: the last known
// per-channel observations plus whether a refresh is currently in flight. It
// is always served from memory or disk and never waits on an upstream.
// BalanceStatus is one of known | unknown | stale | refresh_failed.
type FinanceSnapshot struct {
	Items      []FinanceItem `json:"items"`
	Refreshing bool          `json:"refreshing"`
	ObservedAt string        `json:"observed_at,omitempty"`
}

const (
	// financeCacheTTL is how long an observation is served without triggering
	// another refresh.
	financeCacheTTL = 2 * time.Minute
	// financeProbeTimeout bounds a single upstream finance call.
	financeProbeTimeout = 8 * time.Second
	// financeRefreshTimeout bounds one whole sweep.
	financeRefreshTimeout = 90 * time.Second
	// financeProbeConcurrency caps simultaneous upstream finance probes.
	financeProbeConcurrency = 4

	balanceStatusKnown         = "known"
	balanceStatusUnknown       = "unknown"
	balanceStatusStale         = "stale"
	balanceStatusRefreshFailed = "refresh_failed"
)

// financeGroupKey identifies the account group that shares one probe: the
// same credential, site, base URL and upstream group.
type financeGroupKey struct {
	credentialID int64
	siteID       int64
	baseURL      string
	group        string
}

// financeTarget is one dedupe group's resolved account and its channels.
type financeTarget struct {
	resolved   *resolvedTarget
	channelIDs []int64
}

// financeFacts are the probed values shared by every channel in one dedupe
// group (same credential, site, base URL and upstream group).
type financeFacts struct {
	failed        bool
	refreshError  string
	pricingFailed bool
	balanceKnown  bool
	balanceStatus string
	balance       int64
	quotaTotal    int64
	quotaUsed     int64
	quotaPerUnit  int64
	unit          domain.QuoteUnit
	prices        map[string]adapters.ModelPrice
	quotes        map[string]domain.PriceQuoteSnapshot
	observedAt    time.Time
}

// FinanceSnapshot returns the console's finance view from the last
// observation, triggering a background refresh when it has gone stale. It
// never waits on an upstream.
func (s *Service) FinanceSnapshot(context.Context) (FinanceSnapshot, error) {
	if err := s.ensureLoaded(); err != nil {
		return FinanceSnapshot{}, internalError("snapshot_unavailable")
	}
	items, fresh := s.snapshotItems()
	if !fresh {
		s.triggerRefresh()
	}
	return s.snapshotResult(items), nil
}

// RefreshFinance starts a single-flight background refresh and returns the
// current snapshot with its refresh status. It never blocks on an upstream.
func (s *Service) RefreshFinance(context.Context) (FinanceSnapshot, error) {
	if err := s.ensureLoaded(); err != nil {
		return FinanceSnapshot{}, internalError("snapshot_unavailable")
	}
	s.triggerRefresh()
	items, _ := s.snapshotItems()
	return s.snapshotResult(items), nil
}

// FinanceOverview synchronously refreshes and returns every channel's finance
// item. It is kept for the alert sweep and the daily balance snapshot, which
// run outside the request path. The work is bounded by the service lifecycle
// context so Stop still cancels it.
func (s *Service) FinanceOverview(context.Context) ([]FinanceItem, error) {
	if err := s.ensureLoaded(); err != nil {
		return nil, internalError("snapshot_unavailable")
	}
	if items, fresh := s.snapshotItems(); fresh {
		return items, nil
	}
	ctx, cancel := context.WithTimeout(s.lifeContext(), financeRefreshTimeout)
	defer cancel()
	s.refreshLocked(ctx)
	items, _ := s.snapshotItems()
	return items, nil
}

// ensureLoaded reads the last persisted snapshots once, so a restart can serve
// the console before the first refresh completes.
func (s *Service) ensureLoaded() error {
	s.snapMu.Lock()
	defer s.snapMu.Unlock()
	if s.loaded {
		return nil
	}
	rows, err := s.db.LatestFinanceSnapshots()
	if err != nil {
		return err
	}
	if s.snapshots == nil {
		s.snapshots = map[int64]FinanceItem{}
	}
	for _, row := range rows {
		var item FinanceItem
		if json.Unmarshal(row.Payload, &item) != nil {
			continue
		}
		if !row.ObservedAt.IsZero() {
			item.ObservedAt = row.ObservedAt.UTC().Format(time.RFC3339Nano)
		}
		s.snapshots[row.ChannelID] = item
	}
	s.loaded = true
	return nil
}

// snapshotItems returns a fresh copy of the in-memory snapshots and whether
// the last refresh is still within the TTL.
func (s *Service) snapshotItems() ([]FinanceItem, bool) {
	s.snapMu.Lock()
	items := make([]FinanceItem, 0, len(s.snapshots))
	for _, item := range s.snapshots {
		copy := cloneFinanceItem(item)
		if observed, err := time.Parse(time.RFC3339Nano, item.ObservedAt); copy.BalanceStatus == balanceStatusKnown && (err != nil || s.now().Sub(observed) < 0 || s.now().Sub(observed) >= financeCacheTTL) {
			copy.BalanceStatus = balanceStatusStale
		}
		items = append(items, copy)
	}
	at := s.refreshedAt
	s.snapMu.Unlock()
	sort.Slice(items, func(i, j int) bool { return items[i].ChannelID < items[j].ChannelID })
	age := s.now().Sub(at)
	fresh := !at.IsZero() && age >= 0 && age < financeCacheTTL
	return items, fresh
}

func (s *Service) snapshotResult(items []FinanceItem) FinanceSnapshot {
	s.snapMu.Lock()
	at := s.refreshedAt
	s.snapMu.Unlock()
	if items == nil {
		items = []FinanceItem{}
	}
	observed := ""
	if !at.IsZero() {
		observed = at.UTC().Format(time.RFC3339Nano)
	}
	return FinanceSnapshot{Items: items, Refreshing: s.isRefreshing(), ObservedAt: observed}
}

func (s *Service) isRefreshing() bool {
	s.refreshGateMu.Lock()
	defer s.refreshGateMu.Unlock()
	return s.refreshing
}

// triggerRefresh starts one background refresh if none is running and the
// service has not stopped. The gate is checked and the WaitGroup incremented
// under the same short lock, so Stop can never race a late WaitGroup.Add.
func (s *Service) triggerRefresh() {
	s.refreshGateMu.Lock()
	if s.refreshing || s.stopped {
		s.refreshGateMu.Unlock()
		return
	}
	s.refreshing = true
	s.refreshWG.Add(1)
	s.refreshGateMu.Unlock()
	go func() {
		defer s.refreshWG.Done()
		defer func() {
			s.refreshGateMu.Lock()
			s.refreshing = false
			s.refreshGateMu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(s.lifeContext(), financeRefreshTimeout)
		defer cancel()
		s.refreshLocked(ctx)
	}()
}

func (s *Service) refreshLocked(ctx context.Context) {
	s.refreshWorkMu.Lock()
	defer s.refreshWorkMu.Unlock()
	s.refresh(ctx)
}

// refresh probes every enabled channel that has a user credential, persists
// the observations, and stores them in memory. Channels sharing one account
// credential, site, base URL and upstream group are probed once.
func (s *Service) refresh(ctx context.Context) {
	channels, err := s.db.Channel.ListEnabled()
	if err != nil {
		return
	}
	groups := make(map[financeGroupKey]*financeTarget)
	order := make([]financeGroupKey, 0, len(channels))
	for index := range channels {
		channel := channels[index]
		resolved, resolveErr := s.resolveUserTarget(channel.ID)
		if resolveErr != nil {
			continue
		}
		key := financeGroupKey{
			credentialID: resolved.credential.ID,
			siteID:       resolved.site.ID,
			baseURL:      resolved.input.BaseURL,
			group:        financeGroupName(channel.GroupName),
		}
		if existing, ok := groups[key]; ok {
			// Same account/group/URL: one probe serves every channel, so the
			// duplicate decrypted input is released immediately.
			zeroAccountInput(&resolved.input)
			existing.channelIDs = append(existing.channelIDs, channel.ID)
			continue
		}
		groups[key] = &financeTarget{resolved: resolved, channelIDs: []int64{channel.ID}}
		order = append(order, key)
	}

	sem := make(chan struct{}, financeProbeConcurrency)
	var wg sync.WaitGroup
	var itemMu sync.Mutex
	items := make([]FinanceItem, 0)
	for _, key := range order {
		target := groups[key]
		wg.Add(1)
		go func(key financeGroupKey, target *financeTarget) {
			defer wg.Done()
			acquired := false
			select {
			case sem <- struct{}{}:
				acquired = true
			case <-ctx.Done():
			}
			if acquired {
				defer func() { <-sem }()
			}
			facts := s.probeFinanceGroup(ctx, target.resolved)
			for _, channelID := range target.channelIDs {
				item := s.financeItemForChannel(channelID, key, facts)
				itemMu.Lock()
				items = append(items, item)
				itemMu.Unlock()
			}
		}(key, target)
	}
	wg.Wait()
	s.storeSnapshots(items)
}

// storeSnapshots replaces the in-memory readings for the channels just probed
// and persists each as an append-only observation. A channel that vanished
// from the enabled list keeps its last reading rather than silently dropping.
func (s *Service) storeSnapshots(items []FinanceItem) {
	s.snapMu.Lock()
	if s.snapshots == nil {
		s.snapshots = map[int64]FinanceItem{}
	}
	for _, item := range items {
		s.snapshots[item.ChannelID] = cloneFinanceItem(item)
	}
	s.refreshedAt = s.now()
	s.loaded = true
	s.snapMu.Unlock()
	for _, item := range items {
		s.persistSnapshot(item)
	}
}

func (s *Service) persistSnapshot(item FinanceItem) {
	payload, err := json.Marshal(item)
	if err != nil {
		return
	}
	observedAt := s.now()
	if parsed, parseErr := time.Parse(time.RFC3339Nano, item.ObservedAt); parseErr == nil {
		observedAt = parsed
	}
	_ = s.db.SaveFinanceSnapshot(item.ChannelID, payload, observedAt)
}

func (s *Service) previousItem(channelID int64) (FinanceItem, bool) {
	s.snapMu.Lock()
	defer s.snapMu.Unlock()
	item, ok := s.snapshots[channelID]
	if !ok {
		return FinanceItem{}, false
	}
	return cloneFinanceItem(item), true
}

// probeFinanceGroup makes one probe of an account group: identity, balance,
// pricing and quota-per-unit. A transport/auth failure is reported as facts
// with failed=true rather than an error, so the caller keeps the last reading.
func (s *Service) probeFinanceGroup(ctx context.Context, resolved *resolvedTarget) financeFacts {
	defer zeroAccountInput(&resolved.input)
	now := s.now()
	facts := financeFacts{
		observedAt:    now,
		balanceStatus: balanceStatusUnknown,
		prices:        map[string]adapters.ModelPrice{},
		quotes:        map[string]domain.PriceQuoteSnapshot{},
	}
	callCtx, cancel := context.WithTimeout(ctx, financeProbeTimeout)
	defer cancel()
	self, selfErr := resolved.adapter.ProbeSelf(callCtx, resolved.input)
	if selfErr != nil {
		facts.failed = true
		facts.balanceStatus = balanceStatusRefreshFailed
		facts.refreshError = errorCategory(mapAdapterError(selfErr))
		return facts
	}
	if self.Quota != nil {
		facts.balanceKnown = true
		facts.balanceStatus = balanceStatusKnown
		facts.balance = *self.Quota
	} else {
		// A missing quota is unknown, never a zero balance: reporting 0 would
		// fabricate a low-balance alert for a site that simply omits the field.
		facts.balanceKnown = false
		facts.balanceStatus = balanceStatusUnknown
	}
	facts.quotaTotal = int64Value(self.Quota)
	facts.quotaUsed = int64Value(self.UsedQuota)

	prices, priceErr := resolved.adapter.Pricing(callCtx, resolved.input)
	if priceErr != nil {
		prices = nil
		facts.pricingFailed = true
		facts.refreshError = "pricing_refresh_failed"
	}
	quotaPerUnit, quotaErr := resolved.adapter.QuotaPerUnit(callCtx, resolved.input)
	if quotaErr != nil || quotaPerUnit <= 0 {
		quotaPerUnit = 500000 // New-API default: 1 unit = 500k quota.
	}
	facts.quotaPerUnit = quotaPerUnit
	facts.unit = financeCreditUnit(resolved.site.ID, quotaPerUnit)

	siteID := resolved.site.ID
	accountID := resolved.credential.ID
	group := financeGroupName(resolved.channel.GroupName)
	for _, published := range prices {
		// One shared normalizer (internal/adapters/pricing.go) owns every
		// published shape; the group is the channel's upstream group, so two
		// groups never share a rate.
		quote, ok := adapters.NormalizePrice(adapters.WithPricingGroup(published, group), quotaPerUnit)
		if !ok {
			continue
		}
		quote.Unit = financeQuoteUnit(siteID, quotaPerUnit, published)
		if price, priced := legacyPrice(quote); priced {
			facts.prices[quote.Model] = price
		}
		snapshot := adapters.SnapshotQuote(quote, siteID, accountID, "site_account", now)
		if id, saveErr := s.db.SavePriceQuote(snapshot); saveErr == nil {
			snapshot.ID = id
		}
		facts.quotes[quote.Model] = snapshot
	}

	// Low-balance alert: only a known balance below one unit (site credit).
	if facts.balanceKnown && facts.balance < quotaPerUnit {
		if notifier := s.notifier.Load(); notifier != nil {
			notifier.SendAlert(context.Background(), webhook.AlertWarning, "余额不足", fmt.Sprintf("连接 #%d (%s) 余额仅剩 %d 额度（低于 1 单位），请及时充值。", resolved.channel.ID, resolved.channel.Name, facts.balance))
		}
	}
	return facts
}

// financeItemForChannel builds one channel's item from a group's facts. A
// failed probe keeps the channel's last good reading and observation time
// instead of reporting a false zero.
func (s *Service) financeItemForChannel(channelID int64, key financeGroupKey, facts financeFacts) FinanceItem {
	item := FinanceItem{
		ChannelID:     channelID,
		AccountID:     key.credentialID,
		SiteID:        key.siteID,
		UpstreamGroup: key.group,
		Balance:       facts.balance,
		BalanceKnown:  facts.balanceKnown,
		BalanceStatus: facts.balanceStatus,
		QuotaTotal:    facts.quotaTotal,
		QuotaUsed:     facts.quotaUsed,
		QuotaPerUnit:  facts.quotaPerUnit,
		Unit:          facts.unit,
		ObservedAt:    facts.observedAt.UTC().Format(time.RFC3339Nano),
		RefreshError:  facts.refreshError,
		Prices:        facts.prices,
		Quotes:        facts.quotes,
	}
	if facts.pricingFailed {
		if prev, ok := s.previousItem(channelID); ok && prev.AccountID == key.credentialID && prev.SiteID == key.siteID && prev.UpstreamGroup == key.group {
			item.Prices = prev.Prices
			item.Quotes = prev.Quotes
		}
	}
	if facts.failed {
		if prev, ok := s.previousItem(channelID); ok && prev.AccountID == key.credentialID && prev.SiteID == key.siteID && prev.UpstreamGroup == key.group {
			item.Balance = prev.Balance
			item.BalanceKnown = prev.BalanceKnown
			item.QuotaTotal = prev.QuotaTotal
			item.QuotaUsed = prev.QuotaUsed
			item.QuotaPerUnit = prev.QuotaPerUnit
			item.Unit = prev.Unit
			item.Prices = prev.Prices
			item.Quotes = prev.Quotes
			item.ObservedAt = prev.ObservedAt
		}
	}
	return item
}

func financeGroupName(group string) string {
	trimmed := strings.TrimSpace(group)
	if trimmed == "" {
		return "default"
	}
	return trimmed
}

// financeCreditUnit is the native unit of a site-credit quote: quota-per-unit
// ties the amount to the site's own currency, which the operator may later
// declare as USD. It never dresses a native amount as USD.
func financeCreditUnit(siteID, quotaPerUnit int64) domain.QuoteUnit {
	return domain.QuoteUnit{Kind: "site_credit", Code: fmt.Sprintf("credit:site/%d", siteID), QuotaPerUnit: quotaPerUnit}
}

// financeQuoteUnit marks a direct USD-per-1M quote as ISO/USD; everything else
// stays native site credit. Cosmetic currencies never overwrite the unit.
func financeQuoteUnit(siteID, quotaPerUnit int64, published adapters.ModelPrice) domain.QuoteUnit {
	if published.TokenUSD != nil && published.TokenUSD.Input > 0 {
		return domain.QuoteUnit{Kind: "iso", Code: "USD"}
	}
	return financeCreditUnit(siteID, quotaPerUnit)
}

// legacyPrice keeps the pre-existing Prices map populated for compatibility;
// the Quotes map carries the unit-aware observations the console now uses.
func legacyPrice(quote adapters.PriceQuote) (adapters.ModelPrice, bool) {
	price := adapters.ModelPrice{
		Model:       quote.Model,
		Currency:    quote.Currency,
		Mode:        quote.Mode,
		BillingExpr: quote.Raw,
	}
	if quote.Unparsed {
		return price, true
	}
	if quote.Mode == "fixed" {
		if quote.PerRequest <= 0 {
			return price, false
		}
		price.PriceUSD = quote.PerRequest
		return price, true
	}
	if quote.InputPerMillion <= 0 {
		return price, false
	}
	price.PriceUSD = quote.InputPerMillion
	price.OutputUSD = quote.OutputPerMillion
	return price, true
}

func cloneFinanceItem(item FinanceItem) FinanceItem {
	out := item
	out.Unit = cloneQuoteUnit(item.Unit)
	if item.Prices != nil {
		out.Prices = make(map[string]adapters.ModelPrice, len(item.Prices))
		for model, price := range item.Prices {
			out.Prices[model] = price
		}
	}
	if item.Quotes != nil {
		out.Quotes = make(map[string]domain.PriceQuoteSnapshot, len(item.Quotes))
		for model, quote := range item.Quotes {
			out.Quotes[model] = cloneQuoteSnapshot(quote)
		}
	}
	return out
}

func cloneQuoteUnit(unit domain.QuoteUnit) domain.QuoteUnit {
	out := unit
	out.DisplayRate = cloneFloat(unit.DisplayRate)
	out.USDPerUnit = cloneFloat(unit.USDPerUnit)
	return out
}

func cloneQuoteSnapshot(quote domain.PriceQuoteSnapshot) domain.PriceQuoteSnapshot {
	out := quote
	out.Unit = cloneQuoteUnit(quote.Unit)
	out.Values = domain.QuoteValues{
		Mode:       quote.Values.Mode,
		Input:      cloneFloat(quote.Values.Input),
		Output:     cloneFloat(quote.Values.Output),
		CacheRead:  cloneFloat(quote.Values.CacheRead),
		CacheWrite: cloneFloat(quote.Values.CacheWrite),
		PerRequest: cloneFloat(quote.Values.PerRequest),
	}
	return out
}

func cloneFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

// RecordBalanceHistory snapshots every channel's current balance into
// balance_history. It reuses the FinanceOverview cache when warm (the daily
// sweep and admin visits share the same upstream probes) and forces a fresh
// probe when the cache has expired. Returns the number of rows written.
func (s *Service) RecordBalanceHistory(ctx context.Context) (int, error) {
	items, err := s.FinanceOverview(ctx)
	if err != nil {
		return 0, err
	}
	// One credential backs every channel of the same site account (often with
	// several upstream groups), and the balance belongs to the account, not the
	// channel. Record it once per account, using the lowest channel id as the
	// stable representative; an unknown / stale / failed reading is skipped
	// rather than written as a false zero.
	representative := map[int64]FinanceItem{}
	direct := make([]FinanceItem, 0)
	for _, item := range items {
		if !item.BalanceKnown || item.BalanceStatus != balanceStatusKnown {
			continue
		}
		if item.AccountID <= 0 {
			direct = append(direct, item)
			continue
		}
		if current, ok := representative[item.AccountID]; !ok || item.ChannelID < current.ChannelID {
			representative[item.AccountID] = item
		}
	}
	ordered := make([]FinanceItem, 0, len(representative)+len(direct))
	for _, item := range representative {
		ordered = append(ordered, item)
	}
	ordered = append(ordered, direct...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ChannelID < ordered[j].ChannelID })

	now := s.now()
	written := 0
	for _, item := range ordered {
		name := ""
		if channel, getErr := s.db.Channel.GetByID(item.ChannelID); getErr == nil && channel != nil {
			name = channel.Name
		}
		if insertErr := s.db.InsertBalanceHistory(item.ChannelID, name, item.Balance, now); insertErr != nil {
			continue // one bad row must not abort the whole snapshot
		}
		written++
	}
	return written, nil
}

// BalanceHistory returns snapshots from the last N days in chronological order
// (oldest first) for trend rendering.
func (s *Service) BalanceHistory(ctx context.Context, days int) ([]store.BalanceHistoryPoint, error) {
	return s.db.ListBalanceHistory(days)
}

// PruneBalanceHistory removes snapshots older than retentionDays.
func (s *Service) PruneBalanceHistory(ctx context.Context, retentionDays int) (int, error) {
	return s.db.PruneBalanceHistory(retentionDays)
}

func int64Value(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

// ListTokenGroups returns the distinct token groups the upstream account has
// access to, so the create-key dialog can offer a pick list instead of a free
// text field. It prefers the New-API family's /api/user/self/groups endpoint
// (usable groups even with an empty token list) and falls back to enumerating
// groups from the account's token list. An empty (non-nil) slice is returned
// when neither source reports groups; a transport/auth failure surfaces as an
// error so the caller can tell "no groups" from "cannot reach the upstream".
func (s *Service) ListTokenGroups(ctx context.Context, channelID int64) ([]string, error) {
	resolved, err := s.resolveUserTarget(channelID)
	if err != nil {
		return nil, err
	}
	defer zeroAccountInput(&resolved.input)

	// Many New-API forks require New-Api-User (and siblings) for /api/token.
	if resolved.input.PlatformUserID <= 0 {
		if self, selfErr := resolved.adapter.ProbeSelf(ctx, resolved.input); selfErr == nil && self.PlatformUserID > 0 {
			resolved.input.PlatformUserID = self.PlatformUserID
			_ = s.persistPlatformUserID(resolved.credential, self.PlatformUserID)
		}
	}

	// Preferred source: /api/user/self/groups reports every group the account
	// can use, even when the account holds no tokens yet (e.g. Ark-style sites
	// whose token list comes back empty). Falls back to token-list enumeration.
	if groups, groupsErr := resolved.adapter.ListTokenGroups(ctx, resolved.input); groupsErr == nil {
		seen := make(map[string]struct{}, len(groups))
		clean := make([]string, 0, len(groups))
		for _, group := range groups {
			normalized := normalizeTokenGroup(group)
			if normalized == "" {
				continue
			}
			if _, exists := seen[normalized]; exists {
				continue
			}
			seen[normalized] = struct{}{}
			clean = append(clean, normalized)
		}
		if len(clean) > 0 {
			return clean, nil
		}
	}

	keys, err := resolved.adapter.ListAPIKeys(ctx, resolved.input, 0, 100)
	if err != nil {
		return nil, mapAdapterError(err)
	}
	seen := make(map[string]struct{})
	groups := make([]string, 0, 4)
	for _, key := range keys {
		group := normalizeTokenGroup(key.Group)
		if _, exists := seen[group]; exists {
			continue
		}
		seen[group] = struct{}{}
		groups = append(groups, group)
	}
	if len(groups) == 0 {
		// Upstream reached and returned an empty token list: treat "default"
		// as the only guaranteed group rather than blocking creation.
		groups = []string{"default"}
	}
	return groups, nil
}

// CreateKey creates a brand-new API key on the upstream via the account's
// access_token / session credential, then stores it as an api_key credential on
// the site (bound to the channel) so it can be used for model relay.
func (s *Service) CreateKey(ctx context.Context, channelID int64, request CreateKeyRequest) (*CreateKeyResult, error) {
	resolved, err := s.resolveUserTarget(channelID)
	if err != nil {
		return nil, err
	}
	defer zeroAccountInput(&resolved.input)

	// Many New-API forks require New-Api-User (and siblings) for /api/token.
	// Resolve user id from /api/user/self when meta is empty.
	if resolved.input.PlatformUserID <= 0 {
		if self, selfErr := resolved.adapter.ProbeSelf(ctx, resolved.input); selfErr == nil && self.PlatformUserID > 0 {
			resolved.input.PlatformUserID = self.PlatformUserID
			_ = s.persistPlatformUserID(resolved.credential, self.PlatformUserID)
		}
	}

	name := strings.TrimSpace(request.Name)
	if name == "" {
		name = "gateway-auto"
	}
	group := normalizeTokenGroup(request.Group)
	created, err := resolved.adapter.CreateAPIKey(ctx, resolved.input, adapters.NewAPIKeyRequest{
		Name:           name,
		Group:          group,
		UnlimitedQuota: request.UnlimitedQuota,
	})
	if err != nil {
		return nil, mapAdapterError(err)
	}
	secret := strings.TrimSpace(created.Secret)
	// Fast path: many forks serve the full key from the per-token reveal
	// endpoint right away; only fall back to the full-list import when that
	// fails.
	if secret == "" && created.ID > 0 {
		if revealed, revealErr := resolved.adapter.RevealAPIKey(ctx, resolved.input, created.ID); revealErr == nil {
			secret = strings.TrimSpace(revealed)
		}
	}
	// credentialID is resolved either from the direct secret path below or by
	// the full-list import fallback when the create response came back masked.
	var credentialID int64
	if secret == "" {
		// Some forks mask the secret in the create response (and often in the
		// token list too), exposing the full sk- only through the per-token
		// reveal endpoint — and the fresh token may take a moment to appear in
		// the list at all. Rather than guessing which list item we just
		// created (create-response ids and names are unreliable across forks),
		// mirror the manual "Sync API keys" action: import the whole list with
		// per-item reveal and dedupe. Retry once for list-propagation delay.
		attachFalse := false
		for attempt := 0; attempt < 2 && credentialID == 0; attempt++ {
			if attempt > 0 {
				select {
				case <-ctx.Done():
					return nil, mapAdapterError(ctx.Err())
				case <-time.After(time.Second):
				}
			}
			syncResult, syncErr := s.SyncKeys(ctx, channelID, SyncKeysRequest{AttachToChannel: &attachFalse})
			if syncErr != nil {
				break
			}
			credentialID = pickCreatedItem(syncResult.Items, created.ID, name)
			if credentialID == 0 && syncResult.SkippedMasked > 0 {
				// Tokens are listed but every secret is masked and cannot be
				// revealed — the sync path cannot help either; fail fast.
				break
			}
		}
		if credentialID == 0 {
			return nil, &Error{Kind: ErrorUpstream, Category: "token_created_but_secret_masked"}
		}
	} else {
		// Store as an api_key credential; if the same secret somehow already
		// exists, reuse that row instead of duplicating.
		existing, err := s.db.Credential.ListBySite(resolved.site.ID)
		if err != nil {
			return nil, internalError("credential_list")
		}
		matchedID, matchErr := s.findMatchingAPIKey(existing, secret)
		if matchErr != nil {
			return nil, matchErr
		}
		credentialID = matchedID
		if credentialID == 0 {
			encSecret, encErr := s.enc.Encrypt([]byte(secret))
			if encErr != nil {
				return nil, internalError("encryption_failed")
			}
			metaPayload := map[string]any{"name": name, "group": group}
			if created.ID > 0 {
				metaPayload["upstream_token_id"] = created.ID
			}
			metaBytes, _ := json.Marshal(metaPayload)
			newID, createErr := s.db.Credential.Create(&domain.Credential{
				SiteID:    resolved.site.ID,
				Kind:      "api_key",
				SecretEnc: []byte(encSecret),
				MetaJSON:  string(metaBytes),
				Status:    domain.StatusEnabled,
			})
			if createErr != nil {
				return nil, internalError("credential_create")
			}
			credentialID = newID
		}
	}

	// Point the channel at the new key so relay can use it right away.
	channel := resolved.channel
	bound := channel.CredentialID
	if bound == nil || *bound != credentialID {
		channel.CredentialID = &credentialID
		if updateErr := s.db.Channel.Update(channel); updateErr != nil {
			return nil, internalError("channel_update")
		}
	}

	return &CreateKeyResult{
		CredentialID: credentialID,
		Name:         name,
		Group:        group,
		Category:     "api_key_created",
		Message:      "API key created and attached to the connection",
	}, nil
}

// pickCreatedItem locates the credential imported by a sync result that
// corresponds to the token just created upstream. Preference order: exact
// upstream token id (create responses usually carry one), then name, then the
// first freshly imported item as a last resort (forks that neither return an id
// nor keep the name). Returns 0 when nothing matches.
func pickCreatedItem(items []SyncKeyItem, upstreamID int64, name string) int64 {
	var firstCreated int64
	for _, item := range items {
		if item.CredentialID <= 0 {
			continue
		}
		if item.Status != "created" && item.Status != "reused" {
			continue
		}
		if upstreamID > 0 && item.UpstreamTokenID == upstreamID {
			return item.CredentialID
		}
		if item.Status == "created" && firstCreated == 0 {
			firstCreated = item.CredentialID
		}
		if upstreamID == 0 && item.Name == name && item.Status == "created" {
			return item.CredentialID
		}
	}
	return firstCreated
}

// deleteOrphanAPIKeys removes local api_key credentials whose upstream token
// no longer exists (identified via meta upstream_token_id), keeping the local
// key pool aligned with the upstream. Credentials pasted manually (no upstream
// id) are left alone. Channels bound to a removed key are unbound so relay
// falls back to the remaining site pool instead of a dangling reference.
func (s *Service) deleteOrphanAPIKeys(resolved *resolvedTarget, upstream []adapters.UpstreamAPIKey, existing []domain.Credential) (int, []SyncKeyItem, error) {
	upstreamIDs := make(map[int64]struct{}, len(upstream))
	for _, key := range upstream {
		if key.ID > 0 {
			upstreamIDs[key.ID] = struct{}{}
		}
	}
	if len(upstreamIDs) == 0 {
		// No upstream ids to compare against — nothing provably orphaned.
		return 0, nil, nil
	}
	allChannels, err := s.db.Channel.List()
	if err != nil {
		return 0, nil, internalError("channel_list")
	}
	removed := make([]SyncKeyItem, 0, 1)
	for index := range existing {
		cred := existing[index]
		if !strings.EqualFold(cred.Kind, "api_key") {
			continue
		}
		upstreamID, metaErr := upstreamTokenID(cred.MetaJSON)
		if metaErr != nil || upstreamID <= 0 {
			continue
		}
		if _, stillThere := upstreamIDs[upstreamID]; stillThere {
			continue
		}
		// Upstream deleted this token: remove it locally too.
		if err := s.db.Credential.Delete(cred.ID); err != nil {
			return 0, nil, internalError("credential_delete")
		}
		for channelIndex := range allChannels {
			channel := &allChannels[channelIndex]
			if channel.CredentialID == nil || *channel.CredentialID != cred.ID {
				continue
			}
			channel.CredentialID = nil
			if updateErr := s.db.Channel.Update(channel); updateErr != nil {
				return 0, nil, internalError("channel_update")
			}
		}
		removed = append(removed, SyncKeyItem{
			CredentialID:    cred.ID,
			UpstreamTokenID: upstreamID,
			Status:          "deleted",
			Category:        "api_key_removed",
		})
	}
	return len(removed), removed, nil
}

// upstreamTokenID extracts the upstream token id from a credential's meta JSON.
func upstreamTokenID(raw string) (int64, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, nil
	}
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.UseNumber()
	var metadata struct {
		UpstreamTokenID *json.Number `json:"upstream_token_id"`
	}
	if err := decoder.Decode(&metadata); err != nil {
		return 0, err
	}
	if metadata.UpstreamTokenID == nil {
		return 0, nil
	}
	id, err := metadata.UpstreamTokenID.Int64()
	if err != nil || id <= 0 {
		return 0, errors.New("upstream_token_id must be a positive integer")
	}
	return id, nil
}

// ensureGroupChannels creates or reuses one channel per New API token group on the site.
// Same model can later hang both group channels as route members (priority/weight).
func (s *Service) ensureGroupChannels(resolved *resolvedTarget, result *SyncKeysResult) error {
	if resolved == nil || result == nil || resolved.site == nil {
		return nil
	}
	// group -> preferred enabled api_key credential id (first seen wins).
	groupToCredential := map[string]int64{}
	for _, item := range result.Items {
		if item.CredentialID <= 0 {
			continue
		}
		if item.Status != "created" && item.Status != "reused" {
			continue
		}
		groupKey := normalizeTokenGroup(item.Group)
		if _, exists := groupToCredential[groupKey]; exists {
			continue
		}
		// Prefer enabled credentials.
		cred, err := s.db.Credential.GetByID(item.CredentialID)
		if err != nil || cred == nil {
			continue
		}
		if cred.Status != domain.StatusEnabled {
			continue
		}
		groupToCredential[groupKey] = item.CredentialID
	}
	if len(groupToCredential) == 0 {
		return nil
	}

	allChannels, err := s.db.Channel.List()
	if err != nil {
		return internalError("channel_list")
	}
	siteChannels := make([]domain.Channel, 0)
	for _, channel := range allChannels {
		if channel.SiteID != nil && *channel.SiteID == resolved.site.ID {
			siteChannels = append(siteChannels, channel)
		}
	}

	siteName := strings.TrimSpace(resolved.site.Name)
	if siteName == "" {
		siteName = "site"
	}
	typeHint := strings.TrimSpace(resolved.channel.TypeHint)
	if typeHint == "" {
		typeHint = strings.TrimSpace(resolved.site.Platform)
	}
	if typeHint == "" {
		typeHint = "openai-compatible"
	}

	result.GroupChannels = make([]GroupChannelItem, 0, len(groupToCredential))
	for groupKey, credentialID := range groupToCredential {
		channelName := siteName + " · " + groupKey
		existing := findChannelForGroup(siteChannels, groupKey, credentialID)
		if existing != nil {
			changed := false
			if existing.CredentialID == nil || *existing.CredentialID != credentialID {
				existing.CredentialID = &credentialID
				changed = true
			}
			if strings.TrimSpace(existing.GroupName) != groupKey {
				existing.GroupName = groupKey
				changed = true
			}
			// Keep a readable name if still generic.
			if strings.TrimSpace(existing.Name) == "" || existing.Name == siteName {
				existing.Name = channelName
				changed = true
			}
			status := "reused"
			if changed {
				if updateErr := s.db.Channel.Update(existing); updateErr != nil {
					return internalError("channel_update")
				}
				status = "updated"
				result.UpdatedChannels++
			}
			result.GroupChannels = append(result.GroupChannels, GroupChannelItem{
				Group: groupKey, ChannelID: existing.ID, CredentialID: credentialID,
				Name: existing.Name, Status: status,
			})
			continue
		}

		siteID := resolved.site.ID
		credID := credentialID
		created := &domain.Channel{
			SiteID:       &siteID,
			CredentialID: &credID,
			Name:         channelName,
			BaseURL:      "", // inherit site base URL
			GroupName:    groupKey,
			Priority:     0,
			Weight:       100,
			Status:       domain.StatusEnabled,
			TypeHint:     typeHint,
		}
		newID, createErr := s.db.Channel.Create(created)
		if createErr != nil {
			return internalError("channel_create")
		}
		created.ID = newID
		siteChannels = append(siteChannels, *created)
		result.CreatedChannels++
		result.GroupChannels = append(result.GroupChannels, GroupChannelItem{
			Group: groupKey, ChannelID: newID, CredentialID: credentialID,
			Name: channelName, Status: "created",
		})
	}
	return nil
}

func normalizeTokenGroup(group string) string {
	trimmed := strings.TrimSpace(group)
	if trimmed == "" {
		return "default"
	}
	return trimmed
}

func findChannelForGroup(channels []domain.Channel, groupKey string, credentialID int64) *domain.Channel {
	// 1) Exact group_name match on this site.
	for index := range channels {
		if normalizeTokenGroup(channels[index].GroupName) == groupKey {
			return &channels[index]
		}
	}
	// 2) Already bound to this credential.
	for index := range channels {
		if channels[index].CredentialID != nil && *channels[index].CredentialID == credentialID {
			return &channels[index]
		}
	}
	// 3) Name suffix " · group" from a previous split.
	suffix := " · " + groupKey
	for index := range channels {
		if strings.HasSuffix(channels[index].Name, suffix) {
			return &channels[index]
		}
	}
	return nil
}

type resolvedTarget struct {
	channel    *domain.Channel
	site       *domain.Site
	credential *domain.Credential
	adapter    adapters.AccountAdapter
	input      adapters.AccountInput
}

func (s *Service) resolveUserTarget(channelID int64) (*resolvedTarget, error) {
	channel, err := s.db.Channel.GetByID(channelID)
	if err != nil {
		return nil, internalError("channel_lookup")
	}
	if channel == nil {
		return nil, &Error{Kind: ErrorNotFound, Category: "channel_not_found"}
	}
	if channel.SiteID == nil {
		return nil, unavailable("site_unavailable")
	}
	site, err := s.db.Site.GetByID(*channel.SiteID)
	if err != nil {
		return nil, internalError("site_lookup")
	}
	if site == nil || site.Status != domain.StatusEnabled {
		return nil, unavailable("site_unavailable")
	}
	credential, err := s.pickUserCredential(site.ID, channel.CredentialID)
	if err != nil {
		return nil, err
	}
	adapter, ok := s.registry.ResolveAccount(firstNonEmpty(channel.TypeHint, site.Platform))
	if !ok {
		adapter, ok = s.registry.ResolveAccount(site.Platform)
	}
	if !ok {
		return nil, unavailable("unsupported_adapter")
	}
	platformUserID, metaErr := platformUserID(credential.MetaJSON)
	if metaErr != nil {
		return nil, unavailable("invalid_metadata")
	}
	plaintext, decErr := s.enc.Decrypt(string(credential.SecretEnc))
	if decErr != nil {
		zero(plaintext)
		plaintext = nil
	}
	cookiePlain, cookieErr := s.enc.Decrypt(string(credential.CookieEnc))
	if cookieErr != nil {
		zero(cookiePlain)
		cookiePlain = nil
	}
	if len(plaintext) == 0 && len(cookiePlain) == 0 {
		zero(plaintext)
		zero(cookiePlain)
		return nil, unavailable(domain.CategoryCredentialUnavailable)
	}
	baseURL := channel.BaseURL
	if baseURL == "" {
		baseURL = site.BaseURL
	}
	result := &resolvedTarget{
		channel:    channel,
		site:       site,
		credential: credential,
		adapter:    adapter,
		input: adapters.AccountInput{
			BaseURL:        baseURL,
			Secret:         string(plaintext),
			Cookie:         string(cookiePlain),
			AuthMode:       accountAuthMode(credential.AuthMode, len(plaintext) > 0, len(cookiePlain) > 0),
			PlatformUserID: platformUserID,
		},
	}
	zero(plaintext)
	zero(cookiePlain)
	return result, nil
}

func (s *Service) pickUserCredential(siteID int64, preferredID *int64) (*domain.Credential, error) {
	if preferredID != nil {
		cred, err := s.db.Credential.GetByID(*preferredID)
		if err != nil {
			return nil, internalError("credential_lookup")
		}
		if cred != nil && cred.SiteID == siteID && isUserKind(cred.Kind) &&
			cred.Status == domain.StatusEnabled && hasAccountAuthMaterial(cred) {
			return cred, nil
		}
	}
	list, err := s.db.Credential.ListBySite(siteID)
	if err != nil {
		return nil, internalError("credential_list")
	}
	for index := range list {
		cred := list[index]
		if isUserKind(cred.Kind) && cred.Status == domain.StatusEnabled && hasAccountAuthMaterial(&cred) {
			return &cred, nil
		}
	}
	return nil, unavailable("user_credential_unavailable")
}

func (s *Service) findMatchingAPIKey(existing []domain.Credential, secret string) (int64, error) {
	secretBytes := []byte(secret)
	for index := range existing {
		cred := existing[index]
		if !strings.EqualFold(cred.Kind, "api_key") || cred.Status != domain.StatusEnabled || len(cred.SecretEnc) == 0 {
			continue
		}
		plaintext, err := s.enc.Decrypt(string(cred.SecretEnc))
		if err != nil {
			continue
		}
		equal := subtle.ConstantTimeCompare(plaintext, secretBytes) == 1
		zero(plaintext)
		if equal {
			return cred.ID, nil
		}
	}
	return 0, nil
}

func (s *Service) persistPlatformUserID(credential *domain.Credential, userID int64) error {
	if userID <= 0 || credential == nil {
		return nil
	}
	current, err := platformUserID(credential.MetaJSON)
	if err != nil || current == userID {
		return err
	}
	meta := map[string]any{}
	if strings.TrimSpace(credential.MetaJSON) != "" {
		if err := json.Unmarshal([]byte(credential.MetaJSON), &meta); err != nil {
			meta = map[string]any{}
		}
	}
	meta["platform_user_id"] = userID
	encoded, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	credential.MetaJSON = string(encoded)
	return s.db.Credential.Update(credential)
}

// Rate-limit pause bounds: default 60s when the upstream sends no Retry-After;
// upstream Retry-After values above 1h are capped.
const (
	defaultRateLimitPause = 60 * time.Second
	maxRateLimitPause     = time.Hour
)

// retryAfterFrom extracts an upstream Retry-After hint from an adapter status
// error, when present. Returns 0 when unknown.
func retryAfterFrom(err error) time.Duration {
	var adapterErr *adapters.Error
	if !errors.As(err, &adapterErr) || adapterErr.RetryAfter <= 0 {
		return 0
	}
	return adapterErr.RetryAfter
}

// probeCategory extracts the redacted failure category used for
// last_account_probe_error.
func probeCategory(err error) string {
	var probeErr *Error
	if errors.As(err, &probeErr) && probeErr.Category != "" {
		return probeErr.Category
	}
	return domain.CategoryUpstreamFailure
}

// isTransientProbeError reports whether an adapter error is worth one retry:
// transport-level failures (timeout, TLS, connection reset, CF challenge)
// only. Auth rejections are excluded — retrying a 401 is pointless.
func isTransientProbeError(err error) bool {
	var adapterErr *adapters.Error
	if errors.As(err, &adapterErr) {
		return adapterErr.Kind == adapters.ErrorTransport
	}
	return false
}

func mapAdapterError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var adapterErr *adapters.Error
	if errors.As(err, &adapterErr) {
		switch adapterErr.Kind {
		case adapters.ErrorInvalidURL:
			return unavailable(domain.CategoryInvalidBaseURL)
		case adapters.ErrorStatus:
			if adapterErr.Status == 401 {
				return &Error{Kind: ErrorUpstream, Category: domain.CategoryUpstreamUnauthorized}
			}
			if adapterErr.Status == 403 {
				return &Error{Kind: ErrorUpstream, Category: domain.CategoryAccountBanned}
			}
			if adapterErr.Status == 429 {
				return &Error{Kind: ErrorUpstream, Category: domain.CategoryRateLimited}
			}
			return &Error{Kind: ErrorUpstream, Category: "upstream_status"}
		case adapters.ErrorTransport:
			return &Error{Kind: ErrorUpstream, Category: domain.CategoryUpstreamFailure}
		case adapters.ErrorPayload:
			return &Error{Kind: ErrorUpstream, Category: "invalid_payload"}
		case adapters.ErrorTooLarge:
			return &Error{Kind: ErrorUpstream, Category: "response_too_large"}
		}
	}
	return &Error{Kind: ErrorUpstream, Category: domain.CategoryUpstreamFailure}
}

func platformUserID(raw string) (int64, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, nil
	}
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.UseNumber()
	var metadata struct {
		PlatformUserID *any `json:"platform_user_id"`
	}
	if err := decoder.Decode(&metadata); err != nil {
		return 0, err
	}
	if metadata.PlatformUserID == nil {
		return 0, nil
	}
	id, ok := adapters.CoercePlatformUserID(*metadata.PlatformUserID)
	if !ok {
		return 0, errors.New("platform_user_id must be a positive integer")
	}
	return id, nil
}

func isUserKind(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "access_token", "session":
		return true
	default:
		return false
	}
}

func hasAccountAuthMaterial(credential *domain.Credential) bool {
	return credential != nil && (len(credential.SecretEnc) > 0 || len(credential.CookieEnc) > 0)
}

func accountAuthMode(mode string, hasSecret, hasCookie bool) adapters.AuthMode {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "cookie":
		if hasCookie {
			return adapters.AuthCookie
		}
	case "auto":
		// Keep the auto intent so the adapter can fall back to Cookie on
		// idempotent GET requests when the upstream rejects the token.
		if hasSecret && hasCookie {
			return adapters.AuthAuto
		}
		if hasSecret {
			return adapters.AuthAccessToken
		}
		if hasCookie {
			return adapters.AuthCookie
		}
	case "access_token":
		if hasSecret {
			return adapters.AuthAccessToken
		}
	}
	if hasSecret {
		return adapters.AuthAccessToken
	}
	return adapters.AuthCookie
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func unavailable(category string) error {
	return &Error{Kind: ErrorUnavailable, Category: category}
}

func internalError(category string) error {
	return &Error{Kind: ErrorInternal, Category: category}
}

// errorCategory extracts the stable category string from an account.Error so a
// bulk result can carry a machine-readable reason without leaking internals.
func errorCategory(err error) string {
	var accountErr *Error
	if errors.As(err, &accountErr) {
		return accountErr.Category
	}
	return "upstream_failure"
}

func zero(value []byte) {
	for i := range value {
		value[i] = 0
	}
}

func zeroString(value *string) {
	if value == nil {
		return
	}
	// Overwrite then clear.
	b := []byte(*value)
	zero(b)
	*value = ""
}

func zeroAccountInput(input *adapters.AccountInput) {
	if input == nil {
		return
	}
	zeroString(&input.Secret)
	zeroString(&input.Cookie)
}

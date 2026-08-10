// Package account probes upstream user identity and syncs site API keys for relay.
package account

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
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
	notifier *webhook.Notifier

	financeMu    sync.Mutex
	financeCache *financeCache
}

func New(db *store.DB, enc *crypto.Encrypter, registry *adapters.Registry) *Service {
	return &Service{db: db, enc: enc, registry: registry, now: time.Now}
}

// SetNotifier attaches the operational alert notifier (token expired / low
// balance events).
func (s *Service) SetNotifier(n *webhook.Notifier) {
	s.notifier = n
}

func (s *Service) Probe(ctx context.Context, channelID int64) (*ProbeResult, error) {
	started := s.now()
	resolved, err := s.resolveUserTarget(channelID)
	if err != nil {
		return nil, err
	}
	defer zeroString(&resolved.input.Secret)
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
		_ = s.db.Channel.RecordProbeFailure(channelID, s.now(), probeCategory(probeErr))
		// Verdict-driven state machine: 429 parks the channel until the rate
		// window passes (Retry-After when the upstream provides one); 401/403
		// mark the credential dead. Alerts are per-verdict and throttled.
		var typed *Error
		if errors.As(probeErr, &typed) {
			switch typed.Category {
			case "rate_limited":
				until := s.now().Add(defaultRateLimitPause)
				if retryAfter := retryAfterFrom(err); retryAfter > 0 && retryAfter < maxRateLimitPause {
					until = s.now().Add(retryAfter)
				}
				_ = s.db.Channel.RecordRateLimited(channelID, until)
				if s.notifier != nil {
					s.notifier.SendAlert(ctx, webhook.AlertWarning, "连接触发限流", fmt.Sprintf("连接 #%d (%s) 被上游限流，暂停至 %s。", channelID, resolved.channel.Name, until.Format(time.RFC3339)))
				}
			case "account_banned":
				if s.notifier != nil {
					s.notifier.SendAlert(ctx, webhook.AlertError, "账号疑似封禁", fmt.Sprintf("连接 #%d (%s) 返回 403，账号可能已被上游封禁。", channelID, resolved.channel.Name))
				}
			case "upstream_unauthorized":
				if s.notifier != nil {
					s.notifier.SendAlert(ctx, webhook.AlertError, "访问令牌失效", fmt.Sprintf("连接 #%d (%s) 的访问令牌已失效，请重新生成后更新凭据。", channelID, resolved.channel.Name))
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
	_ = s.db.Channel.RecordProbeSuccess(channelID, checkedAt)
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
		result.Message = "upstream listed tokens but secrets were masked and could not be revealed; paste an sk- manually or use a site that exposes full keys"
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
	defer zeroString(&resolved.input.Secret)
	prices, err := resolved.adapter.Pricing(ctx, resolved.input)
	if err != nil {
		return nil, mapAdapterError(err)
	}
	return prices, nil
}

// FinanceItem is one channel's account finances: balance (in quota), the
// quota-per-unit conversion, and the model price table (quota per 1M tokens).
type FinanceItem struct {
	ChannelID    int64                          `json:"channel_id"`
	Balance      int64                          `json:"balance"`
	QuotaTotal   int64                          `json:"quota_total,omitempty"`
	QuotaUsed    int64                          `json:"quota_used,omitempty"`
	QuotaPerUnit int64                          `json:"quota_per_unit"`
	Prices       map[string]adapters.ModelPrice `json:"prices"`
}

const financeCacheTTL = 2 * time.Minute

// financeCache holds the last FinanceOverview result with its timestamp.
type financeCache struct {
	items []FinanceItem
	at    time.Time
}

// FinanceOverview returns balance + model pricing for every enabled channel
// that has a user credential, cached for a short TTL so the models page does
// not hammer upstreams on every visit. It runs on a detached context so a
// client disconnect does not abort every in-flight upstream probe.
func (s *Service) FinanceOverview(ctx context.Context) ([]FinanceItem, error) {
	s.financeMu.Lock()
	if s.financeCache != nil && time.Since(s.financeCache.at) < financeCacheTTL {
		items := s.financeCache.items
		s.financeMu.Unlock()
		return items, nil
	}
	s.financeMu.Unlock()

	// Detach from the request context: finance is read-only and the cache
	// serves the next visitor; a browser/curl timeout must not cancel the
	// whole sweep mid-flight.
	workCtx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	channels, err := s.db.Channel.ListEnabled()
	if err != nil {
		return nil, internalError("channel_list")
	}
	// Only channels with a usable user credential are worth probing.
	targets := make([]*resolvedTarget, 0, len(channels))
	for index := range channels {
		resolved, resolveErr := s.resolveUserTarget(channels[index].ID)
		if resolveErr != nil {
			continue
		}
		targets = append(targets, resolved)
	}
	if len(targets) == 0 {
		items := []FinanceItem{}
		s.financeMu.Lock()
		s.financeCache = &financeCache{items: items, at: s.now()}
		s.financeMu.Unlock()
		return items, nil
	}

	sem := make(chan struct{}, 4)
	items := make([]FinanceItem, 0, len(targets))
	var itemMu sync.Mutex
	var wg sync.WaitGroup
	for index := range targets {
		resolved := targets[index]
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-workCtx.Done():
				return
			}
			defer func() { <-sem }()
			item := s.financeForChannel(workCtx, resolved)
			if item == nil {
				return
			}
			itemMu.Lock()
			items = append(items, *item)
			itemMu.Unlock()
		}()
	}
	wg.Wait()

	s.financeMu.Lock()
	s.financeCache = &financeCache{items: items, at: s.now()}
	s.financeMu.Unlock()
	return items, nil
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
	now := s.now()
	written := 0
	for _, item := range items {
		name := ""
		if channel, err := s.db.Channel.GetByID(item.ChannelID); err == nil && channel != nil {
			name = channel.Name
		}
		if err := s.db.InsertBalanceHistory(item.ChannelID, name, item.Balance, now); err != nil {
			continue // one bad row must not abort the whole snapshot
		}
		written++
	}
	return written, nil
}

// BalanceHistory returns snapshots from the last N days, newest first.
func (s *Service) BalanceHistory(ctx context.Context, days int) ([]store.BalanceHistoryPoint, error) {
	return s.db.ListBalanceHistory(days)
}

// PruneBalanceHistory removes snapshots older than retentionDays.
func (s *Service) PruneBalanceHistory(ctx context.Context, retentionDays int) (int, error) {
	return s.db.PruneBalanceHistory(retentionDays)
}

func (s *Service) financeForChannel(ctx context.Context, resolved *resolvedTarget) *FinanceItem {
	defer zeroString(&resolved.input.Secret)
	// Bound each upstream call so a slow public site cannot stall the sweep.
	callCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	self, selfErr := resolved.adapter.ProbeSelf(callCtx, resolved.input)
	if selfErr != nil {
		return nil
	}
	prices, priceErr := resolved.adapter.Pricing(callCtx, resolved.input)
	if priceErr != nil {
		prices = nil
	}
	quotaPerUnit, quotaErr := resolved.adapter.QuotaPerUnit(callCtx, resolved.input)
	if quotaErr != nil || quotaPerUnit <= 0 {
		quotaPerUnit = 500000 // New-API default: 1 unit = 500k quota.
	}
	// New-API family semantics: /api/user/self returns quota as the remaining
	// balance (site currency × quota_per_unit) and used_quota as historical
	// cumulative usage. Subtracting used_quota would double-count past
	// consumption and turn a healthy balance negative (42 API case), so the
	// balance is the quota field as-is.
	balance := self.Quota
	if balance == nil {
		zero := int64(0)
		balance = &zero
	}
	// Low-balance alert: remaining quota below one unit (site currency).
	if balance != nil && *balance < quotaPerUnit && s.notifier != nil {
		s.notifier.SendAlert(context.Background(), webhook.AlertWarning, "余额不足", fmt.Sprintf("连接 #%d (%s) 余额仅剩 %d 额度（低于 1 单位），请及时充值。", resolved.channel.ID, resolved.channel.Name, *balance))
	}
	priceMap := make(map[string]adapters.ModelPrice, len(prices))
	for _, p := range prices {
		// Convert every pricing shape to USD following the All API Hub
		// modelPricing.ts normalization:
		//   - direct USD/1M (token_price_usd_per_million) wins when present;
		//   - token: inputUSD = ratio × 1e6 / quota_per_unit × group_ratio;
		//   - per-call: model_price × group_ratio;
		//   - legacy map: quota-per-1M ÷ quota_per_unit.
		mode := p.Mode
		inputUSD := p.PriceUSD
		gr := p.GroupRatio
		if gr <= 0 {
			gr = 1
		}
		switch {
		case p.TokenUSD != nil && p.TokenUSD.Input > 0:
			inputUSD = p.TokenUSD.Input // direct USD, no ratio semantics
			mode = "token"
		case p.Ratio > 0:
			inputUSD = p.Ratio * 1_000_000 / float64(quotaPerUnit) * gr
			mode = "token"
		case p.QuotaPer1M > 0:
			inputUSD = p.QuotaPer1M / float64(quotaPerUnit)
			mode = "token"
		case p.ModelPrice > 0:
			inputUSD = p.ModelPrice * gr
			mode = "fixed"
		}
		if inputUSD <= 0 {
			continue
		}
		outputUSD := inputUSD
		if p.CompletionRatio > 0 && mode == "token" {
			outputUSD = inputUSD * p.CompletionRatio
		}
		priceMap[p.Model] = adapters.ModelPrice{
			Model:     p.Model,
			Currency:  p.Currency,
			PriceUSD:  inputUSD,
			OutputUSD: outputUSD,
			Mode:      mode,
		}
	}
	return &FinanceItem{
		ChannelID:    resolved.channel.ID,
		Balance:      int64Value(balance),
		QuotaTotal:   int64Value(self.Quota),
		QuotaUsed:    int64Value(self.UsedQuota),
		QuotaPerUnit: quotaPerUnit,
		Prices:       priceMap,
	}
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
	defer zeroString(&resolved.input.Secret)

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
	defer zeroString(&resolved.input.Secret)

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
	if decErr != nil || len(plaintext) == 0 {
		zero(plaintext)
		return nil, unavailable("credential_unavailable")
	}
	baseURL := channel.BaseURL
	if baseURL == "" {
		baseURL = site.BaseURL
	}
	return &resolvedTarget{
		channel:    channel,
		site:       site,
		credential: credential,
		adapter:    adapter,
		input: adapters.AccountInput{
			BaseURL:        baseURL,
			Secret:         string(plaintext),
			PlatformUserID: platformUserID,
		},
	}, nil
}

func (s *Service) pickUserCredential(siteID int64, preferredID *int64) (*domain.Credential, error) {
	if preferredID != nil {
		cred, err := s.db.Credential.GetByID(*preferredID)
		if err != nil {
			return nil, internalError("credential_lookup")
		}
		if cred != nil && cred.SiteID == siteID && isUserKind(cred.Kind) &&
			cred.Status == domain.StatusEnabled && len(cred.SecretEnc) > 0 {
			return cred, nil
		}
	}
	list, err := s.db.Credential.ListBySite(siteID)
	if err != nil {
		return nil, internalError("credential_list")
	}
	for index := range list {
		cred := list[index]
		if isUserKind(cred.Kind) && cred.Status == domain.StatusEnabled && len(cred.SecretEnc) > 0 {
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

// probeCategory extracts the redacted failure category used for last_probe_error.
func probeCategory(err error) string {
	var probeErr *Error
	if errors.As(err, &probeErr) && probeErr.Category != "" {
		return probeErr.Category
	}
	return "upstream_failure"
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
			return unavailable("invalid_base_url")
		case adapters.ErrorStatus:
			if adapterErr.Status == 401 {
				return &Error{Kind: ErrorUpstream, Category: "upstream_unauthorized"}
			}
			if adapterErr.Status == 403 {
				return &Error{Kind: ErrorUpstream, Category: "account_banned"}
			}
			if adapterErr.Status == 429 {
				return &Error{Kind: ErrorUpstream, Category: "rate_limited"}
			}
			return &Error{Kind: ErrorUpstream, Category: "upstream_status"}
		case adapters.ErrorTransport:
			return &Error{Kind: ErrorUpstream, Category: "upstream_failure"}
		case adapters.ErrorPayload:
			return &Error{Kind: ErrorUpstream, Category: "invalid_payload"}
		case adapters.ErrorTooLarge:
			return &Error{Kind: ErrorUpstream, Category: "response_too_large"}
		}
	}
	return &Error{Kind: ErrorUpstream, Category: "upstream_failure"}
}

func platformUserID(raw string) (int64, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, nil
	}
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.UseNumber()
	var metadata struct {
		PlatformUserID *json.Number `json:"platform_user_id"`
	}
	if err := decoder.Decode(&metadata); err != nil {
		return 0, err
	}
	if metadata.PlatformUserID == nil {
		return 0, nil
	}
	id, err := metadata.PlatformUserID.Int64()
	if err != nil || id <= 0 {
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

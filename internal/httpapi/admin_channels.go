package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/lan/meta-gateway/internal/adapters"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/proxy"
)

func (h *AdminHandler) listChannels(w http.ResponseWriter, r *http.Request) {
	channels, err := h.db.Channel.List()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, channels)
}

func (h *AdminHandler) listChannelOverviews(w http.ResponseWriter, r *http.Request) {
	channels, err := h.db.Channel.ListOverviews(time.Now())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	// Site-family capability flags (AAH-derived profile table).
	for index := range channels {
		profile := adapters.SiteProfileFor(channels[index].Channel.TypeHint, channels[index].SitePlatform)
		channels[index].CheckinSupported = profile.Checkin
		channels[index].AccountSupported = profile.Family != adapters.FamilyUnsupported
	}
	writeJSON(w, http.StatusOK, channels)
}

func (h *AdminHandler) createChannel(w http.ResponseWriter, r *http.Request) {
	var ch domain.Channel
	if err := decodeJSON(w, r, &ch, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if ch.Status == "" {
		ch.Status = domain.StatusEnabled
	}
	if err := h.validateChannel(&ch); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	id, err := h.db.Channel.Create(&ch)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	h.modelsCache.Invalidate()
	created, _ := h.db.Channel.GetByID(id)
	writeJSON(w, http.StatusCreated, created)
}

func (h *AdminHandler) duplicateChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	source, err := h.db.Channel.GetByID(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if source == nil {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}
	clone := *source
	clone.ID = 0
	clone.Name = source.Name + " (copy)"
	clone.Status = domain.StatusEnabled
	clone.CreatedAt = time.Time{}
	clone.UpdatedAt = time.Time{}
	if err := h.validateChannel(&clone); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	newID, err := h.db.Channel.Create(&clone)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	h.modelsCache.Invalidate()
	created, _ := h.db.Channel.GetByID(newID)
	writeJSON(w, http.StatusCreated, created)
}

func (h *AdminHandler) getChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	ch, err := h.db.Channel.GetByID(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if ch == nil {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}
	writeJSON(w, http.StatusOK, ch)
}

// pingChannel performs a network-layer reachability check (connectivity ping)
// against the channel's base URL. Any HTTP response counts as reachable;
// only connection-level failures (DNS, dial, TLS, timeout) are unreachable.
// The result is persisted for the overview UI (separate from model/auth probe).
func (h *AdminHandler) pingChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	ch, err := h.db.Channel.GetByID(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if ch == nil {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}
	baseURL := strings.TrimSpace(ch.BaseURL)
	if baseURL == "" && ch.SiteID != nil {
		site, siteErr := h.db.Site.GetByID(*ch.SiteID)
		if siteErr == nil && site != nil {
			baseURL = strings.TrimSpace(site.BaseURL)
		}
	}
	if baseURL == "" {
		checkedAt := time.Now()
		_ = h.db.Channel.RecordPingFailure(ch.ID, checkedAt, domain.CategoryInvalidBaseURL)
		_ = h.db.HealthHistory.Append(ch.ID, domain.ProbeKindPing, false, 0, domain.CategoryInvalidBaseURL, checkedAt)
		writeJSON(w, http.StatusOK, map[string]any{"channel_id": ch.ID, "reachable": false, "connectivity_state": domain.ConnectivityStateUnreachable, "error": domain.CategoryInvalidBaseURL, "checked_at": checkedAt})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	started := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL, nil)
	if err != nil {
		checkedAt := time.Now()
		_ = h.db.Channel.RecordPingFailure(ch.ID, checkedAt, "invalid_url")
		_ = h.db.HealthHistory.Append(ch.ID, domain.ProbeKindPing, false, 0, "invalid_url", checkedAt)
		writeJSON(w, http.StatusOK, map[string]any{"channel_id": ch.ID, "reachable": false, "connectivity_state": domain.ConnectivityStateUnreachable, "error": "invalid_url", "checked_at": checkedAt})
		return
	}
	resp, err := h.httpClient.Do(req)
	latencyMs := int(time.Since(started).Milliseconds())
	if err != nil {
		category := classifyPingError(err)
		checkedAt := time.Now()
		_ = h.db.Channel.RecordPingFailure(ch.ID, checkedAt, category)
		_ = h.db.HealthHistory.Append(ch.ID, domain.ProbeKindPing, false, 0, category, checkedAt)
		writeJSON(w, http.StatusOK, map[string]any{"channel_id": ch.ID, "reachable": false, "connectivity_state": domain.ConnectivityStateUnreachable, "error": category, "latency_ms": latencyMs, "checked_at": checkedAt})
		return
	}
	defer resp.Body.Close()
	// Drain a bounded amount so the connection returns to the pool.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	checkedAt := time.Now()
	_ = h.db.Channel.RecordPingSuccess(ch.ID, checkedAt, latencyMs)
	_ = h.db.HealthHistory.Append(ch.ID, domain.ProbeKindPing, true, latencyMs, "", checkedAt)
	writeJSON(w, http.StatusOK, map[string]any{
		"channel_id": ch.ID, "reachable": true, "connectivity_state": domain.ConnectivityStateReachable, "latency_ms": latencyMs, "status_code": resp.StatusCode, "checked_at": checkedAt,
	})
}

// classifyPingError maps a connection error to a stable redacted category.
func classifyPingError(err error) string {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "timeout"
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "dns"
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if opErr.Err != nil && strings.Contains(strings.ToLower(opErr.Err.Error()), "refused") {
			return "connection_refused"
		}
		return "connection_failed"
	}
	return "unreachable"
}

func (h *AdminHandler) updateChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	existing, err := h.db.Channel.GetByID(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if existing == nil {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}
	// The embedded Channel keeps the flat decode; the shadowing pointers make
	// priority/weight true patch fields — omitting them preserves the stored
	// values instead of zeroing them.
	var patch struct {
		domain.Channel
		Priority *int `json:"priority"`
		Weight   *int `json:"weight"`
	}
	if err := decodeJSON(w, r, &patch, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	// Merge onto the stored row so clients that omit site_id (or send 0) cannot
	// break credential ownership validation when rebinding API keys.
	ch := *existing
	if name := strings.TrimSpace(patch.Name); name != "" {
		ch.Name = name
	}
	// BaseURL may intentionally be cleared to inherit site URL.
	if patch.BaseURL != existing.BaseURL {
		ch.BaseURL = strings.TrimSpace(patch.BaseURL)
	}
	if group := strings.TrimSpace(patch.GroupName); group != "" {
		ch.GroupName = group
	}
	if patch.Priority != nil {
		ch.Priority = *patch.Priority
	}
	if patch.Weight != nil {
		if *patch.Weight < 0 {
			writeError(w, http.StatusBadRequest, "weight must be >= 0")
			return
		}
		ch.Weight = *patch.Weight
	}
	if patch.Status == domain.StatusEnabled || patch.Status == domain.StatusDisabled {
		ch.Status = patch.Status
	}
	if hint := strings.TrimSpace(patch.TypeHint); hint != "" {
		ch.TypeHint = hint
	}
	// Max reasoning effort: empty string clears it (passthrough).
	if patch.MaxReasoningEffort != existing.MaxReasoningEffort {
		ch.MaxReasoningEffort = strings.ToLower(strings.TrimSpace(patch.MaxReasoningEffort))
	}
	// Payload rules: JSON array of rewrite rules; empty string clears them.
	if patch.PayloadRules != existing.PayloadRules {
		if trimmed := strings.TrimSpace(patch.PayloadRules); trimmed != "" && trimmed != "[]" {
			var probe []proxy.PayloadRule
			if err := json.Unmarshal([]byte(trimmed), &probe); err != nil {
				writeError(w, http.StatusBadRequest, "payload_rules must be a valid JSON array")
				return
			}
			ch.PayloadRules = trimmed
		} else {
			ch.PayloadRules = ""
		}
	}
	// Hard concurrency ceiling: non-negative; 0 = unlimited.
	if patch.MaxConcurrent < 0 {
		writeError(w, http.StatusBadRequest, "max_concurrent must be >= 0")
		return
	}
	ch.MaxConcurrent = patch.MaxConcurrent
	// Per-channel proxy: http/https URL validated against the outbound
	// policy (SSRF); empty clears it (inherits the global proxy).
	if patch.ProxyURL != existing.ProxyURL {
		if trimmed := strings.TrimSpace(patch.ProxyURL); trimmed != "" {
			if h.validateProxyURL != nil {
				if err := h.validateProxyURL(trimmed); err != nil {
					writeError(w, http.StatusBadRequest, "proxy_url: "+err.Error())
					return
				}
			}
			ch.ProxyURL = trimmed
		} else {
			ch.ProxyURL = ""
		}
	}
	if patch.ModelsCSV != "" {
		ch.ModelsCSV = patch.ModelsCSV
	}
	// Header overrides and system prompt: empty string clears the stored value.
	if patch.HeaderOverride != existing.HeaderOverride {
		ch.HeaderOverride = strings.TrimSpace(patch.HeaderOverride)
	}
	if patch.SystemPrompt != existing.SystemPrompt {
		ch.SystemPrompt = strings.TrimSpace(patch.SystemPrompt)
	}
	// Retry config: empty string clears it (global defaults only).
	if patch.RetryConfig != existing.RetryConfig {
		ch.RetryConfig = strings.TrimSpace(patch.RetryConfig)
	}
	// Tags: empty string clears the tag list.
	if patch.Tags != existing.Tags {
		ch.Tags = strings.TrimSpace(patch.Tags)
	}
	// StableFirst: grayscale flag follows the patch when explicitly toggled.
	if patch.StableFirst != existing.StableFirst {
		ch.StableFirst = patch.StableFirst
	}
	// Only accept a new site_id when it is a positive id; never wipe ownership.
	if patch.SiteID != nil && *patch.SiteID > 0 {
		ch.SiteID = patch.SiteID
	}
	if patch.CredentialID != nil && *patch.CredentialID > 0 {
		ch.CredentialID = patch.CredentialID
	}
	ch.ID = id
	if err := h.validateChannel(&ch); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Manual recovery of an auto-disabled channel must also clear the route
	// members' failure/cooldown state. Otherwise the channel badge turns green
	// while its model members remain parked from the same failure burst.
	recoverAutoHealth := existing.Status == domain.StatusAutoDisabled && ch.Status == domain.StatusEnabled
	if recoverAutoHealth {
		if _, err := h.db.Channel.RecoverAutoDisabled(id); err != nil {
			writeStoreError(w, err)
			return
		}
	}
	if err := h.db.Channel.Update(&ch); err != nil {
		writeStoreError(w, err)
		return
	}
	h.modelsCache.Invalidate()
	updated, _ := h.db.Channel.GetByID(id)
	writeJSON(w, http.StatusOK, updated)
}

func (h *AdminHandler) deleteChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := h.db.Channel.Delete(id); err != nil {
		writeStoreError(w, err)
		return
	}
	h.modelsCache.Invalidate()
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *AdminHandler) validateChannel(ch *domain.Channel) error {
	ch.Name = strings.TrimSpace(ch.Name)
	ch.BaseURL = strings.TrimSpace(ch.BaseURL)
	ch.GroupName = strings.TrimSpace(ch.GroupName)
	ch.TypeHint = strings.TrimSpace(ch.TypeHint)
	if ch.Name == "" {
		return errors.New("name is required")
	}
	if ch.Weight < 0 {
		return errors.New("weight must be non-negative")
	}
	if ch.Status != domain.StatusEnabled && ch.Status != domain.StatusDisabled {
		return errors.New("invalid channel status")
	}
	if ch.SiteID == nil || *ch.SiteID <= 0 {
		return errors.New("site_id is required")
	}
	site, err := h.db.Site.GetByID(*ch.SiteID)
	if err != nil || site == nil {
		return errors.New("site not found")
	}
	if ch.CredentialID == nil || *ch.CredentialID <= 0 {
		return errors.New("credential_id is required")
	}
	credential, err := h.db.Credential.GetByID(*ch.CredentialID)
	if err != nil || credential == nil || credential.SiteID != *ch.SiteID {
		return errors.New("credential does not belong to site")
	}
	return nil
}

// ---------------------------------------------------------------------------
// Routes
// ---------------------------------------------------------------------------

// patchChannelsByTag applies a partial update to every channel carrying the
// tag (bulk priority/weight/status/mapping operations).
func (h *AdminHandler) patchChannelsByTag(w http.ResponseWriter, r *http.Request) {
	tag := strings.TrimSpace(chi.URLParam(r, "tag"))
	if tag == "" {
		writeError(w, http.StatusBadRequest, "tag is required")
		return
	}
	var patch domain.ChannelPatch
	if err := decodeJSON(w, r, &patch, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	// Validate enum-ish fields early so a typo does not silently no-op.
	if patch.Status != nil {
		switch *patch.Status {
		case domain.StatusEnabled, domain.StatusDisabled, domain.StatusAutoDisabled:
		default:
			writeError(w, http.StatusBadRequest, "invalid status")
			return
		}
	}
	if patch.Priority != nil && (*patch.Priority < 0 || *patch.Priority > 1000) {
		writeError(w, http.StatusBadRequest, "priority out of range")
		return
	}
	if patch.Weight != nil && (*patch.Weight < 0 || *patch.Weight > 10000) {
		writeError(w, http.StatusBadRequest, "weight out of range")
		return
	}
	affected, err := h.db.Channel.UpdateByTag(tag, patch)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tag": tag, "affected": affected})
}

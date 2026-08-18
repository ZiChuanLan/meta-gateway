package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

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
	if strings.TrimSpace(ch.GroupName) == "" {
		ch.GroupName = "default"
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
	created, err := h.db.Channel.GetByID(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if created == nil {
		writeError(w, http.StatusInternalServerError, "channel vanished after create")
		return
	}
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
	created, err := h.db.Channel.GetByID(newID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if created == nil {
		writeError(w, http.StatusInternalServerError, "channel vanished after duplicate")
		return
	}
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
	// Every field is a pointer so omission preserves the stored value while an
	// explicit zero/empty value can still clear fields that support it.
	var patch struct {
		SiteID             *int64  `json:"site_id"`
		CredentialID       *int64  `json:"credential_id"`
		Name               *string `json:"name"`
		BaseURL            *string `json:"base_url"`
		ModelsCSV          *string `json:"models_csv"`
		GroupName          *string `json:"group_name"`
		Priority           *int    `json:"priority"`
		Weight             *int    `json:"weight"`
		Status             *string `json:"status"`
		TypeHint           *string `json:"type_hint"`
		MaxReasoningEffort *string `json:"max_reasoning_effort"`
		PayloadRules       *string `json:"payload_rules"`
		MaxConcurrent      *int    `json:"max_concurrent"`
		ProxyURL           *string `json:"proxy_url"`
		HeaderOverride     *string `json:"header_override"`
		SystemPrompt       *string `json:"system_prompt"`
		RetryConfig        *string `json:"retry_config"`
		StableFirst        *bool   `json:"stable_first"`
	}
	if err := decodeJSON(w, r, &patch, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	// Merge onto the stored row so clients that omit site_id (or send 0) cannot
	// break credential ownership validation when rebinding API keys.
	ch := *existing
	if patch.Name != nil {
		name := strings.TrimSpace(*patch.Name)
		if name == "" {
			writeError(w, http.StatusBadRequest, "name must not be empty")
			return
		}
		ch.Name = name
	}
	// BaseURL and the other string fields may intentionally be cleared to
	// inherit defaults, so nil means "preserve" while an explicit empty value
	// means "clear".
	if patch.BaseURL != nil {
		ch.BaseURL = strings.TrimSpace(*patch.BaseURL)
	}
	if patch.GroupName != nil {
		ch.GroupName = strings.TrimSpace(*patch.GroupName)
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
	if patch.Status != nil {
		if *patch.Status != domain.StatusEnabled && *patch.Status != domain.StatusDisabled {
			writeError(w, http.StatusBadRequest, "status must be enabled or disabled")
			return
		}
		ch.Status = *patch.Status
	}
	if patch.TypeHint != nil {
		ch.TypeHint = strings.TrimSpace(*patch.TypeHint)
	}
	// Max reasoning effort: empty string clears it (passthrough).
	if patch.MaxReasoningEffort != nil {
		ch.MaxReasoningEffort = strings.ToLower(strings.TrimSpace(*patch.MaxReasoningEffort))
	}
	// Payload rules: JSON array of rewrite rules; empty string clears them.
	if patch.PayloadRules != nil {
		if trimmed := strings.TrimSpace(*patch.PayloadRules); trimmed != "" && trimmed != "[]" {
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
	// Hard concurrency ceiling: non-negative; 0 = unlimited. A missing field
	// preserves the existing ceiling; an explicit zero clears it.
	if patch.MaxConcurrent != nil {
		if *patch.MaxConcurrent < 0 {
			writeError(w, http.StatusBadRequest, "max_concurrent must be >= 0")
			return
		}
		ch.MaxConcurrent = *patch.MaxConcurrent
	}
	// Per-channel proxy: http/https URL validated against the outbound
	// policy (SSRF); empty clears it (inherits the global proxy).
	if patch.ProxyURL != nil {
		if trimmed := strings.TrimSpace(*patch.ProxyURL); trimmed != "" {
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
	if patch.ModelsCSV != nil {
		ch.ModelsCSV = strings.TrimSpace(*patch.ModelsCSV)
	}
	// Header overrides and system prompt: empty string clears the stored value.
	if patch.HeaderOverride != nil {
		ch.HeaderOverride = strings.TrimSpace(*patch.HeaderOverride)
	}
	if patch.SystemPrompt != nil {
		ch.SystemPrompt = strings.TrimSpace(*patch.SystemPrompt)
	}
	// Retry config: empty string clears it (global defaults only).
	if patch.RetryConfig != nil {
		ch.RetryConfig = strings.TrimSpace(*patch.RetryConfig)
	}
	// StableFirst: a missing field preserves the current grayscale state.
	if patch.StableFirst != nil {
		ch.StableFirst = *patch.StableFirst
	}
	// Only accept a new site_id when it is a positive id; never wipe ownership.
	if patch.SiteID != nil && *patch.SiteID > 0 {
		v := *patch.SiteID
		ch.SiteID = &v
	}
	if patch.CredentialID != nil && *patch.CredentialID > 0 {
		v := *patch.CredentialID
		ch.CredentialID = &v
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
	updated, err := h.db.Channel.GetByID(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if updated == nil {
		writeError(w, http.StatusInternalServerError, "channel vanished after update")
		return
	}
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
	ch.ModelsCSV = strings.TrimSpace(ch.ModelsCSV)
	ch.GroupName = strings.TrimSpace(ch.GroupName)
	ch.TypeHint = strings.TrimSpace(ch.TypeHint)
	ch.MaxReasoningEffort = strings.ToLower(strings.TrimSpace(ch.MaxReasoningEffort))
	ch.PayloadRules = strings.TrimSpace(ch.PayloadRules)
	ch.ProxyURL = strings.TrimSpace(ch.ProxyURL)
	ch.HeaderOverride = strings.TrimSpace(ch.HeaderOverride)
	ch.SystemPrompt = strings.TrimSpace(ch.SystemPrompt)
	ch.RetryConfig = strings.TrimSpace(ch.RetryConfig)
	if ch.Name == "" {
		return errors.New("name is required")
	}
	if ch.Weight < 0 {
		return errors.New("weight must be non-negative")
	}
	if ch.MaxConcurrent < 0 {
		return errors.New("max_concurrent must be non-negative")
	}
	if ch.Status != domain.StatusEnabled && ch.Status != domain.StatusDisabled {
		return errors.New("invalid channel status")
	}
	if ch.PayloadRules == "[]" {
		ch.PayloadRules = ""
	} else if ch.PayloadRules != "" {
		var rules []proxy.PayloadRule
		if err := json.Unmarshal([]byte(ch.PayloadRules), &rules); err != nil || rules == nil {
			return errors.New("payload_rules must be a valid JSON array")
		}
	}
	if ch.ProxyURL != "" && h.validateProxyURL != nil {
		if err := h.validateProxyURL(ch.ProxyURL); err != nil {
			return fmt.Errorf("proxy_url: %w", err)
		}
	}
	if ch.HeaderOverride != "" {
		if err := proxy.ValidateHeaderOverrides(ch.HeaderOverride); err != nil {
			return fmt.Errorf("header_override: %w", err)
		}
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

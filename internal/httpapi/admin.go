package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/lan/meta-gateway/internal/adapters"
	"github.com/lan/meta-gateway/internal/alerts"
	"github.com/lan/meta-gateway/internal/auth"
	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/maintenance"
	"github.com/lan/meta-gateway/internal/proxy"
	"github.com/lan/meta-gateway/internal/routing"
	"github.com/lan/meta-gateway/internal/sitedetect"
	"github.com/lan/meta-gateway/internal/store"
)

// AdminHandler serves management endpoints under /admin.
type AdminHandler struct {
	db     *store.DB
	enc    *crypto.Encrypter
	router *routing.Selector
	sticky *routing.StickyStore
	// httpClient is used for outbound site-detection probes; it is the shared
	// SSRF-policy client so admin input can never reach private/loopback space.
	httpClient *http.Client
	// modelsCache is invalidated on route/channel writes so /v1/models never
	// serves stale ids after admin changes.
	modelsCache *modelsCache
	// gcService drives scheduled + manual database maintenance (may be nil).
	gcService *maintenance.GCService
	// validateProxyURL validates a per-channel proxy URL against the outbound
	// SSRF policy (nil skips validation — admin handlers without a policy).
	validateProxyURL func(raw string) error
}

func NewAdminHandler(db *store.DB, enc *crypto.Encrypter, selector *routing.Selector, sticky *routing.StickyStore, outboundClient *http.Client, modelsCache *modelsCache) *AdminHandler {
	return &AdminHandler{
		db:          db,
		enc:         enc,
		router:      selector,
		sticky:      sticky,
		httpClient:  outboundClient,
		modelsCache: modelsCache,
	}
}

// SetProxyValidator wires the outbound-policy proxy URL validator (nil
// disables per-channel proxy validation).
func (h *AdminHandler) SetProxyValidator(fn func(raw string) error) {
	h.validateProxyURL = fn
}

// SetGCService wires the database-maintenance service (may be nil).
func (h *AdminHandler) SetGCService(s *maintenance.GCService) {
	h.gcService = s
}

// SetSticky hot-swaps the sticky-session store backing the admin read-only
// stats endpoint (nil = disabled). Used by the runtime-settings hot reload.
func (h *AdminHandler) SetSticky(store *routing.StickyStore) {
	h.sticky = store
}

func (h *AdminHandler) Register(r chi.Router) {
	// Sites
	r.Get("/sites", h.listSites)
	r.Post("/sites", h.createSite)
	r.Get("/sites/{id}", h.getSite)
	r.Put("/sites/{id}", h.updateSite)
	r.Delete("/sites/{id}", h.deleteSite)
	// Site-type detection (AAH chain) for the connection editor.
	r.Get("/site-type", h.detectSiteType)
	// One-shot connection creation: site + credential + channel, with site
	// reuse by normalized URL and rollback of partially created rows.
	r.Post("/connections", h.createConnection)

	// Credentials
	r.Get("/sites/{siteId}/credentials", h.listCredentials)
	r.Post("/sites/{siteId}/credentials", h.createCredential)
	r.Put("/credentials/{id}", h.updateCredential)
	r.Delete("/credentials/{id}", h.deleteCredential)
	// POST so the admin audit trail records plaintext secret reveals.
	r.Post("/sites/{siteId}/credentials/{id}/reveal", h.revealCredentialSecret)

	// Channels
	r.Get("/channels", h.listChannels)
	r.Get("/search", h.globalSearch)
	r.Get("/channels/overview", h.listChannelOverviews)
	r.Post("/channels", h.createChannel)
	r.Post("/channels/{id}/duplicate", h.duplicateChannel)
	r.Post("/reset", h.factoryReset)
	r.Get("/channels/{id}", h.getChannel)
	r.Put("/channels/{id}", h.updateChannel)
	r.Delete("/channels/{id}", h.deleteChannel)
	r.Post("/channels/{id}/ping", h.pingChannel)

	// Routes
	r.Get("/routes", h.listRoutes)
	r.Get("/routes/overview", h.listRouteOverviews)
	r.Get("/routes/explain", h.explainRoute)
	r.Post("/routes", h.createRoute)
	r.Get("/routes/{id}", h.getRoute)
	r.Put("/routes/{id}", h.updateRoute)
	r.Delete("/routes/{id}", h.deleteRoute)

	// Route members
	r.Get("/routes/{routeId}/members", h.listRouteMembers)
	r.Post("/routes/{routeId}/members", h.createRouteMember)
	r.Put("/route-members/{id}", h.updateRouteMember)
	r.Post("/route-members/{id}/clear-health", h.clearRouteMemberHealth)
	r.Delete("/route-members/{id}", h.deleteRouteMember)

	// Downstream keys
	r.Get("/downstream-keys", h.listDownstreamKeys)
	r.Post("/downstream-keys", h.createDownstreamKey)
	r.Put("/downstream-keys/{id}", h.updateDownstreamKey)
	r.Delete("/downstream-keys/{id}", h.deleteDownstreamKey)
	// POST verbs so the admin audit trail records plaintext reveals and token
	// rotations (GET requests are not audited).
	r.Post("/downstream-keys/{id}/reveal", h.revealDownstreamKeyToken)
	r.Post("/downstream-keys/{id}/rotate", h.rotateDownstreamKeyToken)

	// Usage / simple billing
	r.Get("/usage/summary", h.usageSummary)
	r.Get("/usage", h.listUsage)
	// Billing ratios (model markup; 1.0 = no markup)
	r.Get("/ratios", h.listModelRatios)
	r.Put("/ratios/{model}", h.setModelRatio)
	// Tenant groups (multi-tenant quotas / rate limits)
	r.Get("/groups", h.listGroups)
	r.Put("/groups/{name}", h.upsertGroup)
	r.Delete("/groups/{name}", h.deleteGroup)
	// Bulk channel operations by tag
	r.Patch("/channels/tag/{tag}", h.patchChannelsByTag)

	// Sticky session routing (available when enabled at boot)
	r.Get("/sticky", h.stickyStats)

	// Proxy logs
	r.Get("/proxy-logs", h.listProxyLogs)
	r.Get("/proxy-logs/latency-histogram", h.latencyHistogram)
	// Routing decision audit trail.
	r.Get("/decision-snapshot", h.decisionSnapshot)
	// Model-not-found blacklist.
	r.Get("/model-blocks", h.listModelBlocks)
	r.Delete("/model-blocks", h.deleteModelBlock)
	// Redemption codes (quota top-up vouchers for downstream keys).
	r.Post("/redemption-codes", h.createRedemptionCodes)
	r.Get("/redemption-codes", h.listRedemptionCodes)
	r.Delete("/redemption-codes/{id}", h.deleteRedemptionCode)
	// Model metadata library (per-model capability annotations).
	r.Get("/model-metadata", h.listModelMetadata)
	r.Put("/model-metadata/{name}", h.upsertModelMetadata)
	r.Delete("/model-metadata/{name}", h.deleteModelMetadata)
	// Channel health history + availability summaries.
	r.Get("/health-history", h.listHealthHistory)
	r.Get("/health-history/summary", h.healthHistorySummary)
	// Alert rules (metric → webhook).
	r.Get("/alert-rules", h.listAlertRules)
	r.Post("/alert-rules", h.createAlertRule)
	r.Put("/alert-rules/{id}", h.updateAlertRule)
	r.Delete("/alert-rules/{id}", h.deleteAlertRule)
	// Sensitive prompt guard rules (regex → mask/reject/exclude).
	r.Get("/prompt-guards", h.listPromptGuards)
	r.Post("/prompt-guards", h.createPromptGuard)
	r.Put("/prompt-guards/{id}", h.updatePromptGuard)
	r.Delete("/prompt-guards/{id}", h.deletePromptGuard)
	// Database maintenance (orphan GC + VACUUM).
	r.Post("/db/gc", h.runDBGC)
	r.Get("/db/gc", h.lastDBGC)
	// Error passthrough rules (status/keyword → passthrough/rewrite/ignore).
	r.Get("/error-rules", h.listErrorRules)
	r.Post("/error-rules", h.createErrorRule)
	r.Put("/error-rules/{id}", h.updateErrorRule)
	r.Delete("/error-rules/{id}", h.deleteErrorRule)
}

// listErrorRules returns all error passthrough rules.
func (h *AdminHandler) listErrorRules(w http.ResponseWriter, r *http.Request) {
	items, err := h.db.ErrorRule.List()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if items == nil {
		items = []store.ErrorPassRule{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// listHealthHistory returns recent probe points; ?channel_id=&hours= filter.
func (h *AdminHandler) listHealthHistory(w http.ResponseWriter, r *http.Request) {
	channelID := int64(0)
	if parsed, err := strconv.ParseInt(r.URL.Query().Get("channel_id"), 10, 64); err == nil && parsed > 0 {
		channelID = parsed
	}
	points, err := h.db.HealthHistory.Recent(channelID, 200)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if points == nil {
		points = []store.HealthPoint{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": points})
}

// healthHistorySummary returns per-channel availability over ?hours= (default 24).
func (h *AdminHandler) healthHistorySummary(w http.ResponseWriter, r *http.Request) {
	hours := 24
	if parsed, err := strconv.Atoi(r.URL.Query().Get("hours")); err == nil && parsed > 0 && parsed <= 24*90 {
		hours = parsed
	}
	summaries, err := h.db.HealthHistory.Summaries(time.Now().Add(-time.Duration(hours) * time.Hour))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if summaries == nil {
		summaries = []store.ChannelHealthSummary{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": summaries})
}

// listAlertRules returns all rules plus the metric catalog for the UI.
func (h *AdminHandler) listAlertRules(w http.ResponseWriter, r *http.Request) {
	items, err := h.db.AlertRule.List()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if items == nil {
		items = []store.AlertRule{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":   items,
		"metrics": alerts.MetricDescriptions,
	})
}

func (h *AdminHandler) decodeAlertRule(w http.ResponseWriter, r *http.Request) (*store.AlertRule, bool) {
	var rule store.AlertRule
	if err := decodeJSON(w, r, &rule, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return nil, false
	}
	if err := alerts.ValidateRule(&rule); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return nil, false
	}
	return &rule, true
}

func (h *AdminHandler) createAlertRule(w http.ResponseWriter, r *http.Request) {
	rule, ok := h.decodeAlertRule(w, r)
	if !ok {
		return
	}
	if err := h.db.AlertRule.Upsert(rule); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rule)
}

func (h *AdminHandler) updateAlertRule(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	rule, ok := h.decodeAlertRule(w, r)
	if !ok {
		return
	}
	rule.ID = id
	if err := h.db.AlertRule.Upsert(rule); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rule)
}

func (h *AdminHandler) deleteAlertRule(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.db.AlertRule.Delete(id); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// listPromptGuards returns all guard rules.
func (h *AdminHandler) listPromptGuards(w http.ResponseWriter, r *http.Request) {
	items, err := h.db.PromptGuard.List()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if items == nil {
		items = []store.PromptGuardRule{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *AdminHandler) decodePromptGuard(w http.ResponseWriter, r *http.Request) (*store.PromptGuardRule, bool) {
	var rule store.PromptGuardRule
	if err := decodeJSON(w, r, &rule, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return nil, false
	}
	rule.Name = strings.TrimSpace(rule.Name)
	rule.Pattern = strings.TrimSpace(rule.Pattern)
	if rule.Name == "" || rule.Pattern == "" {
		writeError(w, http.StatusBadRequest, "name and pattern are required")
		return nil, false
	}
	if _, err := regexp.Compile(rule.Pattern); err != nil {
		writeError(w, http.StatusBadRequest, "invalid regex: "+err.Error())
		return nil, false
	}
	switch rule.Action {
	case "mask", "reject", "exclude":
	default:
		writeError(w, http.StatusBadRequest, "action must be mask, reject or exclude")
		return nil, false
	}
	if rule.Action == "exclude" && strings.TrimSpace(rule.ExcludeChannels) == "" {
		writeError(w, http.StatusBadRequest, "exclude_channels required for exclude action")
		return nil, false
	}
	return &rule, true
}

func (h *AdminHandler) createPromptGuard(w http.ResponseWriter, r *http.Request) {
	rule, ok := h.decodePromptGuard(w, r)
	if !ok {
		return
	}
	if err := h.db.PromptGuard.Upsert(rule); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rule)
}

func (h *AdminHandler) updatePromptGuard(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	rule, ok := h.decodePromptGuard(w, r)
	if !ok {
		return
	}
	rule.ID = id
	if err := h.db.PromptGuard.Upsert(rule); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rule)
}

func (h *AdminHandler) deletePromptGuard(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.db.PromptGuard.Delete(id); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// runDBGC executes a maintenance pass synchronously and returns the counts.
func (h *AdminHandler) runDBGC(w http.ResponseWriter, r *http.Request) {
	if h.gcService == nil {
		writeError(w, http.StatusServiceUnavailable, "maintenance not available")
		return
	}
	res, err := h.gcService.RunOnce()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// lastDBGC returns the most recent pass result (null when none ran yet).
func (h *AdminHandler) lastDBGC(w http.ResponseWriter, r *http.Request) {
	if h.gcService == nil {
		writeJSON(w, http.StatusOK, map[string]any{"result": nil})
		return
	}
	res, at := h.gcService.Last()
	writeJSON(w, http.StatusOK, map[string]any{"result": res, "ran_at": at})
}

// validateErrorRule normalizes and checks one rule payload; returns the
// normalized rule or a 400 error response.
func (h *AdminHandler) validateErrorRule(w http.ResponseWriter, r *store.ErrorPassRule) bool {
	r.Name = strings.TrimSpace(r.Name)
	if r.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return false
	}
	if r.StatusCode < 0 || r.StatusCode > 599 {
		writeError(w, http.StatusBadRequest, "status_code must be 0-599")
		return false
	}
	switch r.Action {
	case store.ErrorRulePassthrough, store.ErrorRuleRewrite, store.ErrorRuleIgnoreMonitor:
	default:
		writeError(w, http.StatusBadRequest, "action must be passthrough, rewrite or ignore_monitor")
		return false
	}
	if r.Action == store.ErrorRuleRewrite && (r.RewriteTo < 100 || r.RewriteTo > 599) {
		writeError(w, http.StatusBadRequest, "rewrite_to must be 100-599")
		return false
	}
	if r.ChannelID < 0 {
		writeError(w, http.StatusBadRequest, "channel_id must be >= 0")
		return false
	}
	return true
}

// createErrorRule inserts a new rule.
func (h *AdminHandler) createErrorRule(w http.ResponseWriter, r *http.Request) {
	var rule store.ErrorPassRule
	if err := decodeJSON(w, r, &rule, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if !h.validateErrorRule(w, &rule) {
		return
	}
	id, err := h.db.ErrorRule.Create(&rule)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	rule.ID = id
	writeJSON(w, http.StatusOK, rule)
}

// updateErrorRule replaces one rule (hot reload: next request reads it live).
func (h *AdminHandler) updateErrorRule(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var rule store.ErrorPassRule
	if err := decodeJSON(w, r, &rule, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if !h.validateErrorRule(w, &rule) {
		return
	}
	rule.ID = id
	if err := h.db.ErrorRule.Update(&rule); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rule)
}

// deleteErrorRule removes one rule.
func (h *AdminHandler) deleteErrorRule(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.db.ErrorRule.Delete(id); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// listModelMetadata returns the full model metadata library.
func (h *AdminHandler) listModelMetadata(w http.ResponseWriter, r *http.Request) {
	items, err := h.db.ModelMetadata.List()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if items == nil {
		items = []domain.ModelMetadata{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// upsertModelMetadata creates or updates one model's capability annotation.
func (h *AdminHandler) upsertModelMetadata(w http.ResponseWriter, r *http.Request) {
	name, err := url.PathUnescape(chi.URLParam(r, "name"))
	if err != nil || strings.TrimSpace(name) == "" {
		writeError(w, http.StatusBadRequest, "model name required")
		return
	}
	name = strings.TrimSpace(name)
	var req struct {
		ContextWindow    *int64  `json:"context_window"`
		InputModalities  *string `json:"input_modalities"`
		OutputModalities *string `json:"output_modalities"`
		SupportsThinking *int    `json:"supports_thinking"`
		Vendor           *string `json:"vendor"`
		Notes            *string `json:"notes"`
	}
	if err := decodeJSON(w, r, &req, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	existing, err := h.db.ModelMetadata.Get(name)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	meta := domain.ModelMetadata{ModelName: name}
	if existing != nil {
		meta = *existing
	}
	if req.ContextWindow != nil {
		if *req.ContextWindow < 0 {
			writeError(w, http.StatusBadRequest, "context_window must be >= 0")
			return
		}
		meta.ContextWindow = *req.ContextWindow
	}
	if req.InputModalities != nil {
		meta.InputModalities = *req.InputModalities
	}
	if req.OutputModalities != nil {
		meta.OutputModalities = *req.OutputModalities
	}
	if req.SupportsThinking != nil {
		if *req.SupportsThinking < -1 || *req.SupportsThinking > 1 {
			writeError(w, http.StatusBadRequest, "supports_thinking must be -1, 0 or 1")
			return
		}
		meta.SupportsThinking = *req.SupportsThinking
	}
	if req.Vendor != nil {
		meta.Vendor = *req.Vendor
	}
	if req.Notes != nil {
		meta.Notes = *req.Notes
	}
	if err := h.db.ModelMetadata.Upsert(&meta); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, meta)
}

// deleteModelMetadata removes one model's annotation (missing is not an error).
func (h *AdminHandler) deleteModelMetadata(w http.ResponseWriter, r *http.Request) {
	name, err := url.PathUnescape(chi.URLParam(r, "name"))
	if err != nil || strings.TrimSpace(name) == "" {
		writeError(w, http.StatusBadRequest, "model name required")
		return
	}
	name = strings.TrimSpace(name)
	if err := h.db.ModelMetadata.Delete(name); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// createRedemptionCodes mints one-time quota vouchers.
func (h *AdminHandler) createRedemptionCodes(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Count       int    `json:"count"`
		QuotaTokens int64  `json:"quota_tokens"`
		ExpiresAt   string `json:"expires_at"`
	}
	if err := decodeJSON(w, r, &req, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if req.Count <= 0 || req.Count > 100 {
		writeError(w, http.StatusBadRequest, "count must be 1-100")
		return
	}
	if req.QuotaTokens <= 0 {
		writeError(w, http.StatusBadRequest, "quota_tokens must be positive")
		return
	}
	var expiresAt *time.Time
	if req.ExpiresAt != "" {
		t, err := time.Parse(time.RFC3339, req.ExpiresAt)
		if err != nil {
			writeError(w, http.StatusBadRequest, "expires_at must be RFC3339")
			return
		}
		expiresAt = &t
	}
	codes, err := h.db.CreateRedemptionCodes(req.Count, req.QuotaTokens, 0, expiresAt)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": codes})
}

// listRedemptionCodes lists vouchers; ?unredeemed=1 filters used ones out.
func (h *AdminHandler) listRedemptionCodes(w http.ResponseWriter, r *http.Request) {
	onlyUnredeemed := r.URL.Query().Get("unredeemed") == "1"
	codes, err := h.db.ListRedemptionCodes(onlyUnredeemed)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if codes == nil {
		codes = []store.RedemptionCode{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": codes})
}

// deleteRedemptionCode voids an unredeemed voucher.
func (h *AdminHandler) deleteRedemptionCode(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.db.DeleteRedemptionCode(id); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// listModelBlocks returns all channel × model not-found blacklist entries.
func (h *AdminHandler) listModelBlocks(w http.ResponseWriter, r *http.Request) {
	blocks, err := h.db.ListModelBlocks()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "model blocks")
		return
	}
	if blocks == nil {
		blocks = []store.ModelBlock{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": blocks})
}

// deleteModelBlock clears one blacklist entry (?channel_id=&model=).
func (h *AdminHandler) deleteModelBlock(w http.ResponseWriter, r *http.Request) {
	channelID := int64(0)
	if parsed, err := strconv.ParseInt(r.URL.Query().Get("channel_id"), 10, 64); err == nil {
		channelID = parsed
	}
	model := strings.TrimSpace(r.URL.Query().Get("model"))
	if channelID <= 0 || model == "" {
		writeError(w, http.StatusBadRequest, "channel_id and model are required")
		return
	}
	if err := h.db.UnblockModel(channelID, model); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// decisionSnapshot serves the routing audit trail for one request id.
func (h *AdminHandler) decisionSnapshot(w http.ResponseWriter, r *http.Request) {
	requestID := strings.TrimSpace(r.URL.Query().Get("request_id"))
	if requestID == "" {
		writeError(w, http.StatusBadRequest, "request_id is required")
		return
	}
	snap, err := h.db.LatestDecisionSnapshot(requestID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "decision snapshot")
		return
	}
	if snap == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no snapshot"})
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (h *AdminHandler) explainRoute(w http.ResponseWriter, r *http.Request) {
	model := r.URL.Query().Get("model")
	if model == "" {
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}
	explanation, err := h.router.ExplainWithSession(r.Context(), model, r.URL.Query().Get("session"))
	if err != nil {
		if errors.Is(err, routing.ErrRouteNotFound) {
			writeError(w, http.StatusNotFound, "route not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to explain route")
		return
	}
	writeJSON(w, http.StatusOK, explanation)
}

// stickyStats returns a stable response for the admin UI. Sticky routing is
// optional, so a disabled instance is represented as an ordinary successful
// response instead of a noisy 404 from the model page's status query.
func (h *AdminHandler) stickyStats(w http.ResponseWriter, _ *http.Request) {
	if h.sticky == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"enabled":     false,
			"stats":       routing.StickyStats{},
			"entries":     []routing.StickyEntrySnapshot{},
			"ttl_seconds": 0,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":     true,
		"stats":       h.sticky.Stats(),
		"entries":     h.sticky.Snapshot(100),
		"ttl_seconds": int(h.sticky.TTL() / time.Second),
	})
}

// ---------------------------------------------------------------------------
// Sites
// ---------------------------------------------------------------------------

func (h *AdminHandler) listSites(w http.ResponseWriter, r *http.Request) {
	sites, err := h.db.Site.List()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sites)
}

// detectSiteType runs the AAH-style site-detection chain against a candidate
// URL and returns the normalized family (new-api/one-api/sub2api/…).
func (h *AdminHandler) detectSiteType(w http.ResponseWriter, r *http.Request) {
	url := strings.TrimSpace(r.URL.Query().Get("url"))
	if url == "" {
		writeError(w, http.StatusBadRequest, "missing url")
		return
	}
	result, err := sitedetect.Detect(r.Context(), h.httpClient, url)
	if err != nil {
		writeError(w, http.StatusBadGateway, "site detection failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *AdminHandler) createSite(w http.ResponseWriter, r *http.Request) {
	var site domain.Site
	if err := decodeJSON(w, r, &site, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if site.Status == "" {
		site.Status = domain.StatusEnabled
	}
	id, err := h.db.Site.Create(&site)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	created, err := h.db.Site.GetByID(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if created == nil {
		writeJSON(w, http.StatusCreated, map[string]any{"id": id})
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *AdminHandler) getSite(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	site, err := h.db.Site.GetByID(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if site == nil {
		writeError(w, http.StatusNotFound, "site not found")
		return
	}
	writeJSON(w, http.StatusOK, site)
}

func (h *AdminHandler) updateSite(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var site domain.Site
	if err := decodeJSON(w, r, &site, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	site.ID = id
	if err := h.db.Site.Update(&site); err != nil {
		writeStoreError(w, err)
		return
	}
	updated, _ := h.db.Site.GetByID(id)
	writeJSON(w, http.StatusOK, updated)
}

func (h *AdminHandler) deleteSite(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.db.Site.Delete(id); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ---------------------------------------------------------------------------
// Connections (one-shot create: site + credential + channel)
// ---------------------------------------------------------------------------

type createConnectionRequest struct {
	Name     string `json:"name"`
	BaseURL  string `json:"base_url"`
	Secret   string `json:"secret"`
	TypeHint string `json:"type_hint"`
	Platform string `json:"platform"`
	Status   string `json:"status"`
}

// normalizeBaseURL canonicalizes a provider base URL for site reuse matching
// (trim whitespace and trailing slashes).
func normalizeBaseURL(raw string) string {
	return strings.TrimRight(strings.TrimSpace(raw), "/")
}

func (h *AdminHandler) createConnection(w http.ResponseWriter, r *http.Request) {
	var req createConnectionRequest
	if err := decodeJSON(w, r, &req, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	baseURL := normalizeBaseURL(req.BaseURL)
	if baseURL == "" {
		writeError(w, http.StatusBadRequest, "base_url is required")
		return
	}
	if strings.TrimSpace(req.Secret) == "" {
		writeError(w, http.StatusBadRequest, "secret is required")
		return
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
		writeError(w, http.StatusBadRequest, "invalid base_url")
		return
	}
	platform := strings.TrimSpace(req.Platform)
	if platform == "" {
		platform = strings.TrimSpace(req.TypeHint)
	}
	if platform == "" {
		platform = "openai-compatible"
	}
	status := strings.TrimSpace(req.Status)
	if status == "" {
		status = domain.StatusEnabled
	}
	if status != domain.StatusEnabled && status != domain.StatusDisabled {
		writeError(w, http.StatusBadRequest, "invalid status")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = hostLabel(baseURL)
	}

	// Reuse an existing site with the same normalized base URL.
	siteID, reusedSite := int64(0), false
	if existing, ok := h.findSiteByBaseURL(baseURL); ok {
		siteID = existing.ID
		reusedSite = true
	} else {
		id, err := h.db.Site.Create(&domain.Site{Name: name, BaseURL: baseURL, Platform: platform, Status: status})
		if err != nil {
			writeStoreError(w, err)
			return
		}
		siteID = id
	}
	// Roll back a site we created when a later step fails.
	rollbackSite := func() {
		if !reusedSite {
			_ = h.db.Site.Delete(siteID)
		}
	}

	encSecret, err := h.enc.Encrypt([]byte(req.Secret))
	if err != nil {
		rollbackSite()
		writeError(w, http.StatusInternalServerError, "encryption failed")
		return
	}
	credID, err := h.db.Credential.Create(&domain.Credential{
		SiteID:    siteID,
		Kind:      "api_key",
		SecretEnc: []byte(encSecret),
		Status:    domain.StatusEnabled,
	})
	if err != nil {
		rollbackSite()
		writeStoreError(w, err)
		return
	}
	channelID, err := h.db.Channel.Create(&domain.Channel{
		SiteID:       &siteID,
		CredentialID: &credID,
		Name:         name,
		GroupName:    "default",
		Priority:     0,
		Weight:       100,
		Status:       status,
		TypeHint:     strings.TrimSpace(req.TypeHint),
	})
	if err != nil {
		_ = h.db.Credential.Delete(credID)
		rollbackSite()
		writeStoreError(w, err)
		return
	}
	h.modelsCache.Invalidate()

	channel, _ := h.db.Channel.GetByID(channelID)
	site, _ := h.db.Site.GetByID(siteID)
	writeJSON(w, http.StatusCreated, map[string]any{
		"channel":           channel,
		"site":              site,
		"credential_id":     credID,
		"reused_site":       reusedSite,
		"has_secret":        true,
		"platform":          platform,
		"detection_matched": false,
	})
}

// findSiteByBaseURL returns the first site whose normalized base URL equals
// the given one (used to reuse a site across connection creations).
func (h *AdminHandler) findSiteByBaseURL(baseURL string) (*domain.Site, bool) {
	sites, err := h.db.Site.List()
	if err != nil {
		return nil, false
	}
	for i := range sites {
		if normalizeBaseURL(sites[i].BaseURL) == baseURL {
			return &sites[i], true
		}
	}
	return nil, false
}

func hostLabel(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return raw
	}
	return parsed.Host
}

// ---------------------------------------------------------------------------
// Credentials
// ---------------------------------------------------------------------------

func (h *AdminHandler) listCredentials(w http.ResponseWriter, r *http.Request) {
	siteID, err := strconv.ParseInt(chi.URLParam(r, "siteId"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid siteId")
		return
	}
	creds, err := h.db.Credential.ListBySite(siteID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	// Never expose secret_enc in JSON responses.
	type safeCred struct {
		ID             int64  `json:"id"`
		SiteID         int64  `json:"site_id"`
		Kind           string `json:"kind"`
		HasSecret      bool   `json:"has_secret"`
		MetaJSON       string `json:"meta_json,omitempty"`
		Status         string `json:"status"`
		CheckinEnabled bool   `json:"checkin_enabled"`
		ModelsCSV      string `json:"models_csv,omitempty"`
	}
	result := make([]safeCred, 0, len(creds))
	for _, c := range creds {
		result = append(result, safeCred{
			ID:             c.ID,
			SiteID:         c.SiteID,
			Kind:           c.Kind,
			HasSecret:      len(c.SecretEnc) > 0,
			MetaJSON:       c.MetaJSON,
			Status:         c.Status,
			CheckinEnabled: c.CheckinEnabled,
			ModelsCSV:      c.ModelsCSV,
		})
	}
	writeJSON(w, http.StatusOK, result)
}

type createCredentialRequest struct {
	Kind     string `json:"kind"`
	Secret   string `json:"secret"`
	MetaJSON string `json:"meta_json,omitempty"`
	Status   string `json:"status,omitempty"`
	// ModelsCSV is the per-key model allowlist (comma-separated; empty = all).
	ModelsCSV string `json:"models_csv,omitempty"`
}

func (h *AdminHandler) createCredential(w http.ResponseWriter, r *http.Request) {
	siteID, err := strconv.ParseInt(chi.URLParam(r, "siteId"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid siteId")
		return
	}
	var req createCredentialRequest
	if err := decodeJSON(w, r, &req, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Secret == "" {
		writeError(w, http.StatusBadRequest, "secret is required")
		return
	}
	encSecret, err := h.enc.Encrypt([]byte(req.Secret))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "encryption failed")
		return
	}
	if req.Status == "" {
		req.Status = domain.StatusEnabled
	}
	cred := &domain.Credential{
		SiteID:    siteID,
		Kind:      req.Kind,
		SecretEnc: []byte(encSecret),
		MetaJSON:  req.MetaJSON,
		Status:    req.Status,
		ModelsCSV: req.ModelsCSV,
	}
	id, err := h.db.Credential.Create(cred)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	created, _ := h.db.Credential.GetByID(id)
	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"id":              created.ID,
		"site_id":         created.SiteID,
		"kind":            created.Kind,
		"has_secret":      true,
		"meta_json":       created.MetaJSON,
		"status":          created.Status,
		"checkin_enabled": created.CheckinEnabled,
		"models_csv":      created.ModelsCSV,
		"created_at":      created.CreatedAt,
	})
}

type updateCredentialRequest struct {
	Kind     string `json:"kind,omitempty"`
	Secret   string `json:"secret,omitempty"` // empty keeps existing secret
	MetaJSON string `json:"meta_json,omitempty"`
	Status   string `json:"status,omitempty"`
	// ModelsCSV is the per-key model allowlist; nil keeps the existing value.
	ModelsCSV *string `json:"models_csv,omitempty"`
}

func (h *AdminHandler) updateCredential(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	existing, err := h.db.Credential.GetByID(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if existing == nil {
		writeError(w, http.StatusNotFound, "credential not found")
		return
	}
	var req updateCredentialRequest
	if err := decodeJSON(w, r, &req, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Kind != "" {
		existing.Kind = req.Kind
	}
	if req.Status != "" {
		existing.Status = req.Status
	}
	if req.MetaJSON != "" {
		existing.MetaJSON = req.MetaJSON
	}
	if req.ModelsCSV != nil {
		existing.ModelsCSV = strings.TrimSpace(*req.ModelsCSV)
	}
	if strings.TrimSpace(req.Secret) != "" {
		encSecret, err := h.enc.Encrypt([]byte(req.Secret))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "encryption failed")
			return
		}
		existing.SecretEnc = []byte(encSecret)
		// Replacing secret invalidates any import fingerprint identity.
		existing.ImportFingerprint = ""
	}
	if err := h.db.Credential.Update(existing); err != nil {
		writeStoreError(w, err)
		return
	}
	updated, _ := h.db.Credential.GetByID(id)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":              updated.ID,
		"site_id":         updated.SiteID,
		"kind":            updated.Kind,
		"has_secret":      len(updated.SecretEnc) > 0,
		"meta_json":       updated.MetaJSON,
		"status":          updated.Status,
		"checkin_enabled": updated.CheckinEnabled,
		"models_csv":      updated.ModelsCSV,
		"created_at":      updated.CreatedAt,
		"updated_at":      updated.UpdatedAt,
	})
}

func (h *AdminHandler) deleteCredential(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.db.Credential.Delete(id); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// revealCredentialSecret decrypts and returns a stored credential secret
// (api_key / session token). POST so the admin audit trail records reveals.
// The response is marked no-store and never cached.
func (h *AdminHandler) revealCredentialSecret(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	cred, err := h.db.Credential.GetByID(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if cred == nil {
		writeError(w, http.StatusNotFound, "credential not found")
		return
	}
	if len(cred.SecretEnc) == 0 {
		writeError(w, http.StatusNotFound, "secret_plaintext_unavailable")
		return
	}
	plain, decryptErr := h.enc.Decrypt(string(cred.SecretEnc))
	if decryptErr != nil {
		writeError(w, http.StatusInternalServerError, "secret_decrypt_failed")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]string{"secret": string(plain)})
}

// ---------------------------------------------------------------------------
// Channels
// ---------------------------------------------------------------------------

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

// duplicateChannel clones an existing channel: every field is copied verbatim
// (base_url, credential binding, retry config, payload rules, proxy, headers,
// tags…) with only the name suffixed " (copy)". The clone starts enabled so it
// is immediately usable/editable.
// globalSearch runs the grouped admin search (channels/routes/keys/logs).
func (h *AdminHandler) globalSearch(w http.ResponseWriter, r *http.Request) {
	term := strings.TrimSpace(r.URL.Query().Get("q"))
	if term == "" {
		writeJSON(w, http.StatusOK, &store.SearchHits{})
		return
	}
	hits, err := h.db.Search(term, 10)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if hits.Channels == nil {
		hits.Channels = []store.SearchChannelHit{}
	}
	if hits.Routes == nil {
		hits.Routes = []store.SearchRouteHit{}
	}
	if hits.Credentials == nil {
		hits.Credentials = []store.SearchCredHit{}
	}
	if hits.Logs == nil {
		hits.Logs = []store.SearchLogHit{}
	}
	writeJSON(w, http.StatusOK, hits)
}

// factoryReset wipes all business data (channels, keys, routes, logs,
// histories, rules) while preserving configuration (sites, runtime settings,
// TOTP, backups). The body must carry confirm="RESET" to fire.
func (h *AdminHandler) factoryReset(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Confirm string `json:"confirm"`
	}
	if err := decodeJSON(w, r, &req, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if req.Confirm != "RESET" {
		writeError(w, http.StatusBadRequest, "confirmation text required")
		return
	}
	deleted, err := h.db.FactoryReset()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	h.modelsCache.Invalidate()
	writeJSON(w, http.StatusOK, map[string]any{"deleted": deleted})
}

func (h *AdminHandler) duplicateChannel(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid id")
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
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
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
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid id")
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
		_ = h.db.Channel.RecordPingFailure(ch.ID, checkedAt, "invalid_base_url")
		_ = h.db.HealthHistory.Append(ch.ID, false, 0, "invalid_base_url", checkedAt)
		writeJSON(w, http.StatusOK, map[string]any{"channel_id": ch.ID, "reachable": false, "connectivity_state": domain.ConnectivityStateUnreachable, "error": "invalid_base_url", "checked_at": checkedAt})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	started := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL, nil)
	if err != nil {
		checkedAt := time.Now()
		_ = h.db.Channel.RecordPingFailure(ch.ID, checkedAt, "invalid_url")
		_ = h.db.HealthHistory.Append(ch.ID, false, 0, "invalid_url", checkedAt)
		writeJSON(w, http.StatusOK, map[string]any{"channel_id": ch.ID, "reachable": false, "connectivity_state": domain.ConnectivityStateUnreachable, "error": "invalid_url", "checked_at": checkedAt})
		return
	}
	resp, err := h.httpClient.Do(req)
	latencyMs := int(time.Since(started).Milliseconds())
	if err != nil {
		category := classifyPingError(err)
		checkedAt := time.Now()
		_ = h.db.Channel.RecordPingFailure(ch.ID, checkedAt, category)
		_ = h.db.HealthHistory.Append(ch.ID, false, 0, category, checkedAt)
		writeJSON(w, http.StatusOK, map[string]any{"channel_id": ch.ID, "reachable": false, "connectivity_state": domain.ConnectivityStateUnreachable, "error": category, "latency_ms": latencyMs, "checked_at": checkedAt})
		return
	}
	defer resp.Body.Close()
	// Drain a bounded amount so the connection returns to the pool.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	checkedAt := time.Now()
	_ = h.db.Channel.RecordPingSuccess(ch.ID, checkedAt, latencyMs)
	_ = h.db.HealthHistory.Append(ch.ID, true, latencyMs, "", checkedAt)
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
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
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
	var patch domain.Channel
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
	ch.Priority = patch.Priority
	ch.Weight = patch.Weight
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
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
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

func validRoutingMode(mode string) bool {
	return mode == "" ||
		mode == domain.RoutingModeAuto ||
		mode == domain.RoutingModeLatency ||
		mode == domain.RoutingModeWeighted ||
		mode == domain.RoutingModeAdaptive ||
		mode == domain.RoutingModeSingle
}

// validateSinglePin keeps routing_mode=single consistent: the pin must belong
// to the route being saved. A single route without a pin evaluates as auto
// (fall-back), but a pin pointing at another route's member is a client bug.
func (h *AdminHandler) validateSinglePin(route *domain.Route) error {
	if route.RoutingMode != domain.RoutingModeSingle || route.SingleMemberID == nil {
		return nil
	}
	member, err := h.db.RouteMember.GetByID(*route.SingleMemberID)
	if err != nil {
		return err
	}
	if member == nil || member.RouteID != route.ID {
		return errors.New("single_member_id must be a member of this route")
	}
	return nil
}

// validateRouteRetryOverrides keeps the model-level policy within the same
// bounds as the global runtime policy. The UI supplies these limits too, but
// the API must enforce them because routes can be edited by any Admin client.
func validateRouteRetryOverrides(route *domain.Route) error {
	if route == nil {
		return errors.New("route is required")
	}
	if route.RetryTimes != nil && (*route.RetryTimes < 0 || *route.RetryTimes > 100) {
		return errors.New("retry_times must be between 0 and 100")
	}
	if route.ChannelRetryTimes != nil && (*route.ChannelRetryTimes < 0 || *route.ChannelRetryTimes > 5) {
		return errors.New("channel_retry_times must be between 0 and 5")
	}
	return nil
}

func (h *AdminHandler) listRoutes(w http.ResponseWriter, r *http.Request) {
	routes, err := h.db.Route.List()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, routes)
}

func (h *AdminHandler) listRouteOverviews(w http.ResponseWriter, r *http.Request) {
	routes, err := h.db.RouteMember.ListRouteOverviews()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, routes)
}

func (h *AdminHandler) createRoute(w http.ResponseWriter, r *http.Request) {
	var rt domain.Route
	if err := decodeJSON(w, r, &rt, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if rt.ModelPattern == "" {
		writeError(w, http.StatusBadRequest, "model_pattern is required")
		return
	}
	if !validRoutingMode(rt.RoutingMode) {
		writeError(w, http.StatusBadRequest, "invalid routing_mode")
		return
	}
	if err := validateRouteRetryOverrides(&rt); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Pins are only meaningful on an existing route (members exist first), so
	// creating a route in single mode without a pin is accepted as auto-fall-back.
	id, err := h.db.Route.Create(&rt)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	h.modelsCache.Invalidate()
	created, _ := h.db.Route.GetByID(id)
	writeJSON(w, http.StatusCreated, created)
}

func (h *AdminHandler) getRoute(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	rt, err := h.db.Route.GetByID(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if rt == nil {
		writeError(w, http.StatusNotFound, "route not found")
		return
	}
	writeJSON(w, http.StatusOK, rt)
}

func (h *AdminHandler) updateRoute(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var rt domain.Route
	if err := decodeJSON(w, r, &rt, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	rt.ID = id
	if rt.ModelPattern == "" {
		writeError(w, http.StatusBadRequest, "model_pattern is required")
		return
	}
	if !validRoutingMode(rt.RoutingMode) {
		writeError(w, http.StatusBadRequest, "invalid routing_mode")
		return
	}
	if err := validateRouteRetryOverrides(&rt); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.validateSinglePin(&rt); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.db.Route.Update(&rt); err != nil {
		writeStoreError(w, err)
		return
	}
	h.modelsCache.Invalidate()
	updated, _ := h.db.Route.GetByID(id)
	writeJSON(w, http.StatusOK, updated)
}

func (h *AdminHandler) deleteRoute(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.db.Route.Delete(id); err != nil {
		writeStoreError(w, err)
		return
	}
	h.modelsCache.Invalidate()
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ---------------------------------------------------------------------------
// Route Members
// ---------------------------------------------------------------------------

func (h *AdminHandler) listRouteMembers(w http.ResponseWriter, r *http.Request) {
	routeID, err := strconv.ParseInt(chi.URLParam(r, "routeId"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid routeId")
		return
	}
	members, err := h.db.RouteMember.ListByRoute(routeID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, members)
}

func (h *AdminHandler) createRouteMember(w http.ResponseWriter, r *http.Request) {
	routeID, err := strconv.ParseInt(chi.URLParam(r, "routeId"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid routeId")
		return
	}
	var rm domain.RouteMember
	if err := decodeJSON(w, r, &rm, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	rm.RouteID = routeID
	if rm.Weight < 0 {
		writeError(w, http.StatusBadRequest, "weight must be non-negative")
		return
	}
	id, err := h.db.RouteMember.Create(&rm)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	created, _ := h.db.RouteMember.GetByID(id)
	writeJSON(w, http.StatusCreated, created)
}

func (h *AdminHandler) updateRouteMember(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var rm domain.RouteMember
	if err := decodeJSON(w, r, &rm, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	rm.ID = id
	if rm.Weight < 0 {
		writeError(w, http.StatusBadRequest, "weight must be non-negative")
		return
	}
	if err := h.db.RouteMember.Update(&rm); err != nil {
		writeStoreError(w, err)
		return
	}
	updated, _ := h.db.RouteMember.GetByID(id)
	writeJSON(w, http.StatusOK, updated)
}

func writeStoreError(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "resource not found")
		return
	}
	if strings.Contains(strings.ToLower(err.Error()), "unique constraint failed") {
		writeError(w, http.StatusConflict, "resource already exists")
		return
	}
	writeError(w, http.StatusInternalServerError, "database operation failed")
}

func (h *AdminHandler) clearRouteMemberHealth(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.db.RouteMember.ClearHealth(id); err != nil {
		writeStoreError(w, err)
		return
	}
	updated, err := h.db.RouteMember.GetByID(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *AdminHandler) deleteRouteMember(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.db.RouteMember.Delete(id); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ---------------------------------------------------------------------------
// Downstream Keys
// ---------------------------------------------------------------------------

func (h *AdminHandler) listDownstreamKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := h.db.DownstreamKey.List()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	// Never expose token_hash.
	type safeKey struct {
		ID                   int64   `json:"id"`
		Name                 string  `json:"name"`
		Enabled              bool    `json:"enabled"`
		Scopes               string  `json:"scopes,omitempty"`
		QuotaTotalTokens     int64   `json:"quota_total_tokens"`
		QuotaUsedTokens      int64   `json:"quota_used_tokens"`
		PricePromptPer1k     float64 `json:"price_prompt_per_1k"`
		PriceCompletionPer1k float64 `json:"price_completion_per_1k"`
		PriceCachePer1k      float64 `json:"price_cache_per_1k"`
		ModelAllowlist       string  `json:"model_allowlist,omitempty"`
		ModelDenylist        string  `json:"model_denylist,omitempty"`
		ExpiresAt            string  `json:"expires_at,omitempty"`
		AllowedIPs           string  `json:"allowed_ips,omitempty"`
		EstimatedCost        float64 `json:"estimated_cost"`
		CreatedAt            string  `json:"created_at"`
		HasToken             bool    `json:"has_token"`
	}
	result := make([]safeKey, 0, len(keys))
	for _, k := range keys {
		// Cost estimate uses used tokens split is unknown at key level; charge all used as prompt-equivalent mixed average.
		estimated := 0.0
		if k.QuotaUsedTokens > 0 {
			// Prefer average of prompt/completion unit prices when both set; else whichever is set.
			unit := 0.0
			if k.PricePromptPer1k > 0 && k.PriceCompletionPer1k > 0 {
				unit = (k.PricePromptPer1k + k.PriceCompletionPer1k) / 2
			} else if k.PricePromptPer1k > 0 {
				unit = k.PricePromptPer1k
			} else {
				unit = k.PriceCompletionPer1k
			}
			if unit > 0 {
				estimated = (float64(k.QuotaUsedTokens) / 1000.0) * unit
			}
		}
		// Re-viewable plaintext is available only for keys created after
		// plaintext storage landed (token_enc set).
		hasToken := len(k.TokenEnc) > 0
		result = append(result, safeKey{
			ID:                   k.ID,
			Name:                 k.Name,
			Enabled:              k.Enabled,
			Scopes:               k.Scopes,
			QuotaTotalTokens:     k.QuotaTotalTokens,
			QuotaUsedTokens:      k.QuotaUsedTokens,
			PricePromptPer1k:     k.PricePromptPer1k,
			PriceCompletionPer1k: k.PriceCompletionPer1k,
			PriceCachePer1k:      k.PriceCachePer1k,
			ModelAllowlist:       k.ModelAllowlist,
			ModelDenylist:        k.ModelDenylist,
			ExpiresAt:            k.ExpiresAt,
			AllowedIPs:           k.AllowedIPs,
			EstimatedCost:        estimated,
			CreatedAt:            k.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
			HasToken:             hasToken,
		})
	}
	writeJSON(w, http.StatusOK, result)
}

type createKeyResponse struct {
	ID                   int64   `json:"id"`
	Name                 string  `json:"name"`
	Token                string  `json:"token"`
	Enabled              bool    `json:"enabled"`
	Scopes               string  `json:"scopes,omitempty"`
	QuotaTotalTokens     int64   `json:"quota_total_tokens"`
	QuotaUsedTokens      int64   `json:"quota_used_tokens"`
	PricePromptPer1k     float64 `json:"price_prompt_per_1k"`
	PriceCompletionPer1k float64 `json:"price_completion_per_1k"`
	PriceCachePer1k      float64 `json:"price_cache_per_1k"`
	ModelAllowlist       string  `json:"model_allowlist,omitempty"`
	ModelDenylist        string  `json:"model_denylist,omitempty"`
	ExpiresAt            string  `json:"expires_at,omitempty"`
	AllowedIPs           string  `json:"allowed_ips,omitempty"`
	CreatedAt            string  `json:"created_at"`
}

func (h *AdminHandler) createDownstreamKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
		// Token is optional. When empty, the server generates an mg-… secret.
		// When set, the provided secret is stored as a hash only (raw never re-readable).
		Token                string  `json:"token,omitempty"`
		Scopes               string  `json:"scopes,omitempty"`
		QuotaTotalTokens     int64   `json:"quota_total_tokens"`
		PricePromptPer1k     float64 `json:"price_prompt_per_1k"`
		PriceCompletionPer1k float64 `json:"price_completion_per_1k"`
		PriceCachePer1k      float64 `json:"price_cache_per_1k"`
		GroupName            string  `json:"group_name,omitempty"`
		ModelAllowlist       string  `json:"model_allowlist,omitempty"`
		ModelDenylist        string  `json:"model_denylist,omitempty"`
		ExpiresAt            string  `json:"expires_at,omitempty"`
		AllowedIPs           string  `json:"allowed_ips,omitempty"`
	}
	if err := decodeJSON(w, r, &req, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	normalizedScopes, scopeErr := auth.NormalizeScopes(req.Scopes)
	if scopeErr != nil {
		writeError(w, http.StatusBadRequest, scopeErr.Error())
		return
	}
	req.Scopes = auth.FormatScopes(normalizedScopes)

	raw := auth.NormalizeDownstreamToken(req.Token)
	var hash string
	if raw == "" {
		var genErr error
		hash, raw, genErr = auth.NewToken()
		if genErr != nil {
			writeError(w, http.StatusInternalServerError, "token generation failed")
			return
		}
	} else {
		if err := validateCustomDownstreamToken(raw); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		hash = auth.HashToken(raw)
		existing, lookupErr := h.db.DownstreamKey.GetByHash(hash)
		if lookupErr != nil {
			writeStoreError(w, lookupErr)
			return
		}
		if existing != nil {
			writeError(w, http.StatusConflict, "token already exists")
			return
		}
	}

	if req.QuotaTotalTokens < 0 {
		writeError(w, http.StatusBadRequest, "quota_total_tokens must be >= 0")
		return
	}
	if req.PricePromptPer1k < 0 || req.PriceCompletionPer1k < 0 {
		writeError(w, http.StatusBadRequest, "prices must be >= 0")
		return
	}
	// Keep the encrypted plaintext token so operators can re-view/copy it
	// later (like New-API). Encrypt failure is non-fatal: the key still works,
	// it just cannot be re-viewed (users can rotate it instead).
	tokenEnc := ""
	if encToken, encErr := h.enc.Encrypt([]byte(raw)); encErr == nil {
		tokenEnc = encToken
	}
	key := &domain.DownstreamKey{
		TokenHash:            hash,
		TokenEnc:             []byte(tokenEnc),
		Name:                 req.Name,
		Enabled:              true,
		Scopes:               req.Scopes,
		QuotaTotalTokens:     req.QuotaTotalTokens,
		PricePromptPer1k:     req.PricePromptPer1k,
		PriceCompletionPer1k: req.PriceCompletionPer1k,
		PriceCachePer1k:      req.PriceCachePer1k,
		ModelAllowlist:       strings.TrimSpace(req.ModelAllowlist),
		ModelDenylist:        strings.TrimSpace(req.ModelDenylist),
		ExpiresAt:            strings.TrimSpace(req.ExpiresAt),
		AllowedIPs:           strings.TrimSpace(req.AllowedIPs),
		GroupName:            strings.TrimSpace(req.GroupName),
	}
	id, err := h.db.DownstreamKey.Create(key)
	if err != nil {
		writeStoreError(w, err)
		return
	}

	createdAt := ""
	if created, err := h.db.DownstreamKey.GetByID(id); err == nil && created != nil {
		createdAt = created.CreatedAt.Format("2006-01-02T15:04:05Z07:00")
	}

	writeJSON(w, http.StatusCreated, createKeyResponse{
		ID:                   id,
		Name:                 req.Name,
		Token:                raw,
		Enabled:              true,
		Scopes:               req.Scopes,
		QuotaTotalTokens:     req.QuotaTotalTokens,
		QuotaUsedTokens:      0,
		PricePromptPer1k:     req.PricePromptPer1k,
		PriceCompletionPer1k: req.PriceCompletionPer1k,
		PriceCachePer1k:      req.PriceCachePer1k,
		ModelAllowlist:       strings.TrimSpace(req.ModelAllowlist),
		ModelDenylist:        strings.TrimSpace(req.ModelDenylist),
		CreatedAt:            createdAt,
	})
}

func (h *AdminHandler) updateDownstreamKey(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	existing, err := h.db.DownstreamKey.GetByID(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if existing == nil {
		writeError(w, http.StatusNotFound, "key not found")
		return
	}
	var req struct {
		Name                 *string  `json:"name"`
		Enabled              *bool    `json:"enabled"`
		Scopes               *string  `json:"scopes"`
		QuotaTotalTokens     *int64   `json:"quota_total_tokens"`
		PricePromptPer1k     *float64 `json:"price_prompt_per_1k"`
		PriceCompletionPer1k *float64 `json:"price_completion_per_1k"`
		PriceCachePer1k      *float64 `json:"price_cache_per_1k"`
		GroupName            *string  `json:"group_name"`
		ModelAllowlist       *string  `json:"model_allowlist"`
		ModelDenylist        *string  `json:"model_denylist"`
		ExpiresAt            *string  `json:"expires_at"`
		AllowedIPs           *string  `json:"allowed_ips"`
		ResetUsed            bool     `json:"reset_used"`
	}
	if err := decodeJSON(w, r, &req, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			writeError(w, http.StatusBadRequest, "name is required")
			return
		}
		existing.Name = name
	}
	if req.Enabled != nil {
		existing.Enabled = *req.Enabled
	}
	if req.Scopes != nil {
		normalizedScopes, scopeErr := auth.NormalizeScopes(*req.Scopes)
		if scopeErr != nil {
			writeError(w, http.StatusBadRequest, scopeErr.Error())
			return
		}
		existing.Scopes = auth.FormatScopes(normalizedScopes)
	}
	if req.QuotaTotalTokens != nil {
		if *req.QuotaTotalTokens < 0 {
			writeError(w, http.StatusBadRequest, "quota_total_tokens must be >= 0")
			return
		}
		existing.QuotaTotalTokens = *req.QuotaTotalTokens
	}
	if req.PricePromptPer1k != nil {
		if *req.PricePromptPer1k < 0 {
			writeError(w, http.StatusBadRequest, "price_prompt_per_1k must be >= 0")
			return
		}
		existing.PricePromptPer1k = *req.PricePromptPer1k
	}
	if req.PriceCompletionPer1k != nil {
		if *req.PriceCompletionPer1k < 0 {
			writeError(w, http.StatusBadRequest, "price_completion_per_1k must be >= 0")
			return
		}
		existing.PriceCompletionPer1k = *req.PriceCompletionPer1k
	}
	if req.PriceCachePer1k != nil {
		if *req.PriceCachePer1k < 0 {
			writeError(w, http.StatusBadRequest, "price_cache_per_1k must be >= 0")
			return
		}
		existing.PriceCachePer1k = *req.PriceCachePer1k
	}
	if req.ModelAllowlist != nil {
		existing.ModelAllowlist = strings.TrimSpace(*req.ModelAllowlist)
	}
	if req.ModelDenylist != nil {
		existing.ModelDenylist = strings.TrimSpace(*req.ModelDenylist)
	}
	if req.ExpiresAt != nil {
		existing.ExpiresAt = strings.TrimSpace(*req.ExpiresAt)
	}
	if req.AllowedIPs != nil {
		existing.AllowedIPs = strings.TrimSpace(*req.AllowedIPs)
	}
	if req.GroupName != nil {
		existing.GroupName = strings.TrimSpace(*req.GroupName)
	}
	if err := h.db.DownstreamKey.Update(existing); err != nil {
		writeStoreError(w, err)
		return
	}
	if req.ResetUsed {
		if err := h.db.DownstreamKey.ResetUsage(id); err != nil {
			writeStoreError(w, err)
			return
		}
		existing.QuotaUsedTokens = 0
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":                      existing.ID,
		"name":                    existing.Name,
		"enabled":                 existing.Enabled,
		"scopes":                  existing.Scopes,
		"quota_total_tokens":      existing.QuotaTotalTokens,
		"quota_used_tokens":       existing.QuotaUsedTokens,
		"price_prompt_per_1k":     existing.PricePromptPer1k,
		"price_completion_per_1k": existing.PriceCompletionPer1k,
		"price_cache_per_1k":      existing.PriceCachePer1k,
		"created_at":              existing.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	})
}

func (h *AdminHandler) usageSummary(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	keyID, ok := optionalPositiveQueryID(w, query.Get("downstream_key_id"), "downstream_key_id")
	if !ok {
		return
	}
	summary, err := h.db.Usage.Summary(keyID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	// Cost is now persisted per record at relay time (key prices × model
	// ratio); the summary aggregates the stored amounts directly.
	writeJSON(w, http.StatusOK, summary)
}

// listModelRatios returns all configured billing ratios.
func (h *AdminHandler) listModelRatios(w http.ResponseWriter, r *http.Request) {
	ratios, err := h.db.ModelRatio.ListRatios()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if ratios == nil {
		ratios = []domain.ModelRatio{}
	}
	writeJSON(w, http.StatusOK, ratios)
}

// setModelRatio upserts a model's billing ratio (ratio < 0 deletes it).
func (h *AdminHandler) setModelRatio(w http.ResponseWriter, r *http.Request) {
	model := strings.TrimSpace(chi.URLParam(r, "model"))
	if model == "" {
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}
	var body struct {
		Ratio float64 `json:"ratio"`
	}
	if err := decodeJSON(w, r, &body, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if body.Ratio < 0 || body.Ratio > 1000 {
		writeError(w, http.StatusBadRequest, "ratio must be between 0 and 1000")
		return
	}
	if err := h.db.ModelRatio.SetRatio(model, body.Ratio); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"model": model, "ratio": body.Ratio})
}

func (h *AdminHandler) listUsage(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	limit := 100
	if raw := query.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 500 {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and 500")
			return
		}
		limit = parsed
	}
	keyID, ok := optionalPositiveQueryID(w, query.Get("downstream_key_id"), "downstream_key_id")
	if !ok {
		return
	}
	channelID, ok := optionalPositiveQueryID(w, query.Get("channel_id"), "channel_id")
	if !ok {
		return
	}
	model := strings.TrimSpace(query.Get("model"))
	rows, err := h.db.Usage.List(store.UsageFilter{
		DownstreamKeyID: keyID,
		ChannelID:       channelID,
		Model:           model,
		Limit:           limit,
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (h *AdminHandler) deleteDownstreamKey(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.db.DownstreamKey.Delete(id); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// revealDownstreamKeyToken returns the stored plaintext token for a key.
// Keys created before plaintext storage landed (token_enc empty) return 404
// so the operator can rotate instead. Admin audit records every reveal.
func (h *AdminHandler) revealDownstreamKeyToken(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	enc, err := h.db.DownstreamKey.GetTokenEnc(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if enc == "" {
		writeError(w, http.StatusNotFound, "token_plaintext_unavailable")
		return
	}
	plain, decryptErr := h.enc.Decrypt(enc)
	if decryptErr != nil {
		writeError(w, http.StatusInternalServerError, "token_decrypt_failed")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]string{"token": string(plain)})
}

// rotateDownstreamKeyToken replaces a key's token with a freshly generated
// one; the old token stops working immediately. The new plaintext is returned
// once and also stored encrypted so it can be re-viewed later.
func (h *AdminHandler) rotateDownstreamKeyToken(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	existing, err := h.db.DownstreamKey.GetByID(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if existing == nil {
		writeError(w, http.StatusNotFound, "downstream key not found")
		return
	}
	hash, raw, genErr := auth.NewToken()
	if genErr != nil {
		writeError(w, http.StatusInternalServerError, "token generation failed")
		return
	}
	tokenEnc, encErr := h.enc.Encrypt([]byte(raw))
	if encErr != nil {
		writeError(w, http.StatusInternalServerError, "encryption failed")
		return
	}
	if err := h.db.DownstreamKey.RotateToken(id, hash, tokenEnc); err != nil {
		writeStoreError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]string{"id": strconv.FormatInt(id, 10), "token": raw})
}

func validateCustomDownstreamToken(token string) error {
	if strings.ContainsAny(token, " \t\r\n") {
		return errors.New("token must not contain whitespace")
	}
	// Allow operator-chosen secrets (OpenAI-style sk-…, random strings, etc.).
	if len(token) < 16 {
		return errors.New("token must be at least 16 characters")
	}
	if len(token) > 256 {
		return errors.New("token must be at most 256 characters")
	}
	for _, r := range token {
		if r < 32 || r == 127 {
			return errors.New("token contains invalid control characters")
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Proxy Logs
// ---------------------------------------------------------------------------

// latencyHistogram returns the latency distribution over the newest proxy
// logs (AAH-style 10-bucket histogram; slow = >= 5s).
func (h *AdminHandler) latencyHistogram(w http.ResponseWriter, r *http.Request) {
	sample := 1000
	if raw := r.URL.Query().Get("sample"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 10000 {
			sample = parsed
		}
	}
	hist, err := h.db.ProxyLog.LatencyHistogram(sample)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, hist)
}

func (h *AdminHandler) listProxyLogs(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	limit := 100
	if raw := query.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 500 {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and 500")
			return
		}
		limit = parsed
	}
	siteID, ok := optionalPositiveQueryID(w, query.Get("site_id"), "site_id")
	if !ok {
		return
	}
	channelID, ok := optionalPositiveQueryID(w, query.Get("channel_id"), "channel_id")
	if !ok {
		return
	}
	beforeID, ok := optionalPositiveQueryID(w, query.Get("before_id"), "before_id")
	if !ok {
		return
	}
	model := strings.TrimSpace(query.Get("model"))
	upstreamRequestID := strings.TrimSpace(query.Get("upstream_request_id"))
	var status *int
	failedOnly := false
	if raw := strings.TrimSpace(query.Get("status")); raw != "" {
		if raw == "failed" {
			failedOnly = true
		} else {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed < 100 || parsed > 599 {
				writeError(w, http.StatusBadRequest, "invalid status")
				return
			}
			status = &parsed
		}
	}
	logs, err := h.db.ProxyLog.ListFilter(store.ProxyLogFilter{
		SiteID:            siteID,
		ChannelID:         channelID,
		Model:             model,
		Status:            status,
		FailedOnly:        failedOnly,
		UpstreamRequestID: upstreamRequestID,
		BeforeID:          beforeID,
		Limit:             limit,
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, logs)
}

// listGroups returns all tenant groups (the default group is always present).
func (h *AdminHandler) listGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := h.db.Group.List()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	found := false
	for _, g := range groups {
		if g.Name == "default" {
			found = true
			break
		}
	}
	if !found {
		groups = append(groups, domain.KeyGroup{Name: "default"})
	}
	writeJSON(w, http.StatusOK, groups)
}

// upsertGroup creates or updates a group's quota/rate limits.
func (h *AdminHandler) upsertGroup(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(chi.URLParam(r, "name"))
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	var req struct {
		QuotaTotalTokens *int64 `json:"quota_total_tokens"`
		RatePerMinute    *int   `json:"rate_per_minute"`
		RateBurst        *int   `json:"rate_burst"`
	}
	if err := decodeJSON(w, r, &req, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	quota := int64(0)
	if req.QuotaTotalTokens != nil {
		if *req.QuotaTotalTokens < 0 {
			writeError(w, http.StatusBadRequest, "quota_total_tokens must be >= 0")
			return
		}
		quota = *req.QuotaTotalTokens
	}
	rpm, burst := 0, 0
	if req.RatePerMinute != nil {
		rpm = *req.RatePerMinute
	}
	if req.RateBurst != nil {
		burst = *req.RateBurst
	}
	if rpm < 0 || burst < 0 {
		writeError(w, http.StatusBadRequest, "rate limits must be >= 0")
		return
	}
	if err := h.db.Group.Upsert(name, quota, rpm, burst); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "quota_total_tokens": quota, "rate_per_minute": rpm, "rate_burst": burst})
}

// deleteGroup removes a tenant group (default is protected).
func (h *AdminHandler) deleteGroup(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(chi.URLParam(r, "name"))
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if name == "default" {
		writeError(w, http.StatusBadRequest, "cannot delete the default group")
		return
	}
	if err := h.db.Group.Delete(name); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": name})
}

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

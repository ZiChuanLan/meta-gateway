package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/routing"
	"github.com/lan/meta-gateway/internal/store"
)

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
	id, ok := pathID(w, r, "id")
	if !ok {
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
	sticky := h.sticky.Load()
	if sticky == nil {
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
		"stats":       sticky.Stats(),
		"entries":     sticky.Snapshot(100),
		"ttl_seconds": int(sticky.TTL() / time.Second),
	})
}

// ---------------------------------------------------------------------------
// Sites
// ---------------------------------------------------------------------------

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

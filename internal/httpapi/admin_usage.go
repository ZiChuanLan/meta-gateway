package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

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

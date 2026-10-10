package httpapi

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

// parseTimeRange reads the optional inclusive since/until query bounds
// (RFC3339). An unparseable value is a 400 rather than a silently ignored
// one — a typo in a link must not widen a window back to "all time".
func parseTimeRange(w http.ResponseWriter, query url.Values) (since, until *time.Time, ok bool) {
	for _, spec := range []struct {
		key  string
		dest **time.Time
	}{{"since", &since}, {"until", &until}} {
		raw := strings.TrimSpace(query.Get(spec.key))
		if raw == "" {
			continue
		}
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, spec.key+" must be RFC3339")
			return nil, nil, false
		}
		value := parsed
		*spec.dest = &value
	}
	if since != nil && until != nil && until.Before(*since) {
		writeError(w, http.StatusBadRequest, "until must not be before since")
		return nil, nil, false
	}
	return since, until, true
}

func (h *AdminHandler) usageSummary(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	keyID, ok := optionalPositiveQueryID(w, query.Get("downstream_key_id"), "downstream_key_id")
	if !ok {
		return
	}
	since, until, ok := parseTimeRange(w, query)
	if !ok {
		return
	}
	summary, err := h.db.Usage.SummaryRange(store.UsageScope{DownstreamKeyID: keyID}, since, until)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	// Cost is now persisted per record at relay time (key prices × model
	// ratio); the summary aggregates the stored amounts directly.
	writeJSON(w, http.StatusOK, summary)
}

// usageSeries returns a bucketed request/token/cost series over an inclusive
// window. Aggregating in SQL keeps the overview chart honest for windows that
// hold far more rows than the newest-500 list endpoint can return.
// seriesWindow resolves a chart window from an already-parsed range.
//
// Shared by the console overview and the member app's, so both read
// window_minutes and buckets the same way: an absent `since` means "the last
// N minutes" (window_minutes, default one hour). Unparseable or out-of-range
// values keep their defaults rather than failing the request — a chart drawn on
// a slightly different window still answers the question.
func seriesWindow(query url.Values, since, until *time.Time) (start, end time.Time, buckets int) {
	if since == nil {
		window := time.Hour
		if raw := strings.TrimSpace(query.Get("window_minutes")); raw != "" {
			if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 60*24*31 {
				window = time.Duration(parsed) * time.Minute
			}
		}
		start = time.Now().Add(-window)
	} else {
		start = *since
	}
	end = time.Now()
	if until != nil {
		end = *until
	}
	buckets = 24
	if raw := strings.TrimSpace(query.Get("buckets")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 1500 {
			buckets = parsed
		}
	}
	return start, end, buckets
}

func (h *AdminHandler) usageSeries(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	since, until, ok := parseTimeRange(w, query)
	if !ok {
		return
	}
	start, end, buckets := seriesWindow(query, since, until)
	series, err := h.db.Usage.Series(store.UsageScope{}, start, end, buckets)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, series)
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

// setModelRatio upserts a model's billing ratio.
//
// A negative ratio removes the row instead of storing it: that is what the
// store does (`ModelRatioStore.SetRatio` treats < 0 as "no markup, delete"),
// and it is the only way back to the 1× default once a multiplier has been set
// — without it a markup could be raised but never taken off. The previous check
// rejected every negative value, which left the delete path unreachable.
func (h *AdminHandler) setModelRatio(w http.ResponseWriter, r *http.Request) {
	rawModel, err := url.PathUnescape(chi.URLParam(r, "model"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid model")
		return
	}
	model := strings.TrimSpace(rawModel)
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
	if body.Ratio > 1000 {
		writeError(w, http.StatusBadRequest, "ratio must be between 0 and 1000")
		return
	}
	if err := h.db.ModelRatio.SetRatio(model, body.Ratio); err != nil {
		writeStoreError(w, err)
		return
	}
	if body.Ratio < 0 {
		writeJSON(w, http.StatusOK, map[string]any{"model": model, "deleted": true})
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
	since, until, ok := parseTimeRange(w, query)
	if !ok {
		return
	}
	model := strings.TrimSpace(query.Get("model"))
	rows, err := h.db.Usage.List(store.UsageFilter{
		DownstreamKeyID: keyID,
		ChannelID:       channelID,
		Model:           model,
		Since:           since,
		Until:           until,
		Limit:           limit,
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

// usageTopModels ranks models by token usage inside an inclusive window. The
// aggregation runs in SQL so a wide window is not distorted by the list
// endpoint's row cap.
func (h *AdminHandler) usageTopModels(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	since, until, ok := parseTimeRange(w, query)
	if !ok {
		return
	}
	limit := 8
	if raw := strings.TrimSpace(query.Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 50 {
			limit = parsed
		}
	}
	rows, err := h.db.Usage.TopModels(store.UsageScope{}, since, until, limit)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if rows == nil {
		rows = []store.ModelUsage{}
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
		QuotaTotalTokens *int64   `json:"quota_total_tokens"`
		QuotaTotalCost   *float64 `json:"quota_total_cost"`
		RatePerMinute    *int     `json:"rate_per_minute"`
		RateBurst        *int     `json:"rate_burst"`
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
	// The spend budget is optional and independent of the token quota; both are
	// enforced, so a group with only money configured must survive a save that
	// does not mention tokens.
	quotaCost := 0.0
	if req.QuotaTotalCost != nil {
		if *req.QuotaTotalCost < 0 {
			writeError(w, http.StatusBadRequest, "quota_total_cost must be >= 0")
			return
		}
		quotaCost = *req.QuotaTotalCost
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
	if err := h.db.Group.Upsert(name, quota, quotaCost, rpm, burst); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "quota_total_tokens": quota, "quota_total_cost": quotaCost, "rate_per_minute": rpm, "rate_burst": burst})
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

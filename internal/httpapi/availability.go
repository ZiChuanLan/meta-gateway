package httpapi

import (
	"net/http"
	"strconv"
	"strings"
)

func availabilityHours(r *http.Request) (int, error) {
	value := r.URL.Query().Get("hours")
	if value == "" {
		return 24, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil || (n != 24 && n != 168) {
		return 0, strconv.ErrSyntax
	}
	return n, nil
}
func (h *SiteProbeHandler) observations(w http.ResponseWriter, r *http.Request) {
	hours, err := availabilityHours(r)
	if err != nil {
		writeError(w, 400, "hours must be 24 or 168")
		return
	}
	report, err := h.service.Observations(hours)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, 200, report)
}
func (h *SiteProbeHandler) availability(w http.ResponseWriter, r *http.Request) {
	hours, err := availabilityHours(r)
	if err != nil {
		writeError(w, 400, "hours must be 24 or 168")
		return
	}
	ids := []int64{}
	if raw := r.URL.Query().Get("route_ids"); raw != "" {
		parts := strings.Split(raw, ",")
		if len(parts) > 100 {
			writeError(w, 400, "at most 100 routes")
			return
		}
		for _, part := range parts {
			id, err := strconv.ParseInt(part, 10, 64)
			if err != nil || id <= 0 {
				writeError(w, 400, "invalid route_ids")
				return
			}
			ids = append(ids, id)
		}
	}
	channelID := int64(0)
	if raw := r.URL.Query().Get("channel_id"); raw != "" {
		channelID, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || channelID <= 0 {
			writeError(w, 400, "invalid channel_id")
			return
		}
	}
	report, err := h.service.Availability(ids, channelID, r.URL.Query().Get("group"), hours)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, 200, report)
}

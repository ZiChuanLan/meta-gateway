package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/lan/meta-gateway/internal/store"
)

type AuditHandler struct {
	db                           *store.DB
	retentionDays, retentionRows int
}

func NewAuditHandler(db *store.DB, days, rows int) *AuditHandler {
	return &AuditHandler{db: db, retentionDays: days, retentionRows: rows}
}
func (h *AuditHandler) Register(r chi.Router) {
	r.Get("/audit-events", h.list)
	r.Post("/audit-events/cleanup", h.cleanup)
}

func (h *AuditHandler) list(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 500 {
			writeError(w, 400, "limit must be between 1 and 500")
			return
		}
		limit = parsed
	}
	var before int64
	if raw := r.URL.Query().Get("before_id"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed <= 0 {
			writeError(w, 400, "invalid before_id")
			return
		}
		before = parsed
	}
	events, err := h.db.AuditEvent.List(limit, before)
	if err != nil {
		writeError(w, 500, "failed to list audit events")
		return
	}
	writeJSON(w, 200, events)
}

func (h *AuditHandler) cleanup(w http.ResponseWriter, _ *http.Request) {
	removed, err := h.db.AuditEvent.Cleanup(time.Now(), h.retentionDays, h.retentionRows)
	if err != nil {
		writeError(w, 500, "audit cleanup failed")
		return
	}
	writeJSON(w, 200, map[string]int64{"removed": removed})
}

package httpapi

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/lan/meta-gateway/internal/backup"
)

type BackupHandler struct{ service *backup.Service }

func NewBackupHandler(service *backup.Service) *BackupHandler {
	return &BackupHandler{service: service}
}
func (h *BackupHandler) Register(r chi.Router) {
	r.Get("/backups", h.list)
	r.Post("/backups", h.create)
}

func (h *BackupHandler) create(w http.ResponseWriter, r *http.Request) {
	record, err := h.service.Create(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "backup failed")
		return
	}
	writeJSON(w, http.StatusCreated, record)
}

func (h *BackupHandler) list(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 500 {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and 500")
			return
		}
		limit = parsed
	}
	records, err := h.service.List(limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list backups")
		return
	}
	writeJSON(w, http.StatusOK, records)
}

package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/lan/meta-gateway/internal/checkin"
	"github.com/lan/meta-gateway/internal/store"
)

type CheckinHandler struct {
	db      *store.DB
	service *checkin.Service
}

func NewCheckinHandler(db *store.DB, service *checkin.Service) *CheckinHandler {
	return &CheckinHandler{db: db, service: service}
}

func (h *CheckinHandler) Register(r chi.Router) {
	r.Post("/checkin/credentials/{id}/run", h.runCredential)
	r.Post("/checkin/run", h.runAll)
	r.Get("/checkin/logs", h.listLogs)
	r.Put("/credentials/{id}/checkin", h.setCredentialEnabled)
}

func (h *CheckinHandler) runCredential(w http.ResponseWriter, r *http.Request) {
	id, ok := positivePathID(w, r, "id", "invalid credential id")
	if !ok {
		return
	}
	result, err := h.service.RunCredential(r.Context(), id, checkin.SourceManual, false)
	if err != nil {
		writeCheckinError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *CheckinHandler) runAll(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.RunAll(r.Context(), checkin.SourceManual)
	if err != nil {
		writeCheckinError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *CheckinHandler) listLogs(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	limit := 100
	if raw := query.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 || parsed > 500 {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and 500")
			return
		}
		limit = parsed
	}
	credentialID, ok := optionalPositiveQueryID(w, query.Get("credential_id"), "credential_id")
	if !ok {
		return
	}
	siteID, ok := optionalPositiveQueryID(w, query.Get("site_id"), "site_id")
	if !ok {
		return
	}
	status := query.Get("status")
	if status != "" && status != checkin.StatusSuccess && status != checkin.StatusFailed && status != checkin.StatusSkipped {
		writeError(w, http.StatusBadRequest, "invalid status")
		return
	}
	source := query.Get("source")
	if source != "" && source != checkin.SourceManual && source != checkin.SourceScheduled {
		writeError(w, http.StatusBadRequest, "invalid source")
		return
	}
	logs, err := h.db.CheckinLog.List(store.CheckinLogFilter{
		CredentialID: credentialID,
		SiteID:       siteID,
		Status:       status,
		Source:       source,
		Limit:        limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list check-in logs")
		return
	}
	writeJSON(w, http.StatusOK, logs)
}

func (h *CheckinHandler) setCredentialEnabled(w http.ResponseWriter, r *http.Request) {
	id, ok := positivePathID(w, r, "id", "invalid credential id")
	if !ok {
		return
	}
	var request struct {
		Enabled *bool `json:"enabled"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || request.Enabled == nil {
		writeError(w, http.StatusBadRequest, "invalid check-in configuration")
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid check-in configuration")
		return
	}
	if err := h.db.Credential.SetCheckinEnabled(id, *request.Enabled); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "credential not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update check-in configuration")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"credential_id": id, "checkin_enabled": *request.Enabled})
}

func positivePathID(w http.ResponseWriter, r *http.Request, name, message string) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, name), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, message)
		return 0, false
	}
	return id, true
}

func optionalPositiveQueryID(w http.ResponseWriter, raw, name string) (*int64, bool) {
	if raw == "" {
		return nil, true
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid "+name)
		return nil, false
	}
	return &id, true
}

func writeCheckinError(w http.ResponseWriter, err error) {
	var checkinErr *checkin.Error
	if errors.As(err, &checkinErr) && checkinErr.Kind == checkin.ErrorNotFound {
		writeError(w, http.StatusNotFound, checkinErr.Category)
		return
	}
	writeError(w, http.StatusInternalServerError, "check-in failed")
}

package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/lan/meta-gateway/internal/discovery"
	"github.com/lan/meta-gateway/internal/store"
)

type DiscoveryHandler struct {
	db      *store.DB
	service *discovery.Service
}

func NewDiscoveryHandler(db *store.DB, service *discovery.Service) *DiscoveryHandler {
	return &DiscoveryHandler{db: db, service: service}
}

func (h *DiscoveryHandler) Register(r chi.Router) {
	r.Post("/discovery/channels/{id}/probe", h.probeChannel)
	r.Post("/discovery/channels/{id}/refresh", h.refreshChannel)
	r.Post("/discovery/refresh", h.refreshAll)
	r.Get("/discovery/models", h.listModels)
}

func (h *DiscoveryHandler) probeChannel(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid channel id")
		return
	}
	result, err := h.service.Probe(r.Context(), id)
	if err != nil {
		writeDiscoveryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *DiscoveryHandler) refreshChannel(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid channel id")
		return
	}
	result, err := h.service.Refresh(r.Context(), id)
	if err != nil {
		writeDiscoveryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *DiscoveryHandler) refreshAll(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.RefreshAll(r.Context())
	if err != nil {
		writeDiscoveryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *DiscoveryHandler) listModels(w http.ResponseWriter, r *http.Request) {
	var channelID *int64
	if raw := r.URL.Query().Get("channel_id"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			writeError(w, http.StatusBadRequest, "invalid channel_id")
			return
		}
		channelID = &id
	}
	models, err := h.db.DiscoveredModel.List(channelID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list discovered models")
		return
	}
	writeJSON(w, http.StatusOK, models)
}

func writeDiscoveryError(w http.ResponseWriter, err error) {
	var discoveryErr *discovery.Error
	if !errors.As(err, &discoveryErr) {
		writeError(w, http.StatusInternalServerError, "discovery failed")
		return
	}
	switch discoveryErr.Kind {
	case discovery.ErrorNotFound:
		writeError(w, http.StatusNotFound, discoveryErr.Category)
	case discovery.ErrorUnavailable:
		writeError(w, http.StatusUnprocessableEntity, discoveryErr.Category)
	case discovery.ErrorUpstream:
		writeError(w, http.StatusBadGateway, discoveryErr.Category)
	default:
		writeError(w, http.StatusInternalServerError, "discovery failed")
	}
}

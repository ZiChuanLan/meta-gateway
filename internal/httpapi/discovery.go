package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/lan/meta-gateway/internal/discovery"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

type DiscoveryHandler struct {
	db          *store.DB
	service     *discovery.Service
	modelsCache *modelsCache
}

func NewDiscoveryHandler(db *store.DB, service *discovery.Service) *DiscoveryHandler {
	return &DiscoveryHandler{db: db, service: service}
}

func (h *DiscoveryHandler) SetModelsCache(cache *modelsCache) {
	h.modelsCache = cache
}

func (h *DiscoveryHandler) Register(r chi.Router) {
	r.Post("/discovery/channels/{id}/probe", h.probeChannel)
	r.Post("/discovery/channels/{id}/refresh", h.refreshChannel)
	r.Post("/discovery/refresh", h.refreshAll)
	r.Post("/discovery/models-preview", h.previewModels)
	r.Get("/discovery/models", h.listModels)
	r.Get("/discovery/missing-models", h.missingModels)
	r.Get("/discovery/model-channels", h.modelChannels)
}

// modelsPreviewRequest is a connection that has not been saved yet: the add-channel
// dialog's 获取模型 asks what an upstream would serve before anything is stored.
type modelsPreviewRequest struct {
	BaseURL  string `json:"base_url"`
	Secret   string `json:"secret"`
	TypeHint string `json:"type_hint"`
}

// previewModels lists the models behind a base URL and a key that are not stored
// anywhere. Read-only: no probe record, no health history, no model adoption. The
// alternative — create the channel, then discover — leaves a site, a credential
// and a channel behind every time the URL or the key turns out wrong.
func (h *DiscoveryHandler) previewModels(w http.ResponseWriter, r *http.Request) {
	var req modelsPreviewRequest
	if err := decodeJSON(w, r, &req, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if strings.TrimSpace(req.Secret) == "" {
		writeError(w, http.StatusBadRequest, "secret is required")
		return
	}
	// Bounded: the dialog blocks on this, and an upstream that accepts the
	// connection and then says nothing must not hold the form open.
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	result, err := h.service.PreviewModels(ctx, req.BaseURL, req.TypeHint, req.Secret)
	if err != nil {
		writeDiscoveryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// missingModels reports channel-exposed models no enabled route covers.
func (h *DiscoveryHandler) missingModels(w http.ResponseWriter, r *http.Request) {
	missing, err := h.db.MissingModels()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "missing models")
		return
	}
	if missing == nil {
		missing = []store.MissingModel{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": missing})
}

// modelChannels lists the enabled channels serving a route pattern — the live
// preview behind the add-route dialog's auto-match option. `match=related`
// widens the check to the pattern's -sibling models as well.
func (h *DiscoveryHandler) modelChannels(w http.ResponseWriter, r *http.Request) {
	pattern := strings.TrimSpace(r.URL.Query().Get("model"))
	if pattern == "" {
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}
	matches, err := h.db.ChannelsMatchingModel(pattern, store.ParseModelMatchMode(r.URL.Query().Get("match")))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to match channels")
		return
	}
	if matches == nil {
		matches = []store.ModelChannelMatch{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": matches})
}

func (h *DiscoveryHandler) probeChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	result, err := h.service.Probe(r.Context(), id)
	if err != nil {
		writeDiscoveryError(w, err)
		return
	}
	h.modelsCache.Invalidate()
	writeJSON(w, http.StatusOK, result)
}

func (h *DiscoveryHandler) refreshChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	result, err := h.service.Refresh(r.Context(), id)
	if err != nil {
		writeDiscoveryError(w, err)
		return
	}
	h.modelsCache.Invalidate()
	writeJSON(w, http.StatusOK, result)
}

func (h *DiscoveryHandler) refreshAll(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.RefreshAll(r.Context())
	if err != nil {
		writeDiscoveryError(w, err)
		return
	}
	h.modelsCache.Invalidate()
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
	if models == nil {
		// Same contract as the refresh payload: a channel with no discovered
		// models answers with an empty array, never JSON null.
		models = []domain.DiscoveredModel{}
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

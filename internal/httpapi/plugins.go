package httpapi

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/lan/meta-gateway/internal/plugins"
)

type PluginHandler struct {
	service *plugins.Service
}

func NewPluginHandler(service *plugins.Service) *PluginHandler {
	return &PluginHandler{service: service}
}

func (h *PluginHandler) Register(r chi.Router) {
	r.Get("/plugins/catalog", h.catalog)
	r.Get("/plugins/status", h.status)
	r.Get("/plugins", h.list)
	r.Post("/plugins/{id}/activate", h.activate)
	r.Post("/plugins/{id}/install", h.install)
	r.Post("/plugins/{id}/enable", h.enable)
	r.Post("/plugins/{id}/disable", h.disable)
	r.Delete("/plugins/{id}", h.uninstall)
}

func (h *PluginHandler) catalog(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.service.Catalog())
}

func (h *PluginHandler) list(w http.ResponseWriter, _ *http.Request) {
	items, err := h.service.ListInstalled()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error")
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (h *PluginHandler) status(w http.ResponseWriter, _ *http.Request) {
	items, err := h.service.Status()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error")
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (h *PluginHandler) activate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	rec, err := h.service.Activate(id)
	if err != nil {
		writePluginError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

func (h *PluginHandler) install(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	rec, err := h.service.Install(id)
	if err != nil {
		writePluginError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, rec)
}

func (h *PluginHandler) enable(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	rec, err := h.service.Enable(id)
	if err != nil {
		writePluginError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

func (h *PluginHandler) disable(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	rec, err := h.service.Disable(id)
	if err != nil {
		writePluginError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

func (h *PluginHandler) uninstall(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.service.Uninstall(id); err != nil {
		writePluginError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func writePluginError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, plugins.ErrNotFound):
		writeError(w, http.StatusNotFound, "plugin_not_found")
	case errors.Is(err, plugins.ErrNotInstalled):
		writeError(w, http.StatusNotFound, "plugin_not_installed")
	case errors.Is(err, plugins.ErrAlreadyExists):
		writeError(w, http.StatusConflict, "plugin_already_installed")
	case errors.Is(err, plugins.ErrInvalidID):
		writeError(w, http.StatusBadRequest, "plugin_invalid_id")
	case errors.Is(err, plugins.ErrCoreImmutable):
		writeError(w, http.StatusBadRequest, "plugin_core_immutable")
	default:
		writeError(w, http.StatusInternalServerError, "internal_error")
	}
}

// requirePluginEnabled returns middleware that 404s unless plugin id is enabled.
func requirePluginEnabled(service *plugins.Service, id string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if service == nil || !service.IsEnabled(id) {
				writeError(w, http.StatusNotFound, "plugin_disabled")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

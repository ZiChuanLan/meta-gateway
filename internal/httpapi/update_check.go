package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/lan/meta-gateway/internal/buildinfo"
	"github.com/lan/meta-gateway/internal/runtimeconfig"
	"github.com/lan/meta-gateway/internal/updatecheck"
)

// UpdateCheckHandler exposes the cached GitHub release comparison that powers
// the console update badge. Requests never hit GitHub while the admin toggle
// is off; with it on, a stale cache refreshes inline (bounded by the service
// client timeout).
type UpdateCheckHandler struct {
	service    *updatecheck.Service
	controller *runtimeconfig.Controller
}

func NewUpdateCheckHandler(service *updatecheck.Service, controller *runtimeconfig.Controller) *UpdateCheckHandler {
	return &UpdateCheckHandler{service: service, controller: controller}
}

func (h *UpdateCheckHandler) Register(r chi.Router) {
	r.Get("/update-check", h.get)
	r.Post("/update-check/refresh", h.refresh)
}

func (h *UpdateCheckHandler) get(w http.ResponseWriter, r *http.Request) {
	enabled := h.controller.Snapshot().Editable.UpdateCheckEnabled
	status := h.service.Status()
	if enabled {
		status = h.service.RefreshIfStale(r.Context(), h.service.Interval())
	}
	h.write(w, enabled, status)
}

// refresh forces a synchronous comparison regardless of cache freshness.
func (h *UpdateCheckHandler) refresh(w http.ResponseWriter, r *http.Request) {
	enabled := h.controller.Snapshot().Editable.UpdateCheckEnabled
	status := h.service.Status()
	if enabled {
		status = h.service.Refresh(r.Context())
	}
	h.write(w, enabled, status)
}

func (h *UpdateCheckHandler) write(w http.ResponseWriter, enabled bool, status updatecheck.Status) {
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":     enabled,
		"current":     buildinfo.Version,
		"latest":      status.Latest,
		"has_update":  status.HasUpdate,
		"release_url": status.URL,
		"checked_at":  status.CheckedAt,
		"error":       status.Err,
	})
}

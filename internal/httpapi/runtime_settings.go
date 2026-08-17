package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/lan/meta-gateway/internal/checkin"
	"github.com/lan/meta-gateway/internal/runtimeconfig"
)

// RuntimeSettingsHandler exposes effective runtime parameters and accepts Admin overrides.
type RuntimeSettingsHandler struct {
	controller *runtimeconfig.Controller
}

func NewRuntimeSettingsHandler(controller *runtimeconfig.Controller) *RuntimeSettingsHandler {
	return &RuntimeSettingsHandler{controller: controller}
}

func (h *RuntimeSettingsHandler) Register(r chi.Router) {
	r.Get("/runtime-settings", h.getRuntimeSettings)
	r.Put("/runtime-settings", h.putRuntimeSettings)
	r.Post("/runtime-settings/reset", h.resetRuntimeSettings)
}

func (h *RuntimeSettingsHandler) getRuntimeSettings(w http.ResponseWriter, _ *http.Request) {
	if h.controller == nil {
		writeError(w, http.StatusInternalServerError, "runtime settings unavailable")
		return
	}
	writeJSON(w, http.StatusOK, h.controller.Snapshot())
}

func (h *RuntimeSettingsHandler) putRuntimeSettings(w http.ResponseWriter, r *http.Request) {
	if h.controller == nil {
		writeError(w, http.StatusInternalServerError, "runtime settings unavailable")
		return
	}
	var body runtimeconfig.Editable
	defer r.Body.Close()
	if err := decodeJSON(w, r, &body, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	snapshot, err := h.controller.Update(body)
	if err != nil {
		if errors.Is(err, checkin.ErrInvalidCron) || err.Error() == "checkin_cron is invalid" {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		// Validation errors are client-facing.
		msg := err.Error()
		if msg != "" && (strings.Contains(msg, "must be") || strings.Contains(msg, "out of range") || strings.Contains(msg, "invalid")) {
			writeError(w, http.StatusBadRequest, msg)
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update runtime settings")
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (h *RuntimeSettingsHandler) resetRuntimeSettings(w http.ResponseWriter, _ *http.Request) {
	if h.controller == nil {
		writeError(w, http.StatusInternalServerError, "runtime settings unavailable")
		return
	}
	snapshot, err := h.controller.ClearOverride()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to reset runtime settings")
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

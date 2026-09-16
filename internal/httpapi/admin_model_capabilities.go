package httpapi

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/lan/meta-gateway/internal/domain"
)

// listModelCapabilities returns every persisted capability row.
func (h *AdminHandler) listModelCapabilities(w http.ResponseWriter, r *http.Request) {
	items, err := h.db.ModelCapability.List()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if items == nil {
		items = []domain.Capability{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "kinds": domain.CapabilityKinds()})
}

// upsertModelCapability creates or updates one model's capability. Absent
// fields keep their previous value, so PATCH-style partial updates are safe.
func (h *AdminHandler) upsertModelCapability(w http.ResponseWriter, r *http.Request) {
	name, err := url.PathUnescape(chi.URLParam(r, "name"))
	if err != nil || strings.TrimSpace(name) == "" {
		writeError(w, http.StatusBadRequest, "model name required")
		return
	}
	name = strings.TrimSpace(name)
	var req struct {
		Kind             *string  `json:"kind"`
		Provider         *string  `json:"provider"`
		Endpoints        []string `json:"endpoints"`
		InputFormats     []string `json:"input_formats"`
		InputModalities  []string `json:"input_modalities"`
		OutputModalities []string `json:"output_modalities"`
		MaxInputImages   *int     `json:"max_input_images"`
		SupportsStream   *bool    `json:"supports_stream"`
		SupportsTools    *bool    `json:"supports_tools"`
		SupportsJSONMode *bool    `json:"supports_json_mode"`
		AsyncTask        *bool    `json:"async_task"`
		SizeOptions      *string  `json:"size_options"`
		Notes            *string  `json:"notes"`
	}
	if err := decodeJSON(w, r, &req, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	existing, err := h.db.ModelCapability.Get(name)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	cap := domain.ClassifyModel(name)
	cap.Model = name
	cap.Source = domain.CapabilitySourceBuiltin
	if existing != nil {
		cap = *existing
	}
	// Any explicit edit turns the row into an operator override so a later
	// discovery sweep cannot silently revert it.
	cap.Source = domain.CapabilitySourceManual
	if req.Kind != nil {
		cap.Kind = domain.NormalizeCapabilityKind(*req.Kind)
	}
	if req.Provider != nil {
		cap.Provider = strings.TrimSpace(*req.Provider)
	}
	if req.Endpoints != nil {
		cap.Endpoints = req.Endpoints
	}
	if req.InputFormats != nil {
		cap.InputFormats = req.InputFormats
	}
	if req.InputModalities != nil {
		cap.InputModalities = req.InputModalities
	}
	if req.OutputModalities != nil {
		cap.OutputModalities = req.OutputModalities
	}
	if req.MaxInputImages != nil {
		if *req.MaxInputImages < 0 {
			writeError(w, http.StatusBadRequest, "max_input_images must be >= 0")
			return
		}
		cap.MaxInputImages = *req.MaxInputImages
	}
	if req.SupportsStream != nil {
		cap.SupportsStream = *req.SupportsStream
	}
	if req.SupportsTools != nil {
		cap.SupportsTools = *req.SupportsTools
	}
	if req.SupportsJSONMode != nil {
		cap.SupportsJSONMode = *req.SupportsJSONMode
	}
	if req.AsyncTask != nil {
		cap.AsyncTask = *req.AsyncTask
	}
	if req.SizeOptions != nil {
		cap.SizeOptions = strings.TrimSpace(*req.SizeOptions)
	}
	if req.Notes != nil {
		cap.Notes = strings.TrimSpace(*req.Notes)
	}
	if len(cap.Endpoints) == 0 {
		writeError(w, http.StatusBadRequest, "endpoints must not be empty")
		return
	}
	if err := h.db.ModelCapability.Upsert(&cap); err != nil {
		writeStoreError(w, err)
		return
	}
	stored, err := h.db.ModelCapability.Get(name)
	if err != nil || stored == nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, stored)
}

// deleteModelCapability drops the override so the model falls back to the
// built-in classifier again.
func (h *AdminHandler) deleteModelCapability(w http.ResponseWriter, r *http.Request) {
	name, err := url.PathUnescape(chi.URLParam(r, "name"))
	if err != nil || strings.TrimSpace(name) == "" {
		writeError(w, http.StatusBadRequest, "model name required")
		return
	}
	if err := h.db.ModelCapability.Delete(strings.TrimSpace(name)); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// resolveModelCapabilities answers "how do I call these models" for a batch of
// names, falling back to the built-in classifier for unknown models. The image
// workbench and the chat-to-edit shim both read this.
func (h *AdminHandler) resolveModelCapabilities(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Models []string `json:"models"`
	}
	if err := decodeJSON(w, r, &req, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if len(req.Models) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"items": map[string]domain.Capability{}})
		return
	}
	if len(req.Models) > 500 {
		writeError(w, http.StatusBadRequest, "too many models (max 500)")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": h.db.ModelCapability.ResolveMany(req.Models)})
}

// autoTagModelCapabilities persists built-in classifications for models that
// have no row yet. Existing rows — including manual overrides — are untouched.
func (h *AdminHandler) autoTagModelCapabilities(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Models []string `json:"models"`
	}
	if err := decodeJSON(w, r, &req, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	tagged := 0
	for _, m := range req.Models {
		if strings.TrimSpace(m) == "" {
			continue
		}
		if err := h.db.ModelCapability.AutoTag(m); err != nil {
			writeStoreError(w, err)
			return
		}
		tagged++
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "requested": tagged})
}

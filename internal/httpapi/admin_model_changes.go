package httpapi

import (
	"errors"
	"net/http"

	"github.com/lan/meta-gateway/internal/store"
)

func writeModelChangeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrModelChangeInvalid):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, store.ErrModelChangeConflict):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeStoreError(w, err)
	}
}
func (h *AdminHandler) listModelChanges(w http.ResponseWriter, r *http.Request) {
	result, err := h.db.ModelChanges()
	if err != nil {
		writeModelChangeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
func (h *AdminHandler) ignoreModelChanges(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs []int64 `json:"ids"`
	}
	if err := decodeJSON(w, r, &req, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if err := h.db.IgnoreModelChanges(req.IDs); err != nil {
		writeModelChangeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"updated": len(req.IDs)})
}
func (h *AdminHandler) previewModelChanges(w http.ResponseWriter, r *http.Request) {
	var req store.ModelChangeRequest
	if err := decodeJSON(w, r, &req, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	result, err := h.db.PreviewModelChanges(req)
	if err != nil {
		writeModelChangeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
func (h *AdminHandler) applyModelChanges(w http.ResponseWriter, r *http.Request) {
	var req store.ModelChangeRequest
	if err := decodeJSON(w, r, &req, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	updated, err := h.db.ApplyModelChanges(req)
	if err != nil {
		writeModelChangeError(w, err)
		return
	}
	h.modelsCache.Invalidate()
	writeJSON(w, http.StatusOK, map[string]int{"updated": updated})
}

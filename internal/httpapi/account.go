package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/lan/meta-gateway/internal/account"
)

type AccountHandler struct {
	service *account.Service
}

func NewAccountHandler(service *account.Service) *AccountHandler {
	return &AccountHandler{service: service}
}

func (h *AccountHandler) Register(r chi.Router) {
	r.Post("/channels/{id}/account/probe", h.probeAccount)
	r.Post("/channels/{id}/account/sync-keys", h.syncKeys)
}

func (h *AccountHandler) probeAccount(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid channel id")
		return
	}
	result, err := h.service.Probe(r.Context(), id)
	if err != nil {
		writeAccountError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *AccountHandler) syncKeys(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid channel id")
		return
	}
	var request account.SyncKeysRequest
	body, readErr := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if readErr != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &request); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
	}
	result, err := h.service.SyncKeys(r.Context(), id, request)
	if err != nil {
		writeAccountError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func writeAccountError(w http.ResponseWriter, err error) {
	var accountErr *account.Error
	if !errors.As(err, &accountErr) {
		writeError(w, http.StatusInternalServerError, "account failed")
		return
	}
	switch accountErr.Kind {
	case account.ErrorNotFound:
		writeError(w, http.StatusNotFound, accountErr.Category)
	case account.ErrorUnavailable:
		writeError(w, http.StatusUnprocessableEntity, accountErr.Category)
	case account.ErrorUpstream:
		writeError(w, http.StatusBadGateway, accountErr.Category)
	default:
		writeError(w, http.StatusInternalServerError, "account failed")
	}
}

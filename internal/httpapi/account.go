package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/lan/meta-gateway/internal/account"
	"github.com/lan/meta-gateway/internal/adapters"
)

type AccountHandler struct {
	service *account.Service
}

func NewAccountHandler(service *account.Service) *AccountHandler {
	return &AccountHandler{service: service}
}

func (h *AccountHandler) Register(r chi.Router) {
	r.Post("/channels/account/probe-all", h.probeAll)
	r.Get("/channels/account/finance", h.finance)
	r.Post("/channels/{id}/account/probe", h.probeAccount)
	r.Post("/channels/{id}/account/sync-keys", h.syncKeys)
	r.Post("/channels/{id}/account/create-key", h.createKey)
	r.Get("/channels/{id}/account/pricing", h.pricing)
	r.Get("/channels/{id}/account/token-groups", h.tokenGroups)
	r.Get("/balance-history", h.balanceHistory)
}

// balanceHistory serves the dashboard balance trend chart.
func (h *AccountHandler) balanceHistory(w http.ResponseWriter, r *http.Request) {
	days := 30
	if raw := r.URL.Query().Get("days"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 365 {
			days = parsed
		}
	}
	points, err := h.service.BalanceHistory(r.Context(), days)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "balance history")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": points})
}

func (h *AccountHandler) finance(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.FinanceOverview(r.Context())
	if err != nil {
		writeAccountError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *AccountHandler) probeAll(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.ProbeAll(r.Context())
	if err != nil {
		writeAccountError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
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

func (h *AccountHandler) createKey(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid channel id")
		return
	}
	var request account.CreateKeyRequest
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
	result, err := h.service.CreateKey(r.Context(), id, request)
	if err != nil {
		writeAccountError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *AccountHandler) pricing(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid channel id")
		return
	}
	prices, err := h.service.GetPricing(r.Context(), id)
	if err != nil {
		writeAccountError(w, err)
		return
	}
	if prices == nil {
		prices = []adapters.ModelPrice{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"prices": prices})
}

func (h *AccountHandler) tokenGroups(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid channel id")
		return
	}
	groups, err := h.service.ListTokenGroups(r.Context(), id)
	if err != nil {
		writeAccountError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": groups})
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

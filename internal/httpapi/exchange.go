package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/lan/meta-gateway/internal/exchange"
)

type ExchangeHandler struct {
	service           *exchange.Service
	allowSecretExport bool
}

func NewExchangeHandler(service *exchange.Service, allowSecretExport bool) *ExchangeHandler {
	return &ExchangeHandler{service: service, allowSecretExport: allowSecretExport}
}

func (h *ExchangeHandler) Register(r chi.Router) {
	r.Post("/exchange/export", h.export)
	r.Post("/exchange/import", h.importDocument)
}

func (h *ExchangeHandler) export(w http.ResponseWriter, r *http.Request) {
	var request exchange.ExportRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, string(exchange.ErrorValidation))
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, string(exchange.ErrorValidation))
		return
	}
	if request.IncludeSecrets {
		if !h.allowSecretExport {
			writeError(w, http.StatusForbidden, "secret_export_disabled")
			return
		}
		if len(request.ChannelIDs) == 0 {
			writeError(w, http.StatusBadRequest, "channel_ids_required_for_secret_export")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
	}
	envelope, err := h.service.Export(r.Context(), request)
	if err != nil {
		writeExchangeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, envelope)
}

func (h *ExchangeHandler) importDocument(w http.ResponseWriter, r *http.Request) {
	reader := http.MaxBytesReader(w, r.Body, exchange.MaxBodyBytes)
	body, err := io.ReadAll(reader)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "body_too_large")
			return
		}
		writeError(w, http.StatusBadRequest, string(exchange.ErrorValidation))
		return
	}
	result, err := h.service.Import(r.Context(), body)
	if err != nil {
		writeExchangeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func writeExchangeError(w http.ResponseWriter, err error) {
	var exchangeErr *exchange.Error
	if !errors.As(err, &exchangeErr) {
		writeError(w, http.StatusInternalServerError, string(exchange.ErrorInternal))
		return
	}
	switch exchangeErr.Kind {
	case exchange.ErrorValidation:
		writeError(w, http.StatusBadRequest, string(exchangeErr.Kind))
	case exchange.ErrorUnsupported:
		writeError(w, http.StatusUnprocessableEntity, string(exchangeErr.Kind))
	case exchange.ErrorConflict:
		writeError(w, http.StatusConflict, string(exchangeErr.Kind))
	case exchange.ErrorNotFound:
		writeError(w, http.StatusNotFound, string(exchangeErr.Kind))
	default:
		writeError(w, http.StatusInternalServerError, string(exchange.ErrorInternal))
	}
}

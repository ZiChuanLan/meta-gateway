package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/lan/meta-gateway/internal/exchange"
	"github.com/lan/meta-gateway/internal/webdavsync"
)

// Import failure categories exposed to the console. They are deliberately
// distinct from the generic validation_error / unsupported_format codes used by
// other admin endpoints: on the import screens those codes rendered as
// "check Base URL and credentials", which has nothing to do with a rejected
// backup document.
const (
	categoryDocumentInvalid     = "exchange_document_invalid"
	categoryDocumentUnsupported = "exchange_document_unsupported"
	categoryDocumentEmpty       = "exchange_document_empty"
	categoryDocumentConflict    = "exchange_document_conflict"
	categoryUnlockRequired      = "backup_unlock_required"
	categoryDecryptFailed       = "decrypt_failed"
)

// maxEncryptedImportBytes bounds the transported request when the payload is an
// encrypted envelope (base64 inflates the plaintext by roughly a third).
const maxEncryptedImportBytes = exchange.MaxBodyBytes*3/2 + (1 << 20)

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
	r.Post("/exchange/import-encrypted", h.importEncryptedDocument)
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
		writeError(w, http.StatusBadRequest, categoryDocumentInvalid)
		return
	}
	// An encrypted backup dropped on the plain import path is not a malformed
	// document — it just needs its unlock password. Say so instead of letting
	// the parser report an unrecognized format.
	if _, encrypted := webdavsync.TryParseEncryptedEnvelope(body); encrypted {
		writeError(w, http.StatusBadRequest, categoryUnlockRequired)
		return
	}
	result, err := h.service.Import(r.Context(), body)
	if err != nil {
		writeExchangeImportError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// importEncryptedDocument decrypts an AAH-encrypted backup envelope and imports
// the plaintext. The file importer and the WebDAV pull share one envelope
// implementation, so the same backup opens on either path.
func (h *ExchangeHandler) importEncryptedDocument(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Document json.RawMessage `json:"document"`
		Password string          `json:"password"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxEncryptedImportBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, categoryDocumentInvalid)
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, categoryDocumentInvalid)
		return
	}
	if len(request.Document) == 0 {
		writeError(w, http.StatusBadRequest, categoryDocumentInvalid)
		return
	}
	envelope, ok := webdavsync.TryParseEncryptedEnvelope(request.Document)
	if !ok {
		writeError(w, http.StatusUnprocessableEntity, categoryDocumentUnsupported)
		return
	}
	if strings.TrimSpace(request.Password) == "" {
		writeError(w, http.StatusBadRequest, categoryUnlockRequired)
		return
	}
	plaintext, err := webdavsync.DecryptEnvelope(envelope, request.Password)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, categoryDecryptFailed)
		return
	}
	if len(plaintext) > exchange.MaxBodyBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "body_too_large")
		return
	}
	result, err := h.service.Import(r.Context(), plaintext)
	if err != nil {
		writeExchangeImportError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
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

// writeExchangeImportError maps import failures to categories that describe the
// backup document, not the connection.
func writeExchangeImportError(w http.ResponseWriter, err error) {
	var exchangeErr *exchange.Error
	if !errors.As(err, &exchangeErr) {
		writeError(w, http.StatusInternalServerError, string(exchange.ErrorInternal))
		return
	}
	switch exchangeErr.Kind {
	case exchange.ErrorValidation:
		writeError(w, http.StatusBadRequest, categoryDocumentInvalid)
	case exchange.ErrorUnsupported:
		writeError(w, http.StatusUnprocessableEntity, categoryDocumentUnsupported)
	case exchange.ErrorNoEntries:
		writeError(w, http.StatusUnprocessableEntity, categoryDocumentEmpty)
	case exchange.ErrorConflict:
		writeError(w, http.StatusConflict, categoryDocumentConflict)
	default:
		writeError(w, http.StatusInternalServerError, string(exchange.ErrorInternal))
	}
}

package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/lan/meta-gateway/internal/webdavsync"
)

type WebDAVHandler struct {
	service *webdavsync.Service
}

func NewWebDAVHandler(service *webdavsync.Service) *WebDAVHandler {
	return &WebDAVHandler{service: service}
}

func (h *WebDAVHandler) Register(r chi.Router) {
	r.Get("/webdav/status", h.status)
	r.Get("/webdav/settings", h.getSettings)
	r.Put("/webdav/settings", h.putSettings)
	r.Post("/webdav/test", h.test)
	r.Post("/webdav/sync", h.sync)
}

func (h *WebDAVHandler) status(w http.ResponseWriter, _ *http.Request) {
	if h.service == nil {
		writeJSON(w, http.StatusOK, webdavsync.StatusView{})
		return
	}
	writeJSON(w, http.StatusOK, h.service.Status())
}

func (h *WebDAVHandler) getSettings(w http.ResponseWriter, _ *http.Request) {
	if h.service == nil {
		writeJSON(w, http.StatusOK, webdavsync.SettingsView{Source: "none", CronExpr: "0 */6 * * *"})
		return
	}
	view, err := h.service.SettingsView()
	if err != nil {
		writeWebDAVError(w, err, nil)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *WebDAVHandler) putSettings(w http.ResponseWriter, r *http.Request) {
	if h.service == nil {
		writeError(w, http.StatusServiceUnavailable, webdavsync.CategoryInternal)
		return
	}
	var update webdavsync.SettingsUpdate
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&update); err != nil {
		writeError(w, http.StatusBadRequest, webdavsync.CategoryValidation)
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, webdavsync.CategoryValidation)
		return
	}
	view, err := h.service.UpdateSettings(update)
	if err != nil {
		writeWebDAVError(w, err, nil)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *WebDAVHandler) test(w http.ResponseWriter, r *http.Request) {
	if h.service == nil {
		writeError(w, http.StatusServiceUnavailable, webdavsync.CategoryConfigIncomplete)
		return
	}
	direction, handled := readWebDAVDirection(w, r)
	if handled {
		return
	}
	result, err := h.service.TestConnection(r.Context(), direction)
	if err != nil {
		writeWebDAVError(w, err, result)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *WebDAVHandler) sync(w http.ResponseWriter, r *http.Request) {
	if h.service == nil {
		writeError(w, http.StatusServiceUnavailable, webdavsync.CategoryConfigIncomplete)
		return
	}
	mode := webdavsync.SyncModeIncremental
	direction := webdavsync.DirectionDownload
	// Always attempt to parse the JSON body for backward compatibility.
	// Empty bodies (including chunked with no content) default to the download
	// direction in incremental mode.
	if r.Body != nil {
		var body struct {
			Mode      string `json:"mode"`
			Direction string `json:"direction"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); errors.Is(err, io.EOF) {
			// No body provided — stay with the defaults.
		} else if err != nil {
			writeError(w, http.StatusBadRequest, webdavsync.CategoryValidation)
			return
		} else {
			var trailing any
			if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
				writeError(w, http.StatusBadRequest, webdavsync.CategoryValidation)
				return
			}
			if m := strings.TrimSpace(body.Mode); m != "" {
				mode = strings.ToLower(m)
			}
			direction = strings.ToLower(strings.TrimSpace(body.Direction))
		}
	}
	if mode != webdavsync.SyncModeIncremental && mode != webdavsync.SyncModeReplace {
		writeError(w, http.StatusBadRequest, webdavsync.CategoryValidation)
		return
	}
	if direction != webdavsync.DirectionDownload && direction != webdavsync.DirectionUpload {
		writeError(w, http.StatusBadRequest, webdavsync.CategoryValidation)
		return
	}
	result, err := h.service.Sync(r.Context(), webdavsync.SourceManual, mode, direction)
	if err != nil {
		writeWebDAVError(w, err, result)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// readWebDAVDirection parses the optional {direction} body used by the test
// endpoint. A missing or empty body defaults to the download direction; a
// malformed payload writes a 400 and reports handled.
func readWebDAVDirection(w http.ResponseWriter, r *http.Request) (string, bool) {
	if r.Body == nil {
		return "", false
	}
	var body struct {
		Direction string `json:"direction"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); errors.Is(err, io.EOF) {
		return "", false
	} else if err != nil {
		writeError(w, http.StatusBadRequest, webdavsync.CategoryValidation)
		return "", true
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, webdavsync.CategoryValidation)
		return "", true
	}
	return strings.ToLower(strings.TrimSpace(body.Direction)), false
}

func writeWebDAVError(w http.ResponseWriter, err error, result *webdavsync.SyncResult) {
	var syncErr webdavsync.Error
	if !errors.As(err, &syncErr) {
		writeError(w, http.StatusInternalServerError, webdavsync.CategoryInternal)
		return
	}
	status := http.StatusBadRequest
	switch syncErr.Category {
	case webdavsync.CategoryConfigIncomplete:
		status = http.StatusServiceUnavailable
	case webdavsync.CategoryAuthFailed:
		status = http.StatusBadGateway
	case webdavsync.CategoryNotFound:
		status = http.StatusBadGateway
	case webdavsync.CategoryOutboundBlocked:
		status = http.StatusBadGateway
	case webdavsync.CategoryUpstream:
		status = http.StatusBadGateway
	case webdavsync.CategoryTooLarge:
		status = http.StatusRequestEntityTooLarge
	case webdavsync.CategoryBusy:
		status = http.StatusConflict
	case webdavsync.CategoryDecryptFailed, webdavsync.CategoryInvalidBackup:
		status = http.StatusUnprocessableEntity
	case webdavsync.CategoryImportFailed:
		status = http.StatusConflict
	case webdavsync.CategoryValidation:
		status = http.StatusBadRequest
	case webdavsync.CategoryInternal:
		status = http.StatusInternalServerError
	}
	if result != nil {
		writeJSON(w, status, result)
		return
	}
	writeError(w, status, syncErr.Category)
}

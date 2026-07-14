package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/lan/meta-gateway/internal/proxy"
	"github.com/lan/meta-gateway/internal/relay"
	"github.com/lan/meta-gateway/internal/routing"
	"github.com/lan/meta-gateway/internal/store"
)

type ChatProxy interface {
	ChatCompletions(ctx context.Context, req proxy.Request) *relay.Result
}

// RelayHandler serves public /v1/* endpoints.
type RelayHandler struct {
	db    *store.DB
	proxy ChatProxy
}

func NewRelayHandler(db *store.DB, service ChatProxy) *RelayHandler {
	return &RelayHandler{db: db, proxy: service}
}

func (h *RelayHandler) Register(r chi.Router) {
	r.Get("/models", h.getModels)
	r.Post("/chat/completions", h.chatCompletions)
}

func (h *RelayHandler) getModels(w http.ResponseWriter, r *http.Request) {
	seen := map[string]struct{}{}
	var models []map[string]interface{}
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		models = append(models, map[string]interface{}{"id": id, "object": "model"})
	}

	routes, err := h.db.Route.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list models")
		return
	}
	for _, route := range routes {
		if route.Enabled {
			add(route.ModelPattern)
		}
	}
	if len(models) == 0 {
		channels, err := h.db.Channel.ListEnabled()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list models")
			return
		}
		for _, channel := range channels {
			for _, model := range strings.Split(channel.ModelsCSV, ",") {
				add(model)
			}
		}
	}
	if models == nil {
		models = []map[string]interface{}{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"object": "list", "data": models})
}

type chatCompletionsRequest struct {
	Model  string `json:"model"`
	Stream bool   `json:"stream"`
}

func (h *RelayHandler) chatCompletions(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 10*1024*1024))
	if err != nil {
		writeError(w, http.StatusBadRequest, "body too large")
		return
	}
	defer r.Body.Close()
	var request chatCompletionsRequest
	if err := json.Unmarshal(body, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if request.Model == "" {
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}

	requestID, _ := r.Context().Value(chimw.RequestIDKey).(string)
	result := h.proxy.ChatCompletions(r.Context(), proxy.Request{
		RequestID: requestID,
		Model:     request.Model,
		Body:      body,
		Stream:    request.Stream,
	})
	if result.Err != nil {
		switch {
		case errors.Is(result.Err, routing.ErrRouteNotFound), errors.Is(result.Err, routing.ErrNoEligible):
			writeError(w, http.StatusNotFound, "no eligible channel for this model")
		case errors.Is(result.Err, proxy.ErrCredential):
			writeError(w, http.StatusInternalServerError, "failed to resolve upstream credentials")
		case errors.Is(result.Err, context.Canceled), errors.Is(result.Err, context.DeadlineExceeded):
			return
		default:
			writeError(w, http.StatusBadGateway, "upstream request failed")
		}
		return
	}
	defer result.Body.Close()
	copyResponseHeaders(w.Header(), result.Header, request.Stream)
	w.WriteHeader(result.StatusCode)
	if _, err := io.Copy(w, result.Body); err != nil {
		log.Printf("relay: copy upstream response request_id=%s: %v", requestID, err)
	}
}

func copyResponseHeaders(dst, src http.Header, stream bool) {
	for _, key := range []string{"Content-Type", "Cache-Control", "Content-Encoding", "Retry-After", "X-Request-Id"} {
		if value := src.Get(key); value != "" {
			dst.Set(key, value)
		}
	}
	if stream {
		dst.Set("Content-Type", "text/event-stream")
		dst.Set("Cache-Control", "no-cache")
		dst.Set("Connection", "keep-alive")
	} else if dst.Get("Content-Type") == "" {
		dst.Set("Content-Type", "application/json")
	}
}

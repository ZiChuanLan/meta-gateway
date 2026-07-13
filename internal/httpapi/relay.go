package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/relay"
	"github.com/lan/meta-gateway/internal/store"
)

// RelayHandler serves public /v1/* endpoints.
type RelayHandler struct {
	db    *store.DB
	relay *relay.Relay
	enc   *crypto.Encrypter
}

func NewRelayHandler(db *store.DB, r *relay.Relay, enc *crypto.Encrypter) *RelayHandler {
	return &RelayHandler{db: db, relay: r, enc: enc}
}

func (h *RelayHandler) Register(r chi.Router) {
	r.Get("/models", h.getModels)
	r.Post("/chat/completions", h.chatCompletions)
}

// getModels returns models from enabled routes, falling back to channel models_csv.
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
		models = append(models, map[string]interface{}{
			"id":     id,
			"object": "model",
		})
	}

	routes, err := h.db.Route.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, rt := range routes {
		if rt.Enabled {
			add(rt.ModelPattern)
		}
	}

	if len(models) == 0 {
		channels, err := h.db.Channel.ListEnabled()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		for _, ch := range channels {
			for _, part := range strings.Split(ch.ModelsCSV, ",") {
				add(part)
			}
		}
	}

	if models == nil {
		models = []map[string]interface{}{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"object": "list",
		"data":   models,
	})
}

// chatCompletionsRequest mirrors the OpenAI chat completions request.
type chatCompletionsRequest struct {
	Model    string          `json:"model"`
	Messages json.RawMessage `json:"messages"`
	Stream   bool            `json:"stream"`
	// Passthrough additional fields
	Temperature *float32               `json:"temperature,omitempty"`
	MaxTokens   *int                   `json:"max_tokens,omitempty"`
	TopP        *float32               `json:"top_p,omitempty"`
	N           *int                   `json:"n,omitempty"`
	Stop        interface{}            `json:"stop,omitempty"`
	Extra       map[string]interface{} `json:"-"` // collected from remaining keys
}

// chatCompletions proxies a chat completion request through the single available channel.
func (h *RelayHandler) chatCompletions(w http.ResponseWriter, r *http.Request) {
	// Read the raw body first so we can forward it.
	bodyBytes, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 10*1024*1024))
	if err != nil {
		writeError(w, http.StatusBadRequest, "body too large")
		return
	}
	defer r.Body.Close()

	// Parse minimally to check stream field.
	var chatReq chatCompletionsRequest
	if err := json.Unmarshal(bodyBytes, &chatReq); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if chatReq.Model == "" {
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}

	// Find the enabled channel (single-channel mode: first enabled route member or default).
	route, err := h.db.Route.GetByModel(chatReq.Model)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if route == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("no route for model: %s", chatReq.Model))
		return
	}

	members, err := h.db.RouteMember.ListByRoute(route.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Find the first enabled member.
	var member *domain.RouteMember
	for _, m := range members {
		if m.Enabled {
			member = &m
			break
		}
	}
	if member == nil {
		writeError(w, http.StatusNotFound, "no enabled channel for this model")
		return
	}

	// Get the channel to find upstream URL and credential.
	ch, err := h.db.Channel.GetByID(member.ChannelID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if ch == nil || ch.Status != domain.StatusEnabled {
		writeError(w, http.StatusNotFound, "channel not found or disabled")
		return
	}

	apiKey, err := h.resolveAPIKey(ch)
	if err != nil {
		log.Printf("relay: resolve key for channel %d: %v", ch.ID, err)
		writeError(w, http.StatusInternalServerError, "failed to resolve upstream credentials")
		return
	}

	upstreamURL := strings.TrimRight(ch.BaseURL, "/") + "/v1/chat/completions"

	proxyStart := time.Now()
	result := h.relay.ChatCompletions(upstreamURL, apiKey, bodyBytes, chatReq.Stream)
	latencyMs := int(time.Since(proxyStart).Milliseconds())

	// Write proxy log (without secrets). Prefer real upstream status when available.
	status := result.StatusCode
	if result.Err != nil {
		status = http.StatusBadGateway
	} else if status == 0 {
		status = http.StatusOK
	}
	reqID, _ := r.Context().Value(chimw.RequestIDKey).(string)
	pl := &domain.ProxyLog{
		RequestID: reqID,
		ChannelID: ch.ID,
		Model:     chatReq.Model,
		Status:    status,
		LatencyMs: latencyMs,
	}
	if result.Err != nil {
		pl.ErrorBrief = result.Err.Error()
	}
	// Best-effort log write
	h.db.ProxyLog.Insert(pl)

	if result.Err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("upstream error: %v", result.Err))
		return
	}
	defer result.Body.Close()

	if chatReq.Stream {
		// SSE passthrough
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(result.StatusCode)
		io.Copy(w, result.Body)
	} else {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(result.StatusCode)
		io.Copy(w, result.Body)
	}
}

func (h *RelayHandler) resolveAPIKey(ch *domain.Channel) (string, error) {
	if ch.CredentialID == nil {
		return "", fmt.Errorf("no credential attached to channel")
	}
	cred, err := h.db.Credential.GetByID(*ch.CredentialID)
	if err != nil {
		return "", fmt.Errorf("credential lookup: %w", err)
	}
	if cred == nil {
		return "", fmt.Errorf("credential not found")
	}
	// Decrypt the secret
	plaintext, err := h.enc.Decrypt(string(cred.SecretEnc))
	if err != nil {
		return "", fmt.Errorf("credential decrypt: %w", err)
	}
	return string(plaintext), nil
}

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/lan/meta-gateway/internal/auth"
	"github.com/lan/meta-gateway/internal/proxy"
	"github.com/lan/meta-gateway/internal/relay"
	"github.com/lan/meta-gateway/internal/routing"
	"github.com/lan/meta-gateway/internal/store"
	"github.com/lan/meta-gateway/internal/usage"
)

type RelayProxy interface {
	Forward(ctx context.Context, req proxy.Request) *relay.Result
	ForwardWithMeta(ctx context.Context, req proxy.Request) (*relay.Result, *proxy.AttemptMeta)
	ChatCompletions(ctx context.Context, req proxy.Request) *relay.Result
	ChatCompletionsWithMeta(ctx context.Context, req proxy.Request) (*relay.Result, *proxy.AttemptMeta)
	RecordUsage(req proxy.Request, channelID int64, status int, tokens struct {
		PromptTokens     int
		CompletionTokens int
		TotalTokens      int
	})
}

// RelayHandler serves public /v1/* endpoints.
type RelayHandler struct {
	db    *store.DB
	proxy RelayProxy
}

func NewRelayHandler(db *store.DB, service RelayProxy) *RelayHandler {
	return &RelayHandler{db: db, proxy: service}
}

func (h *RelayHandler) Register(r chi.Router) {
	r.Get("/models", h.getModels)
	r.Post("/chat/completions", h.chatCompletions)
	r.Post("/completions", h.completions)
	r.Post("/embeddings", h.embeddings)
	r.Post("/responses", h.responses)
	r.Post("/messages", h.messages)
	// OpenAI-compatible passthrough surfaces (JSON body + model routing).
	r.Post("/images/generations", h.imagesGenerations)
	r.Post("/images/edits", h.imagesEdits)
	r.Post("/images/variations", h.imagesVariations)
	r.Post("/audio/speech", h.audioSpeech)
	r.Post("/audio/transcriptions", h.audioTranscriptions)
	r.Post("/audio/translations", h.audioTranslations)
	r.Post("/moderations", h.moderations)
}

func (h *RelayHandler) getModels(w http.ResponseWriter, r *http.Request) {
	if !auth.HasScope(auth.DownstreamScopes(r), auth.ScopeModels) {
		writeError(w, http.StatusForbidden, "insufficient scope")
		return
	}
	if !h.ensureQuota(w, r) {
		return
	}
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

type modelOnlyRequest struct {
	Model string `json:"model"`
}

func (h *RelayHandler) chatCompletions(w http.ResponseWriter, r *http.Request) {
	h.forwardModelRequest(w, r, "chat/completions", true, auth.ScopeChat)
}

func (h *RelayHandler) completions(w http.ResponseWriter, r *http.Request) {
	h.forwardModelRequest(w, r, "completions", true, auth.ScopeCompletions)
}

func (h *RelayHandler) embeddings(w http.ResponseWriter, r *http.Request) {
	h.forwardModelRequest(w, r, "embeddings", false, auth.ScopeEmbeddings)
}

func (h *RelayHandler) responses(w http.ResponseWriter, r *http.Request) {
	h.forwardModelRequest(w, r, "responses", true, auth.ScopeResponses)
}

// messages is the Anthropic Messages API surface (native clients).
func (h *RelayHandler) messages(w http.ResponseWriter, r *http.Request) {
	h.forwardModelRequest(w, r, "messages", true, auth.ScopeMessages)
}

func (h *RelayHandler) imagesGenerations(w http.ResponseWriter, r *http.Request) {
	h.forwardPassthrough(w, r, "images/generations", auth.ScopeImages, false, 20<<20)
}

func (h *RelayHandler) imagesEdits(w http.ResponseWriter, r *http.Request) {
	h.forwardPassthrough(w, r, "images/edits", auth.ScopeImages, true, 30<<20)
}

func (h *RelayHandler) imagesVariations(w http.ResponseWriter, r *http.Request) {
	h.forwardPassthrough(w, r, "images/variations", auth.ScopeImages, true, 30<<20)
}

func (h *RelayHandler) audioSpeech(w http.ResponseWriter, r *http.Request) {
	h.forwardPassthrough(w, r, "audio/speech", auth.ScopeAudio, false, 10<<20)
}

func (h *RelayHandler) audioTranscriptions(w http.ResponseWriter, r *http.Request) {
	h.forwardPassthrough(w, r, "audio/transcriptions", auth.ScopeAudio, true, 30<<20)
}

func (h *RelayHandler) audioTranslations(w http.ResponseWriter, r *http.Request) {
	h.forwardPassthrough(w, r, "audio/translations", auth.ScopeAudio, true, 30<<20)
}

func (h *RelayHandler) moderations(w http.ResponseWriter, r *http.Request) {
	h.forwardPassthrough(w, r, "moderations", auth.ScopeModerations, false, 2<<20)
}

// forwardPassthrough relays OpenAI-compatible paths that may be JSON or multipart.
// model may come from JSON "model" or multipart form field "model".
func (h *RelayHandler) forwardPassthrough(w http.ResponseWriter, r *http.Request, openAIPath, requiredScope string, allowMultipart bool, maxBytes int64) {
	if !auth.HasScope(auth.DownstreamScopes(r), requiredScope) {
		writeError(w, http.StatusForbidden, "insufficient scope")
		return
	}
	if !h.ensureQuota(w, r) {
		return
	}
	contentType := r.Header.Get("Content-Type")
	isMultipart := allowMultipart && strings.Contains(strings.ToLower(contentType), "multipart/form-data")

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "body too large")
		return
	}
	defer r.Body.Close()

	modelName := ""
	stream := false
	if isMultipart {
		modelName = extractMultipartModel(body, contentType)
		// Fallback: some clients still put model only in query.
		if modelName == "" {
			modelName = strings.TrimSpace(r.URL.Query().Get("model"))
		}
	} else {
		var request struct {
			Model  string `json:"model"`
			Stream bool   `json:"stream"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		modelName = request.Model
		// Only speech/json endpoints may stream; still pass flag through.
		stream = request.Stream
	}
	if strings.TrimSpace(modelName) == "" {
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}

	requestID, _ := r.Context().Value(chimw.RequestIDKey).(string)
	keyID, _ := auth.DownstreamKeyID(r)
	proxyReq := proxy.Request{
		RequestID:       requestID,
		Model:           modelName,
		Body:            body,
		Stream:          stream,
		Method:          http.MethodPost,
		OpenAIPath:      openAIPath,
		DownstreamKeyID: keyID,
		ContentType:     contentType,
	}
	result, meta := h.proxy.ForwardWithMeta(r.Context(), proxyReq)
	// Binary / non-JSON responses: do not force SSE content-type unless stream.
	forceSSE := stream
	writeUpstreamResult(w, requestID, result, forceSSE, func(tokens usage.Tokens, status int) {
		channelID := int64(0)
		if meta != nil {
			channelID = meta.ChannelID
		}
		h.proxy.RecordUsage(proxyReq, channelID, status, struct {
			PromptTokens     int
			CompletionTokens int
			TotalTokens      int
		}{
			PromptTokens:     tokens.PromptTokens,
			CompletionTokens: tokens.CompletionTokens,
			TotalTokens:      tokens.TotalTokens,
		})
	})
}

// extractMultipartModel does a best-effort scan for name="model" in multipart bodies
// without fully re-parsing the form (body must be forwarded intact).
func extractMultipartModel(body []byte, contentType string) string {
	// Look for Content-Disposition: form-data; name="model" then the next non-empty line after headers.
	marker := []byte(`name="model"`)
	idx := bytes.Index(body, marker)
	if idx < 0 {
		marker = []byte(`name=model`)
		idx = bytes.Index(body, marker)
	}
	if idx < 0 {
		return ""
	}
	rest := body[idx:]
	// Skip to end of header block (blank line).
	sep := bytes.Index(rest, []byte("\r\n\r\n"))
	headerEndLen := 4
	if sep < 0 {
		sep = bytes.Index(rest, []byte("\n\n"))
		headerEndLen = 2
	}
	if sep < 0 {
		return ""
	}
	valueStart := sep + headerEndLen
	if valueStart >= len(rest) {
		return ""
	}
	value := rest[valueStart:]
	// Value ends at next boundary line starting with --
	end := bytes.Index(value, []byte("\r\n--"))
	if end < 0 {
		end = bytes.Index(value, []byte("\n--"))
	}
	if end >= 0 {
		value = value[:end]
	}
	return strings.TrimSpace(string(value))
}

func (h *RelayHandler) ensureQuota(w http.ResponseWriter, r *http.Request) bool {
	keyID, ok := auth.DownstreamKeyID(r)
	if !ok || keyID <= 0 || h.db == nil || h.db.DownstreamKey == nil {
		return true
	}
	key, err := h.db.DownstreamKey.GetByID(keyID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load key quota")
		return false
	}
	if store.QuotaExceeded(key) {
		writeError(w, http.StatusPaymentRequired, "token quota exceeded")
		return false
	}
	return true
}

func (h *RelayHandler) forwardModelRequest(w http.ResponseWriter, r *http.Request, openAIPath string, allowStream bool, requiredScope string) {
	if !auth.HasScope(auth.DownstreamScopes(r), requiredScope) {
		writeError(w, http.StatusForbidden, "insufficient scope")
		return
	}
	if !h.ensureQuota(w, r) {
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 10*1024*1024))
	if err != nil {
		writeError(w, http.StatusBadRequest, "body too large")
		return
	}
	defer r.Body.Close()

	modelName := ""
	stream := false
	if allowStream {
		var request chatCompletionsRequest
		if err := json.Unmarshal(body, &request); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		modelName = request.Model
		stream = request.Stream
	} else {
		var request modelOnlyRequest
		if err := json.Unmarshal(body, &request); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		modelName = request.Model
	}
	if strings.TrimSpace(modelName) == "" {
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}
	if stream && (openAIPath == "chat/completions" || openAIPath == "completions") {
		body = ensureStreamUsageOption(body)
	}

	requestID, _ := r.Context().Value(chimw.RequestIDKey).(string)
	keyID, _ := auth.DownstreamKeyID(r)
	proxyReq := proxy.Request{
		RequestID:       requestID,
		Model:           modelName,
		Body:            body,
		Stream:          stream,
		Method:          http.MethodPost,
		OpenAIPath:      openAIPath,
		DownstreamKeyID: keyID,
	}
	result, meta := h.proxy.ForwardWithMeta(r.Context(), proxyReq)
	writeUpstreamResult(w, requestID, result, stream, func(tokens usage.Tokens, status int) {
		channelID := int64(0)
		if meta != nil {
			channelID = meta.ChannelID
		}
		h.proxy.RecordUsage(proxyReq, channelID, status, struct {
			PromptTokens     int
			CompletionTokens int
			TotalTokens      int
		}{
			PromptTokens:     tokens.PromptTokens,
			CompletionTokens: tokens.CompletionTokens,
			TotalTokens:      tokens.TotalTokens,
		})
	})
}

// ensureStreamUsageOption asks OpenAI-compatible upstreams to emit a final usage
// chunk on SSE streams so the gateway can meter tokens without buffering the full reply.
func ensureStreamUsageOption(body []byte) []byte {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return body
	}
	raw, ok := payload["stream_options"]
	if !ok || raw == nil {
		payload["stream_options"] = map[string]any{"include_usage": true}
	} else if options, ok := raw.(map[string]any); ok {
		if _, exists := options["include_usage"]; !exists {
			options["include_usage"] = true
			payload["stream_options"] = options
		}
	} else {
		return body
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return encoded
}

func writeUpstreamResult(w http.ResponseWriter, requestID string, result *relay.Result, stream bool, onUsage func(usage.Tokens, int)) {
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
	copyResponseHeaders(w.Header(), result.Header, stream)
	w.WriteHeader(result.StatusCode)
	tee := usage.NewTee(io.NopCloser(result.Body), stream)
	if err := copyUpstreamBody(w, tee, stream); err != nil {
		log.Printf("relay: copy upstream response request_id=%s: %v", requestID, err)
	}
	_ = tee.Close()
	if onUsage != nil {
		onUsage(tee.Tokens(), result.StatusCode)
	}
}

// copyUpstreamBody streams the upstream body to the client. For SSE, it flushes
// after every successful write so intermediaries and ResponseControllers do not
// hold chunks until the stream ends.
func copyUpstreamBody(w http.ResponseWriter, body io.Reader, stream bool) error {
	if !stream {
		_, err := io.Copy(w, body)
		return err
	}
	flusher, canFlush := w.(http.Flusher)
	buffer := make([]byte, 32*1024)
	for {
		bytesRead, readErr := body.Read(buffer)
		if bytesRead > 0 {
			if _, writeErr := w.Write(buffer[:bytesRead]); writeErr != nil {
				return writeErr
			}
			if canFlush {
				flusher.Flush()
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			return readErr
		}
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
		dst.Set("X-Accel-Buffering", "no")
	} else if dst.Get("Content-Type") == "" {
		dst.Set("Content-Type", "application/json")
	}
}

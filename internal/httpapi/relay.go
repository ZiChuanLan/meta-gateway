package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/lan/meta-gateway/internal/ratelimit"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/lan/meta-gateway/internal/adapters"
	"github.com/lan/meta-gateway/internal/auth"
	"github.com/lan/meta-gateway/internal/proxy"
	"github.com/lan/meta-gateway/internal/relay"
	"github.com/lan/meta-gateway/internal/routing"
	"github.com/lan/meta-gateway/internal/store"
	"github.com/lan/meta-gateway/internal/usage"
)

type RelayProxy interface {
	ForwardWithMeta(ctx context.Context, req proxy.Request) (*relay.Result, *proxy.AttemptMeta)
	ChatCompletions(ctx context.Context, req proxy.Request) *relay.Result
	ChatCompletionsWithMeta(ctx context.Context, req proxy.Request) (*relay.Result, *proxy.AttemptMeta)
	RecordUsage(req proxy.Request, channelID int64, status int, tokens usage.Tokens)
	RecordStreamFailure(memberID int64)
}

// RelayHandler serves public /v1/* endpoints.
type RelayHandler struct {
	db    *store.DB
	proxy RelayProxy
	// modelLimiter is nil when per-model relay limiting is disabled.
	modelLimiter *ratelimit.Limiter
	// groupLimiter enforces per-tenant-group rate limits (nil disables).
	groupLimiter *groupRateLimiter
	// modelsCache serves /v1/models from memory (invalidated on admin writes).
	modelsCache *modelsCache
}

func NewRelayHandler(db *store.DB, service RelayProxy, modelLimiter *ratelimit.Limiter, groupLimiter *groupRateLimiter, modelsCache *modelsCache) *RelayHandler {
	return &RelayHandler{db: db, proxy: service, modelLimiter: modelLimiter, groupLimiter: groupLimiter, modelsCache: modelsCache}
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
	modelFilter := auth.DownstreamModelFilter(r)
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		if modelFilter != nil && !modelFilter.Allows(id) {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		models = append(models, map[string]interface{}{"id": id, "object": "model"})
	}

	// Serve from the shared cache when warm; only recompute from the DB on a
	// cold/expired entry (admin writes invalidate via h.modelsCache).
	raw, ok := h.modelsCache.Get()
	if !ok {
		raw = h.computeRawModels()
		h.modelsCache.Put(raw)
	}
	for _, id := range raw {
		add(id)
	}
	if models == nil {
		models = []map[string]interface{}{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"object": "list", "data": models})
}

// computeRawModels builds the unfiltered model id list from enabled routes,
// falling back to channel model lists when no route exposes any model.
func (h *RelayHandler) computeRawModels() []string {
	seen := map[string]struct{}{}
	var raw []string
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		raw = append(raw, id)
	}
	routes, err := h.db.Route.List()
	if err == nil {
		for _, route := range routes {
			if route.Enabled {
				add(route.ModelPattern)
			}
		}
	}
	if len(raw) == 0 {
		channels, err := h.db.Channel.ListEnabled()
		if err == nil {
			for _, channel := range channels {
				for _, model := range strings.Split(channel.ModelsCSV, ",") {
					add(model)
				}
			}
		}
	}
	return raw
}

type chatCompletionsRequest struct {
	Model           string `json:"model"`
	Stream          bool   `json:"stream"`
	ReasoningEffort string `json:"reasoning_effort"`
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

// messages is the Anthropic Messages API surface (native clients). The
// gateway serves any channel here: Anthropic-native channels pass through
// verbatim; other channels translate the request/response (see proxy).
func (h *RelayHandler) messages(w http.ResponseWriter, r *http.Request) {
	h.forwardModelRequest(w, r, "messages", true, auth.ScopeMessages, "anthropic")
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
	if !h.ensureGroupRate(w, r) {
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
	reasoningEffort := ""
	if isMultipart {
		modelName = extractMultipartModel(body, contentType)
		// Fallback: some clients still put model only in query.
		if modelName == "" {
			modelName = strings.TrimSpace(r.URL.Query().Get("model"))
		}
	} else {
		var request struct {
			Model           string `json:"model"`
			Stream          bool   `json:"stream"`
			ReasoningEffort string `json:"reasoning_effort"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		modelName = request.Model
		// Only speech/json endpoints may stream; still pass flag through.
		stream = request.Stream
		reasoningEffort = request.ReasoningEffort
	}
	if strings.TrimSpace(modelName) == "" {
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}
	if filter := auth.DownstreamModelFilter(r); filter != nil && !filter.Allows(modelName) {
		writeError(w, http.StatusForbidden, "model is not allowed for this token")
		return
	}
	if !h.checkModelRate(w, r, modelName) {
		return
	}

	requestID, _ := r.Context().Value(chimw.RequestIDKey).(string)
	keyID, _ := auth.DownstreamKeyID(r)
	clientFamily := ClientFamilyOf(r)
	proxyReq := proxy.Request{
		RequestID:       requestID,
		Model:           modelName,
		Body:            body,
		Stream:          stream,
		Method:          http.MethodPost,
		OpenAIPath:      openAIPath,
		DownstreamKeyID: keyID,
		ContentType:     contentType,
		SessionKey:      r.Header.Get("X-Meta-Session-Id"),
		ReasoningEffort: reasoningEffort,
	}
	result, meta := h.proxy.ForwardWithMeta(r.Context(), proxyReq)
	// Binary / non-JSON responses: do not force SSE content-type unless stream.
	forceSSE := stream
	writeUpstreamResult(
		w, requestID, result, forceSSE,
		func(tokens usage.Tokens, status int, firstByteMs int) {
			channelID := int64(0)
			if meta != nil {
				channelID = meta.ChannelID
			}
			h.proxy.RecordUsage(proxyReq, channelID, status, tokens)
			if h.db != nil && h.db.ProxyLog != nil && requestID != "" {
				if err := h.db.ProxyLog.UpdateMetaByRequestID(requestID, firstByteMs, clientFamily); err != nil {
					log.Printf("relay: update log meta request_id=%s: %v", requestID, err)
				}
			}
		},
		h.streamErrorCallback(r, meta),
	)
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
	// The authenticated key snapshot rides in the request context, so quota
	// checks reuse the auth lookup instead of a second DB read.
	key := auth.DownstreamKey(r)
	if key == nil || key.ID <= 0 || h.db == nil || h.db.DownstreamKey == nil {
		return true
	}
	if store.QuotaExceeded(key) {
		writeError(w, http.StatusPaymentRequired, "token quota exceeded")
		return false
	}
	// Tenant group quota: enforced on top of the key quota. Absent groups are
	// unlimited (Group.Get returns a zero-quota group for unknown names).
	if groupName := key.GroupName; groupName != "" && h.db.Group != nil {
		group, err := h.db.Group.Get(groupName)
		if err == nil && group != nil && group.QuotaTotalTokens > 0 && group.QuotaUsedTokens >= group.QuotaTotalTokens {
			writeError(w, http.StatusPaymentRequired, "group quota exceeded")
			return false
		}
	}
	return true
}

func (h *RelayHandler) forwardModelRequest(w http.ResponseWriter, r *http.Request, openAIPath string, allowStream bool, requiredScope string, downstreamProtocol ...string) {
	if !auth.HasScope(auth.DownstreamScopes(r), requiredScope) {
		writeError(w, http.StatusForbidden, "insufficient scope")
		return
	}
	if !h.ensureQuota(w, r) {
		return
	}
	if !h.ensureGroupRate(w, r) {
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
	reasoningEffort := ""
	if allowStream {
		var request chatCompletionsRequest
		if err := json.Unmarshal(body, &request); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		modelName = request.Model
		stream = request.Stream
		reasoningEffort = request.ReasoningEffort
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
	if filter := auth.DownstreamModelFilter(r); filter != nil && !filter.Allows(modelName) {
		writeError(w, http.StatusForbidden, "model is not allowed for this token")
		return
	}
	if !h.checkModelRate(w, r, modelName) {
		return
	}
	if stream && (openAIPath == "chat/completions" || openAIPath == "completions") {
		body = ensureStreamUsageOption(body)
	}

	requestID, _ := r.Context().Value(chimw.RequestIDKey).(string)
	keyID, _ := auth.DownstreamKeyID(r)
	downstream := "openai"
	if len(downstreamProtocol) > 0 && downstreamProtocol[0] != "" {
		downstream = downstreamProtocol[0]
	}
	clientFamily := ClientFamilyOf(r)
	proxyReq := proxy.Request{
		RequestID:          requestID,
		Model:              modelName,
		Body:               body,
		Stream:             stream,
		Method:             http.MethodPost,
		OpenAIPath:         openAIPath,
		DownstreamKeyID:    keyID,
		DownstreamProtocol: downstream,
		SessionKey:         r.Header.Get("X-Meta-Session-Id"),
		ReasoningEffort:    reasoningEffort,
	}
	result, meta := h.proxy.ForwardWithMeta(r.Context(), proxyReq)
	writeUpstreamResult(
		w, requestID, result, stream,
		func(tokens usage.Tokens, status int, firstByteMs int) {
			channelID := int64(0)
			if meta != nil {
				channelID = meta.ChannelID
			}
			h.proxy.RecordUsage(proxyReq, channelID, status, tokens)
			if h.db != nil && h.db.ProxyLog != nil && requestID != "" {
				if err := h.db.ProxyLog.UpdateMetaByRequestID(requestID, firstByteMs, clientFamily); err != nil {
					log.Printf("relay: update log meta request_id=%s: %v", requestID, err)
				}
			}
		},
		h.streamErrorCallback(r, meta),
	)
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

// ensureGroupRate enforces the tenant group's rate limit (if any) on relay
// requests. Returns false after writing a 429 with Retry-After when the group
// bucket is exhausted.
func (h *RelayHandler) ensureGroupRate(w http.ResponseWriter, r *http.Request) bool {
	if h.groupLimiter == nil || h.db == nil || h.db.Group == nil {
		return true
	}
	key := auth.DownstreamKey(r)
	if key == nil || key.GroupName == "" {
		return true
	}
	group, err := h.db.Group.Get(key.GroupName)
	if err != nil || group == nil {
		return true // never fail closed on a group lookup error
	}
	allowed, wait := h.groupLimiter.Allow(group)
	if allowed {
		return true
	}
	w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())))
	writeError(w, http.StatusTooManyRequests, "group rate limit exceeded")
	return false
}

// streamErrorCallback returns a callback that records a failure for the member
// that served a stream when the upstream connection breaks mid-stream. Client
// disconnects (context canceled) are not treated as upstream failures.
func (h *RelayHandler) streamErrorCallback(r *http.Request, meta *proxy.AttemptMeta) func() {
	return func() {
		if r.Context().Err() != nil || meta == nil || meta.MemberID <= 0 {
			return
		}
		h.proxy.RecordStreamFailure(meta.MemberID)
	}
}

func writeUpstreamResult(w http.ResponseWriter, requestID string, result *relay.Result, stream bool, onUsage func(usage.Tokens, int, int), onStreamError func()) {
	if result.Err != nil {
		switch {
		case errors.Is(result.Err, routing.ErrRouteNotFound), errors.Is(result.Err, routing.ErrNoEligible):
			writeError(w, http.StatusNotFound, "no eligible channel for this model")
		case errors.Is(result.Err, proxy.ErrCredential):
			writeError(w, http.StatusInternalServerError, "failed to resolve upstream credentials")
		case errors.Is(result.Err, adapters.ErrUnsupportedPath), errors.Is(result.Err, adapters.ErrUnsupportedFeature):
			writeError(w, http.StatusNotImplemented, "requested capability is not supported by this channel")
		case errors.Is(result.Err, adapters.ErrContentBlocked):
			writeError(w, http.StatusBadRequest, "request blocked by upstream safety policy")
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
	bytesWritten, copyErr := copyUpstreamBody(w, tee, stream)
	if copyErr != nil {
		log.Printf("relay: copy upstream response request_id=%s: %v", requestID, copyErr)
		if stream && onStreamError != nil {
			onStreamError()
		}
	}
	_ = tee.Close()
	if onUsage != nil {
		tokens := tee.Tokens()
		// An interrupted stream that never delivered its final usage chunk
		// would otherwise meter nothing for bytes the client already received.
		// Fall back to a conservative estimate from the written byte count so
		// partial completions are never entirely free.
		if stream && copyErr != nil && !tokens.Valid() && bytesWritten > 0 {
			tokens = usage.Tokens{
				PromptTokens:     0,
				CompletionTokens: int(bytesWritten / streamEstimateBytesPerToken),
				TotalTokens:      int(bytesWritten / streamEstimateBytesPerToken),
			}
		}
		onUsage(tokens, result.StatusCode, result.FirstByteMs)
	}
}

// streamEstimateBytesPerToken is the conservative byte-per-token ratio used to
// meter an interrupted stream that produced no usage chunk (~4 bytes per token
// for typical English text; underestimating is safer than overcharging).
const streamEstimateBytesPerToken = 4

// ClientFamily classifies a User-Agent into a coarse client family for audit
// and troubleshooting. Unknown agents fall back to "unknown".
func ClientFamily(userAgent string) string {
	ua := strings.ToLower(userAgent)
	switch {
	case strings.Contains(ua, "claude-code"), strings.Contains(ua, "claude code"):
		return "claude-code"
	case strings.Contains(ua, "claudedesktop"), strings.Contains(ua, "claude-desktop"), strings.Contains(ua, "claude desktop"):
		return "claude-desktop"
	case strings.Contains(ua, "cherrystudio"), strings.Contains(ua, "cherry studio"):
		return "cherry-studio"
	case strings.Contains(ua, "lobehub"), strings.Contains(ua, "lobechat"), strings.Contains(ua, "lobe-chat"), strings.Contains(ua, "lobe chat"):
		return "lobe"
	case strings.Contains(ua, "chatbox"):
		return "chatbox"
	case strings.Contains(ua, "nextchat"), strings.Contains(ua, "next-chat"):
		return "nextchat"
	case strings.Contains(ua, "opencat"):
		return "opencat"
	case strings.Contains(ua, "copilot"):
		return "copilot"
	case strings.Contains(ua, "cursor"):
		return "cursor"
	case strings.Contains(ua, "windsurf"):
		return "windsurf"
	case strings.Contains(ua, "pi-coding-agent"),
		(strings.Contains(ua, "pi/") && !strings.Contains(ua, "api/")):
		return "pi"
	case strings.Contains(ua, "anthropic"):
		return "anthropic"
	case strings.Contains(ua, "openai") && strings.Contains(ua, "python"):
		return "openai-python"
	case strings.Contains(ua, "openai"), strings.Contains(ua, "chatgpt"):
		return "openai"
	case strings.Contains(ua, "curl"), strings.Contains(ua, "wget"):
		return "cli"
	case strings.Contains(ua, "python-requests"):
		return "python"
	case strings.Contains(ua, "node"), strings.Contains(ua, "axios"), strings.Contains(ua, "undici"):
		return "node"
	case strings.Contains(ua, "postman"):
		return "postman"
	case strings.Contains(ua, "insomnia"):
		return "insomnia"
	default:
		if strings.TrimSpace(userAgent) == "" {
			return "unknown"
		}
		return "browser"
	}
}

// ClientFamilyOf classifies the caller client from the request: an explicit
// X-Meta-Client header wins (let clients self-identify), then the User-Agent,
// then request-body signals for Anthropic-protocol callers (Claude Code uses
// the /v1/messages shape with an anthropic-version header).
func ClientFamilyOf(r *http.Request) string {
	if r == nil {
		return "unknown"
	}
	if declared := strings.TrimSpace(r.Header.Get("X-Meta-Client")); declared != "" {
		// Validate against known families so a stray header cannot fabricate a
		// custom bucket key dimension.
		for _, known := range knownClientFamilies {
			if strings.EqualFold(declared, known) {
				return known
			}
		}
	}
	if family := ClientFamily(r.UserAgent()); family != "unknown" {
		return family
	}
	// Anthropic-protocol callers often send no distinctive UA (Claude Code
	// sends an opaque UA); the anthropic-version header + /v1/messages path
	// pin them down.
	if r.Header.Get("anthropic-version") != "" && strings.HasPrefix(r.URL.Path, "/v1/messages") {
		return "anthropic"
	}
	return "unknown"
}

// knownClientFamilies is the validated set for X-Meta-Client and the bucket
// dimension; keep in sync with ClientFamily's outputs.
var knownClientFamilies = []string{
	"claude-code", "claude-desktop", "cherry-studio", "lobe", "chatbox",
	"nextchat", "opencat", "copilot", "cursor", "windsurf", "pi", "anthropic",
	"openai-python", "openai", "cli", "python", "node", "postman",
	"insomnia", "browser", "unknown",
}

// copyUpstreamBody streams the upstream body to the client. For SSE, it flushes
// after every successful write so intermediaries and ResponseControllers do not
// hold chunks until the stream ends. It returns the number of bytes written to
// the client (used to estimate usage when an interrupted stream never delivered
// its final usage chunk).
func copyUpstreamBody(w http.ResponseWriter, body io.Reader, stream bool) (int64, error) {
	if !stream {
		n, err := io.Copy(w, body)
		return n, err
	}
	flusher, canFlush := w.(http.Flusher)
	buffer := make([]byte, 32*1024)
	var written int64
	for {
		bytesRead, readErr := body.Read(buffer)
		if bytesRead > 0 {
			if _, writeErr := w.Write(buffer[:bytesRead]); writeErr != nil {
				return written, writeErr
			}
			written += int64(bytesRead)
			if canFlush {
				flusher.Flush()
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return written, nil
			}
			return written, readErr
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

// checkModelRate enforces the per-(key, model) request limit, when configured.
// Returns false after writing a 429 response.
func (h *RelayHandler) checkModelRate(w http.ResponseWriter, r *http.Request, model string) bool {
	if h.modelLimiter == nil {
		return true
	}
	keyID, ok := auth.DownstreamKeyID(r)
	if !ok || keyID <= 0 {
		return true
	}
	// Composite bucket key: fnv64(keyID + "\x00" + model + "\x00" + family).
	// Collisions are astronomically unlikely for realistic key/model counts and
	// merely cause an occasional shared bucket — acceptable for a rate limit.
	var buffer bytes.Buffer
	buffer.WriteString(strconv.FormatInt(keyID, 10))
	buffer.WriteByte(0)
	buffer.WriteString(model)
	buffer.WriteByte(0)
	buffer.WriteString(ClientFamilyOf(r))
	hash := fnv.New64a()
	_, _ = hash.Write(buffer.Bytes())
	allowed, wait := h.modelLimiter.Allow(int64(hash.Sum64()))
	if !allowed {
		w.Header().Set("Retry-After", fmt.Sprintf("%.0f", wait.Seconds()))
		writeError(w, http.StatusTooManyRequests, "model rate limit exceeded")
		return false
	}
	return true
}

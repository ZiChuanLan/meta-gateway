package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/imgproto"
	"github.com/lan/meta-gateway/internal/proxy"
	"github.com/lan/meta-gateway/internal/relay"
	"github.com/lan/meta-gateway/internal/routing"
	"github.com/lan/meta-gateway/internal/store"
)

// TryHandler lets the admin console probe chat completions without a downstream key.
// Auth is the admin Bearer; upstream selection uses the same routing/proxy path as /v1.
// Optional channel_id pins a specific upstream when multiple members share the model name.
type TryHandler struct {
	proxy RelayProxy
	db    *store.DB
}

func NewTryHandler(service RelayProxy, db *store.DB) *TryHandler {
	return &TryHandler{proxy: service, db: db}
}

func (h *TryHandler) Register(r chi.Router) {
	r.Post("/try/chat", h.tryChat)
	r.Post("/try/image", h.tryImage)
}

type tryChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type tryChatRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	// Messages is the playground's multi-turn conversation. Prompt stays as the
	// one-shot shortcut the Models page probe has always used; when both arrive
	// the conversation wins.
	Messages []tryChatMessage `json:"messages"`
	System   string           `json:"system"`
	// Pointers so "not supplied" is distinguishable from an explicit 0 — the
	// upstream must not receive a temperature the operator never asked for.
	Temperature *float64 `json:"temperature"`
	TopP        *float64 `json:"top_p"`
	MaxTokens   int      `json:"max_tokens"`
	Stream      bool     `json:"stream"`
	ChannelID   int64    `json:"channel_id"`
}

const (
	tryChatDefaultTokens = 128
	tryChatMaxTokens     = 32768
	// A playground turn is a conversation, not a document. The budget keeps one
	// pasted transcript from becoming a 1 MB admin body that the router would
	// reject anyway.
	tryChatConversationCap = 512 << 10
	tryChatMessageCap      = 100
	tryChatFallbackPrompt  = "Say hello in one short sentence."
)

// buildTryMessages turns either shape of the request into the upstream
// `messages` array. Roles are whitelisted because the console must not become a
// way to smuggle arbitrary fields into the upstream payload.
func buildTryMessages(request *tryChatRequest) ([]map[string]string, error) {
	messages := make([]map[string]string, 0, len(request.Messages)+2)
	if system := strings.TrimSpace(request.System); system != "" {
		messages = append(messages, map[string]string{"role": "system", "content": system})
	}
	if len(request.Messages) == 0 {
		prompt := strings.TrimSpace(request.Prompt)
		if prompt == "" {
			prompt = tryChatFallbackPrompt
		}
		return append(messages, map[string]string{"role": "user", "content": prompt}), nil
	}
	if len(request.Messages) > tryChatMessageCap {
		return nil, errors.New("conversation is too long")
	}
	total := 0
	for _, message := range request.Messages {
		role := strings.TrimSpace(message.Role)
		switch role {
		case "system", "user", "assistant":
		case "developer":
			// OpenAI's newer alias; older upstreams only understand system.
			role = "system"
		default:
			return nil, errors.New("unsupported message role")
		}
		if strings.TrimSpace(message.Content) == "" {
			return nil, errors.New("message content is required")
		}
		total += len(message.Content)
		if total > tryChatConversationCap {
			return nil, errors.New("conversation is too large")
		}
		messages = append(messages, map[string]string{"role": role, "content": message.Content})
	}
	return messages, nil
}

func clampFloat(value, low, high float64) float64 {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

func (h *TryHandler) tryChat(w http.ResponseWriter, r *http.Request) {
	var request tryChatRequest
	if err := decodeJSON(w, r, &request, 1<<20, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	model := strings.TrimSpace(request.Model)
	if model == "" {
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}
	if len([]byte(model)) > 256 {
		writeError(w, http.StatusBadRequest, "model is too long")
		return
	}
	messages, err := buildTryMessages(&request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	maxTokens := request.MaxTokens
	if maxTokens <= 0 {
		maxTokens = tryChatDefaultTokens
	}
	if maxTokens > tryChatMaxTokens {
		maxTokens = tryChatMaxTokens
	}

	payload := map[string]any{
		"model":      model,
		"messages":   messages,
		"stream":     request.Stream,
		"max_tokens": maxTokens,
	}
	if request.Temperature != nil {
		payload["temperature"] = clampFloat(*request.Temperature, 0, 2)
	}
	if request.TopP != nil {
		payload["top_p"] = clampFloat(*request.TopP, 0, 1)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build request")
		return
	}

	requestID := chimw.GetReqID(r.Context())
	if requestID == "" {
		requestID = "admin-try"
	}
	started := time.Now()
	result, meta := h.proxy.ChatCompletionsWithMeta(r.Context(), proxy.Request{
		RequestID:       requestID,
		Model:           model,
		Body:            body,
		Stream:          request.Stream,
		PreferChannelID: request.ChannelID,
	})
	latency := int(time.Since(started).Milliseconds())
	if latency < 0 {
		latency = 0
	}

	if result == nil {
		writeError(w, http.StatusBadGateway, "empty_proxy_result")
		return
	}
	if result.Err != nil {
		h.writeTryChatError(w, r, result)
		return
	}
	if result.Body == nil {
		writeError(w, http.StatusBadGateway, "upstream response missing")
		return
	}
	defer result.Body.Close()

	if request.Stream {
		streamTryChat(w, r, result, meta, model, latency)
		return
	}

	const maxTryResponseBytes = 2 << 20
	upstreamBody, err := io.ReadAll(io.LimitReader(result.Body, maxTryResponseBytes+1))
	if err != nil {
		writeError(w, http.StatusBadGateway, "upstream_read_failed")
		return
	}
	if len(upstreamBody) > maxTryResponseBytes {
		writeError(w, http.StatusBadGateway, "upstream_response_too_large")
		return
	}

	var parsed any
	if json.Unmarshal(upstreamBody, &parsed) != nil {
		parsed = string(upstreamBody)
	}
	payloadOut := map[string]any{
		"status":     result.StatusCode,
		"latency_ms": latency,
		"model":      model,
		"body":       parsed,
	}
	if meta != nil {
		payloadOut["channel_id"] = meta.ChannelID
		payloadOut["channel_name"] = meta.ChannelName
		payloadOut["member_id"] = meta.MemberID
		payloadOut["priority"] = meta.Priority
		payloadOut["weight"] = meta.Weight
	}
	writeJSON(w, http.StatusOK, payloadOut)
}

// writeTryChatError maps a relay failure onto the admin error contract. Shared
// by both shapes so a streamed probe fails exactly like a buffered one.
func (h *TryHandler) writeTryChatError(w http.ResponseWriter, r *http.Request, result *relay.Result) {
	if result.Body != nil {
		_ = result.Body.Close()
	}
	if errors.Is(result.Err, routing.ErrRouteNotFound) {
		writeError(w, http.StatusNotFound, "route_not_found")
		return
	}
	if errors.Is(result.Err, routing.ErrNoEligible) {
		writeError(w, http.StatusNotFound, "no_eligible_upstream")
		return
	}
	if errors.Is(result.Err, proxy.ErrPreferredChannel) {
		writeError(w, http.StatusUnprocessableEntity, "preferred_channel_unavailable")
		return
	}
	if errors.Is(result.Err, proxy.ErrCredential) {
		writeError(w, http.StatusUnprocessableEntity, "credential_unavailable")
		return
	}
	if errors.Is(result.Err, proxy.ErrModelTooLong) {
		writeError(w, http.StatusBadRequest, "model is too long")
		return
	}
	if errors.Is(result.Err, proxy.ErrGuardRejected) {
		writeError(w, http.StatusBadRequest, "request rejected by prompt policy")
		return
	}
	if errors.Is(result.Err, proxy.ErrPayloadFiltered) {
		writeError(w, http.StatusForbidden, "request filtered by policy")
		return
	}
	if errors.Is(result.Err, r.Context().Err()) {
		return
	}
	if errors.Is(result.Err, context.DeadlineExceeded) {
		writeError(w, http.StatusGatewayTimeout, "upstream request timed out")
		return
	}
	// Uncategorised relay failures are surfaced verbatim rather than flattened.
	// This endpoint exists to explain why an upstream refused, and the proxy's
	// own messages are the explanation ("stream ended silently before any
	// content: upstream answered 200 with an empty completion" is the difference
	// between a five-second config fix and a mystery). It is admin-only, and the
	// text is built from upstream status and response shape — never from request
	// headers — so no credential can reach it. Capped so a pathological upstream
	// cannot turn the error body into a payload of its own.
	message := strings.TrimPrefix(result.Err.Error(), "proxy: ")
	if message == "" {
		message = "upstream_failure"
	}
	if len(message) > 300 {
		message = message[:300] + "…"
	}
	writeError(w, http.StatusBadGateway, message)
}

// streamTryChat hands an upstream SSE body to the console.
//
// The console needs the routing decision before the first token exists, and it
// needs a uniform transport: like its buffered sibling this endpoint always
// answers HTTP 200 and reports the *upstream* status in a leading `event: meta`
// frame, so a 4xx from the provider arrives as a readable message instead of a
// failed fetch that swallows the body. Everything after that frame is either
// the provider's own stream, verbatim, or one `error`/`raw` frame when the
// provider did not actually stream.
func streamTryChat(w http.ResponseWriter, r *http.Request, result *relay.Result, meta *proxy.AttemptMeta, model string, latency int) {
	// Reuse the relay's header contract so the console sees the same SSE
	// headers /v1 clients get. Upstream status is deliberately not mirrored
	// (see above).
	copyResponseHeaders(w.Header(), result.Header, true)
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)

	writeFrame := func(event string, data []byte) bool {
		if _, err := w.Write([]byte("event: " + event + "\ndata: ")); err != nil {
			return false
		}
		if _, err := w.Write(data); err != nil {
			return false
		}
		if _, err := w.Write([]byte("\n\n")); err != nil {
			return false
		}
		if flusher != nil {
			flusher.Flush()
		}
		return true
	}

	head := map[string]any{
		"status":     result.StatusCode,
		"latency_ms": latency,
		"model":      model,
		"stream":     true,
	}
	if meta != nil {
		head["channel_id"] = meta.ChannelID
		head["channel_name"] = meta.ChannelName
		head["member_id"] = meta.MemberID
		head["priority"] = meta.Priority
		head["weight"] = meta.Weight
	}
	if encoded, err := json.Marshal(head); err == nil {
		if !writeFrame("meta", encoded) {
			return
		}
	}

	contentType := strings.ToLower(result.Header.Get("Content-Type"))
	switch {
	case result.StatusCode < 200 || result.StatusCode >= 300:
		// Providers answer a rejected stream with a plain JSON error.
		if !writeFrame("error", readCapped(result.Body, tryChatFrameCap)) {
			return
		}
	case !strings.HasPrefix(contentType, "text/event-stream"):
		// A channel's own stream policy can override the client's choice, in
		// which case the proxy hands back one aggregated completion instead of a
		// stream. Pass the document on rather than reporting an empty reply.
		if !writeFrame("raw", readCapped(result.Body, tryChatRawFrameCap)) {
			return
		}
	default:
		copyTryStream(r.Context(), w, result.Body, flusher)
	}
	writeFrame("done", []byte(`{"status":"complete"}`))
}

const (
	tryChatFrameCap    = 64 << 10
	tryChatRawFrameCap = 2 << 20
)

// readCapped never fails: a truncated diagnostic is more useful to the operator
// than an error about failing to describe an error.
func readCapped(body io.Reader, limit int64) []byte {
	data, _ := io.ReadAll(io.LimitReader(body, limit))
	return data
}

func copyTryStream(ctx context.Context, w io.Writer, body io.Reader, flusher http.Flusher) {
	buffer := make([]byte, 16<<10)
	for {
		if ctx.Err() != nil {
			// Console navigated away or hit stop; the deferred Close on the
			// upstream body tears the provider connection down.
			return
		}
		read, err := body.Read(buffer)
		if read > 0 {
			if _, writeErr := w.Write(buffer[:read]); writeErr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}

// tryImage is the image counterpart of try/chat: the console's image workbench
// calls it with admin auth, so no downstream key is needed.
//
// The point of routing this through the capability registry instead of letting
// the browser pick a path is that the three upstream families disagree:
// gpt-image wants multipart, grok's editor answers 415 for multipart, and
// Gemini's image models never leave /v1/chat/completions. Getting it wrong
// looks like "the gateway is broken" when the upstream simply refused the
// encoding — so we resolve it server-side and hand the plan back to the UI.
func (h *TryHandler) tryImage(w http.ResponseWriter, r *http.Request) {
	const maxImageRequestBytes = 40 << 20
	const maxImageResponseBytes = 40 << 20

	var request struct {
		Model              string `json:"model"`
		Prompt             string `json:"prompt"`
		Mode               string `json:"mode"`
		Format             string `json:"format"`
		Size               string `json:"size"`
		N                  int    `json:"n"`
		ResponseFormat     string `json:"response_format"`
		IncludeRawResponse bool   `json:"include_raw_response"`
		Images             []struct {
			DataURL string `json:"data_url"`
			Name    string `json:"name"`
		} `json:"images"`
		ChannelID int64 `json:"channel_id"`
	}
	if err := decodeJSON(w, r, &request, maxImageRequestBytes, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	model := strings.TrimSpace(request.Model)
	if model == "" {
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}
	if len([]byte(model)) > 256 {
		writeError(w, http.StatusBadRequest, "model is too long")
		return
	}

	cap := domain.ClassifyModel(model)
	cap.ResolvedByBuiltin = true
	if h.db != nil && h.db.ModelCapability != nil {
		cap, _ = h.db.ModelCapability.Resolve(model)
	}
	plan, err := imgproto.PlanForRequest(cap, imgproto.ParseMode(request.Mode), len(request.Images))
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	// An explicit encoding override lets an operator work around a mislabelled
	// upstream without editing the whole row.
	switch strings.ToLower(strings.TrimSpace(request.Format)) {
	case "":
	case imgproto.FormatJSON:
		plan.Format = imgproto.FormatJSON
	case imgproto.FormatMultipart:
		if plan.UsesChatProtocol {
			writeError(w, http.StatusBadRequest, "this model speaks the chat protocol; multipart does not apply")
			return
		}
		plan.Format = imgproto.FormatMultipart
	default:
		writeError(w, http.StatusBadRequest, "format must be json or multipart")
		return
	}

	inputs := make([]imgproto.ImageInput, 0, len(request.Images))
	for _, img := range request.Images {
		inputs = append(inputs, imgproto.ImageInput{DataURL: img.DataURL, Name: img.Name})
	}
	body, contentType, err := imgproto.BuildBody(plan, model, imgproto.Request{
		Mode:           plan.Mode,
		Prompt:         strings.TrimSpace(request.Prompt),
		Size:           strings.TrimSpace(request.Size),
		N:              request.N,
		ResponseFormat: strings.TrimSpace(request.ResponseFormat),
		Images:         inputs,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	requestID := chimw.GetReqID(r.Context())
	if requestID == "" {
		requestID = "admin-try-image"
	}
	started := time.Now()
	result, meta := h.proxy.ForwardWithMeta(r.Context(), proxy.Request{
		RequestID:       requestID,
		Model:           model,
		Body:            body,
		Method:          http.MethodPost,
		OpenAIPath:      plan.Endpoint,
		ContentType:     contentType,
		PreferChannelID: request.ChannelID,
	})
	latency := int(time.Since(started).Milliseconds())
	if latency < 0 {
		latency = 0
	}
	if result == nil {
		writeError(w, http.StatusBadGateway, "empty_proxy_result")
		return
	}
	if result.Err != nil {
		writeUpstreamResult(w, r.Context(), requestID, result, false, nil, nil, nil)
		return
	}
	if result.Body == nil {
		writeError(w, http.StatusBadGateway, "upstream response missing")
		return
	}
	defer result.Body.Close()

	upstreamBody, err := io.ReadAll(io.LimitReader(result.Body, maxImageResponseBytes+1))
	if err != nil {
		writeError(w, http.StatusBadGateway, "upstream_read_failed")
		return
	}
	if len(upstreamBody) > maxImageResponseBytes {
		writeError(w, http.StatusBadGateway, "upstream_response_too_large")
		return
	}

	images := imgproto.ExtractImages(upstreamBody)
	payload := map[string]any{
		"status":     result.StatusCode,
		"latency_ms": latency,
		"model":      model,
		"plan":       plan,
		"images":     images,
	}
	// Large base64 images otherwise appear twice in every workbench response.
	// Keep raw responses available for diagnostics without duplicating success.
	if request.IncludeRawResponse || result.StatusCode < 200 || result.StatusCode >= 300 || len(images) == 0 {
		var parsed any
		if json.Unmarshal(upstreamBody, &parsed) != nil {
			parsed = string(upstreamBody)
		}
		payload["body"] = parsed
	}
	if meta != nil {
		payload["channel_id"] = meta.ChannelID
		payload["channel_name"] = meta.ChannelName
		payload["member_id"] = meta.MemberID
	}
	writeJSON(w, http.StatusOK, payload)
}

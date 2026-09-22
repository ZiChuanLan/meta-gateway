package httpapi

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/lan/meta-gateway/internal/adapters"
	"github.com/lan/meta-gateway/internal/auth"
	"github.com/lan/meta-gateway/internal/livetrace"
	"github.com/lan/meta-gateway/internal/proxy"
	"github.com/lan/meta-gateway/internal/usage"
)

// Custom-path passthrough: POST /v1/<anything-not-registered>.
//
// The /v1 surface is a fixed list of OpenAI-compatible endpoints (chat,
// responses, embeddings, images…). An upstream that speaks its OWN protocol —
// TypeSafe's POST /v1/systemone is the reference case — has no registered
// path, so before this handler the only way to reach it was to bounce the
// request through an existing endpoint (patching Content-Type and the URL with
// header/payload rules).
//
// new-api accepts such an upstream with a single field: its Custom channel type
// treats the base URL as the complete upstream endpoint
// (new-api-main/relay/channel/openai/adaptor.go:162-165) and forwards whatever
// path the client called. This handler is the gateway's equivalent: an
// unregistered /v1 path is forwarded verbatim to the channel's
// `<base>/<same path>`, body and response untouched, so a channel whose base URL
// is `https://api.typesafe.ai` plus a client calling `/v1/systemone` just works.
//
// It is deliberately narrow, following sub2api's closed-allowlist URL guard
// (sub2api backend/internal/service/upstream_path_guard.go): a path may only
// contain structural-inert segments (word characters, '-', '.') so a client can
// never inject traversal, a second path, a query, or a fragment into the
// upstream URL. Measured against the router, an escaped path stays escaped
// (`/v1/%2E%2E%2Fsystemone` is handed over undecoded, fails the allowlist on '%',
// and answers 404 without any outbound request). Anything else keeps the
// historical 404.

// maxCustomPathBodyBytes bounds a custom-path request body. It matches the
// model-request limit: these are ordinary JSON API calls, not uploads.
const maxCustomPathBodyBytes = 10 << 20

// upstreamPathPinHeader lets a client state which upstream path it wants when
// it cannot change the URL it calls (SDKs that hardcode /v1/chat/completions).
// The value is validated by the same allowlist as a real path and, crucially,
// exposed to channel payload rules as a request header — so a rule can map a
// model (or any other condition) to a different upstream path:
//
//	{"match": {"model": "jev-latest"},
//	 "actions": [{"op": "set", "path": "upstream_path", "value": {"str": "systemone"}}]}
//
// (the set/delete DSL writes body fields, so the rule maps the header onto
// nothing by itself; the header is what makes the per-model choice expressible
// without an extra endpoint.)
const upstreamPathPinHeader = "X-Meta-Upstream-Path"

// customPath relays an unregistered /v1 path verbatim.
func (h *RelayHandler) customPath(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimSpace(chi.URLParam(r, "*")), "/")
	if path == "" || !IsSafeUpstreamPathSuffix(path) {
		writeError(w, http.StatusNotFound, "unknown endpoint")
		return
	}
	if !auth.HasScope(auth.DownstreamScopes(r), auth.ScopeRelay) {
		writeError(w, http.StatusForbidden, "insufficient scope")
		return
	}
	if !h.ensureQuota(w, r) {
		return
	}
	if !h.ensureGroupRate(w, r) {
		return
	}
	if pin := strings.Trim(strings.TrimSpace(r.Header.Get(upstreamPathPinHeader)), "/"); pin != "" {
		if !IsSafeUpstreamPathSuffix(pin) {
			writeError(w, http.StatusBadRequest, "invalid "+upstreamPathPinHeader)
			return
		}
		path = pin
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxCustomPathBodyBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "body too large")
		return
	}
	defer r.Body.Close()

	var request struct {
		Model           string `json:"model"`
		Stream          bool   `json:"stream"`
		ReasoningEffort string `json:"reasoning_effort"`
	}
	// The body is parsed for routing only; what is forwarded is the original
	// bytes. A body that is not JSON is rejected like every other relay entry
	// point: routing needs a model, so accepting it would only relabel the same
	// failure. (Multipart custom paths are out of scope — the registered
	// audio/image endpoints already handle those.)
	if err := json.Unmarshal(body, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	modelName := strings.TrimSpace(request.Model)
	if modelName == "" {
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}
	if len([]byte(modelName)) > 256 {
		writeError(w, http.StatusBadRequest, "model is too long")
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

	clientKeyName := ""
	if key := auth.DownstreamKey(r); key != nil {
		clientKeyName = key.Name
	}
	watchCtx := r.Context()
	var finishTrace func()
	if h.liveTrace != nil && requestID != "" {
		lbCtx, release, _ := h.liveTrace.Begin(r.Context(), requestID, "openai", modelName, livetrace.BeginMeta{Stream: request.Stream, ClientKey: clientKeyName})
		watchCtx, finishTrace = lbCtx, release
	}
	if finishTrace == nil {
		finishTrace = func() {}
	}
	defer finishTrace()

	headers := clientHeaders(r.Header)
	// Pin the path where payload rules can see it, so a per-model
	// upstream-path decision stays expressible.
	headers[upstreamPathPinHeader] = path

	proxyReq := proxy.Request{
		RequestID:          requestID,
		Model:              modelName,
		Body:               body,
		Stream:             request.Stream,
		Method:             http.MethodPost,
		OpenAIPath:         path,
		DownstreamKeyID:    keyID,
		DownstreamProtocol: "openai",
		ContentType:        r.Header.Get("Content-Type"),
		SessionKey:         r.Header.Get("X-Meta-Session-Id"),
		ReasoningEffort:    request.ReasoningEffort,
		Headers:            headers,
		RouteGroup:         downstreamRouteGroup(r),
	}
	result, meta := h.proxy.ForwardWithMeta(watchCtx, proxyReq)
	writeUpstreamResult(
		w, watchCtx, requestID, result, request.Stream,
		func(tokens usage.Tokens, status int, firstByteMs int, bytesSent int64) {
			channelID := int64(0)
			if meta != nil {
				channelID = meta.ChannelID
				proxyReq.RouteID = meta.RouteID
				proxyReq.MemberID = meta.MemberID
				proxyReq.GrayAttempt = meta.GrayAttempt
			}
			h.proxy.RecordUsage(proxyReq, channelID, status, tokens)
			if h.db != nil && h.db.ProxyLog != nil && requestID != "" {
				if err := h.db.ProxyLog.UpdateMetaByRequestID(requestID, firstByteMs, clientFamily); err != nil {
					log.Printf("relay: update log meta request_id=%s: %v", requestID, err)
				}
			}
			if h.liveTrace != nil && requestID != "" {
				h.liveTrace.FinishCopied(requestID, status, int64(firstByteMs), bytesSent, tokens.PromptTokens, tokens.CompletionTokens)
			}
		},
		func(int64) {
			// Custom paths carry no known stream contract, so the live view gets
			// no byte-progress rumble; the request still shows as running.
		},
		h.streamErrorCallback(watchCtx, meta),
	)
	if h.liveTrace != nil && requestID != "" {
		switch {
		case result == nil:
			h.liveTrace.Finish(requestID, livetrace.StatusFailed, "upstream response missing")
		case result.Err != nil && r.Context().Err() != nil:
			h.liveTrace.Finish(requestID, livetrace.StatusCanceled, result.Err.Error())
		case result.Err != nil:
			h.liveTrace.Finish(requestID, livetrace.StatusFailed, result.Err.Error())
		}
	}
}

// UpstreamURLEchoHeader names the response header carrying the URL the gateway
// actually called (scheme + host + path; query and fragment are stripped because
// they can carry credentials — sub2api's safeUpstreamURL makes the same cut).
// The proxy sets it on the relay result and copyResponseHeaders forwards it, so
// a custom-path call is traceable end to end from the client side.
const UpstreamURLEchoHeader = "X-Meta-Upstream-URL"

// IsSafeUpstreamPathSuffix reports whether a "/a/b" style path can be appended
// to an upstream base URL. The grammar lives in the adapters package (which owns
// URL construction); this is the HTTP layer's name for it.
func IsSafeUpstreamPathSuffix(path string) bool {
	return adapters.IsSafeURLPathSuffix(path)
}

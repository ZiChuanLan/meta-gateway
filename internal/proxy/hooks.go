package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/relay"
)

// Plugin hook seam.
//
// A sidecar plugin may declare three interception points along the forward
// pipeline. The proxy owns the contract; internal/httpapi supplies the
// implementation (the plugin host), so this package never imports the plugin
// machinery — the same shape as LiveTraceObserver.
//
// Two properties are load-bearing and every path below preserves them:
//
//  1. Fail-open. A plugin that is missing, slow, broken, or malformed never
//     changes the outcome of a request. The only way a plugin affects traffic
//     is by returning an explicit, well-formed decision.
//  2. Zero cost when unused. A request whose model matches no plugin
//     declaration never leaves the gateway's own code path: Wants is the cheap
//     pre-check that gates everything else (including the model-catalogue read
//     that only the route point needs).
type HookPoint string

const (
	// HookRoute runs once per request, BEFORE the selector is consulted. It is
	// the only point that can change WHICH model is routed, so it decides for
	// the whole request: a failover replay must not drift onto a different
	// model than the one that was chosen for the client's request.
	HookRoute HookPoint = "route"
	// HookRequest runs once per channel attempt, after the upstream body and
	// endpoint are final. A rewrite here applies to every key attempt of that
	// channel.
	HookRequest HookPoint = "request"
	// HookResponse runs on a successful NON-STREAMING answer, after it has been
	// converted to the downstream contract and before the client sees it.
	// Streaming answers are deliberately not offered: an SSE stream is copied
	// chunk by chunk, so there is no complete document for a plugin to rewrite
	// (and buffering one would destroy the streaming latency it exists for).
	HookResponse HookPoint = "response"
)

// HookInput is the context handed to a plugin. Point is always set; every
// other field is populated per point and left zero where it does not apply.
type HookInput struct {
	Point     HookPoint `json:"hook"`
	RequestID string    `json:"request_id,omitempty"`
	// Model is the model being routed or forwarded. At HookRoute it is what the
	// CLIENT asked for (possibly a virtual name such as "auto"); at the later
	// points it is the resolved real model.
	Model  string `json:"model"`
	Stream bool   `json:"stream"`
	// Body is the JSON payload for this point: the client request at HookRoute,
	// the upstream request at HookRequest, the upstream answer at HookResponse.
	// Nil when the payload was not valid JSON, in which case a plugin may still
	// decide on the other fields but cannot rewrite the body.
	Body json.RawMessage `json:"body,omitempty"`
	// Headers carries client request headers (canonical keys, credential and
	// hop-by-hop headers removed) so a plugin can branch on client identity.
	Headers map[string]string `json:"headers,omitempty"`
	// RouteGroup is the downstream key's bound route group ("" = default).
	RouteGroup string `json:"route_group,omitempty"`
	// Attempt is the 1-based failover round. Always 1 at HookRoute.
	Attempt int `json:"attempt,omitempty"`
	// AvailableModels is HookRoute-only: the models the gateway can currently
	// route. A router cannot select a model that is absent from this list.
	AvailableModels []string `json:"available_models,omitempty"`
	// Channel facts for the attempt (HookRequest / HookResponse).
	ChannelID     int64  `json:"channel_id,omitempty"`
	ChannelName   string `json:"channel_name,omitempty"`
	UpstreamModel string `json:"upstream_model,omitempty"`
	UpstreamURL   string `json:"upstream_url,omitempty"`
	// StatusCode is HookResponse-only: what the upstream answered.
	StatusCode int `json:"status_code,omitempty"`
	// LatencyMs is HookResponse-only: the upstream round-trip time.
	LatencyMs int `json:"latency_ms,omitempty"`
}

// HookReject aborts a request instead of forwarding it.
type HookReject struct {
	Status  int    `json:"status,omitempty"`
	Message string `json:"message,omitempty"`
}

// HookResult is a plugin's answer. Handled=false (or a nil result, or any
// failure recorded in Err) means "no opinion": the gateway proceeds exactly as
// if the plugin had not been installed.
type HookResult struct {
	Handled bool `json:"handled"`
	// PluginID is filled by the gateway, never by the plugin payload.
	PluginID string `json:"-"`
	// Model replaces the routed model. Only meaningful at HookRoute; ignored
	// elsewhere so a later point cannot silently re-route the request.
	Model string `json:"model,omitempty"`
	// Reason and Confidence are recorded for the operator. They never change
	// control flow — a low confidence is the plugin's problem to resolve, and
	// the gateway has no second opinion to fall back on.
	Reason     string   `json:"reason,omitempty"`
	Confidence *float64 `json:"confidence,omitempty"`
	// Body replaces the payload. At HookRequest it is the upstream request; at
	// HookResponse it is the answer sent to the client.
	Body json.RawMessage `json:"body,omitempty"`
	// Headers are merged into the upstream request (HookRequest) or the client
	// answer (HookResponse).
	Headers map[string]string `json:"headers,omitempty"`
	// Status rewrites the client-visible status code (HookResponse only).
	Status int `json:"status,omitempty"`
	// Reject aborts the request with this status instead of forwarding it.
	Reject *HookReject `json:"reject,omitempty"`
	// The fields below are gateway bookkeeping, not part of the wire contract.
	Fallback  bool  `json:"-"`
	LatencyMs int   `json:"-"`
	Err       error `json:"-"`
}

// HookDecision is one recorded plugin decision. It is audit metadata only: the
// gateway never re-reads it to change behavior, but the log row and the
// console need it to explain a result the operator did not configure by hand
// (e.g. "auto" resolving to a model a plugin picked).
type HookDecision struct {
	Point      HookPoint `json:"point"`
	PluginID   string    `json:"plugin_id"`
	Model      string    `json:"model,omitempty"`
	Reason     string    `json:"reason,omitempty"`
	Confidence *float64  `json:"confidence,omitempty"`
	LatencyMs  int       `json:"latency_ms,omitempty"`
	// Fallback marks a decision the gateway synthesized because the plugin
	// failed or declined; Error carries why.
	Fallback bool   `json:"fallback,omitempty"`
	Error    string `json:"error,omitempty"`
}

// Interceptor is the plugin-host seam implemented by internal/plugins.
type Interceptor interface {
	// Wants reports whether any enabled plugin declared an interest in this
	// point for this model. It must be cheap and must never touch the network:
	// it is what keeps unmatched models (the overwhelming majority) entirely
	// off the plugin path.
	Wants(point HookPoint, model string) bool
	// Decide offers the point to every interested plugin in priority order and
	// returns the first decision. It returns nil when nobody had an opinion —
	// including every failure mode (timeout, transport error, malformed
	// answer, open circuit breaker), because a broken plugin must never break
	// the gateway.
	Decide(ctx context.Context, req HookInput) *HookResult
}

// hookModelsCacheTTL bounds how stale the HookRoute model catalogue may be.
// Same order as the /v1/models cache: long enough to keep the DB off the hot
// path, short enough that a freshly created route shows up while the operator
// is still looking at it.
const hookModelsCacheTTL = 5 * time.Second

// SetInterceptor installs the plugin hook host (nil disables plugin hooks).
func (s *Service) SetInterceptor(interceptor Interceptor) {
	if interceptor == nil {
		s.interceptor.Store(nil)
		return
	}
	s.interceptor.Store(&interceptor)
}

// hookEnabled reports whether plugin hooks are installed at all.
func (s *Service) hookEnabled() Interceptor {
	interceptor := s.interceptor.Load()
	if interceptor == nil {
		return nil
	}
	return *interceptor
}

// recordHookDecision appends one decision to the request's audit trail.
func recordHookDecision(req *Request, decision HookDecision) {
	if req == nil {
		return
	}
	req.HookDecisions = append(req.HookDecisions, decision)
}

// availableModels lists the model names the gateway can currently route,
// mirroring the /v1/models catalogue: enabled route patterns, falling back to
// the enabled channels' model lists when no enabled route exposes any model.
// Cached briefly — the route point is hot and the answer changes slowly.
func (s *Service) availableModels() []string {
	if s.db == nil {
		return nil
	}
	s.hookModelsMu.Lock()
	defer s.hookModelsMu.Unlock()
	now := s.now()
	if !s.hookModelsAt.IsZero() && now.Sub(s.hookModelsAt) < hookModelsCacheTTL {
		return s.hookModelsCache
	}
	seen := make(map[string]struct{})
	models := make([]string, 0, 32)
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		models = append(models, id)
	}
	if s.db.Route != nil {
		if routes, err := s.db.Route.List(); err == nil {
			for _, route := range routes {
				if route.Enabled {
					add(route.ModelPattern)
				}
			}
		}
	}
	if len(models) == 0 && s.db.Channel != nil {
		// Cold fallback (no enabled routes at all): manual-sync channels adopt
		// models on demand, so their unadopted entries are not part of the
		// catalogue.
		if channels, err := s.db.Channel.ListEnabled(); err == nil {
			for _, channel := range channels {
				if channel.ModelSyncMode == domain.ModelSyncModeManual {
					continue
				}
				for _, model := range strings.Split(channel.ModelsCSV, ",") {
					add(model)
				}
			}
		}
	}
	s.hookModelsCache = models
	s.hookModelsAt = now
	return models
}

// applyRouteHook offers the request to the plugin chain before selection.
//
// It is called once per request (attempt 0) because the decision is
// request-scoped: letting a failover round re-run it would let a plugin
// silently move the client onto a different model mid-request.
//
// Returns a terminal result only when a plugin rejected the request.
func (s *Service) applyRouteHook(ctx context.Context, req *Request, interceptor Interceptor) *relay.Result {
	if req == nil || !interceptor.Wants(HookRoute, req.Model) {
		return nil
	}
	// The original name is preserved so a rewrite stays attributable: after
	// this point req.Model is the real model and the client's "auto" would
	// otherwise be invisible in every log and in the response.
	requested := req.Model
	hookReq := HookInput{
		Point:           HookRoute,
		RequestID:       req.RequestID,
		Model:           requested,
		Stream:          req.Stream,
		Body:            jsonPayload(req.Body),
		Headers:         hookVisibleHeaders(req.Headers),
		RouteGroup:      req.RouteGroup,
		Attempt:         1,
		AvailableModels: s.availableModels(),
	}
	result := interceptor.Decide(ctx, hookReq)
	if result == nil {
		return nil
	}
	decision := HookDecision{
		Point:      HookRoute,
		PluginID:   result.PluginID,
		Reason:     result.Reason,
		Confidence: result.Confidence,
		LatencyMs:  result.LatencyMs,
		Fallback:   result.Fallback,
	}
	if result.Err != nil {
		decision.Error = result.Err.Error()
	}
	if !result.Handled {
		// Declined: record why (if the plugin said) and continue untouched.
		if decision.Error != "" || decision.Reason != "" || result.Fallback {
			recordHookDecision(req, decision)
		}
		return nil
	}
	if result.Reject != nil {
		status := result.Reject.Status
		if status < 400 || status > 599 {
			status = http.StatusForbidden
		}
		message := strings.TrimSpace(result.Reject.Message)
		if message == "" {
			message = "request rejected by plugin"
		}
		decision.Model = requested
		recordHookDecision(req, decision)
		log.Printf("proxy: plugin %q rejected request (status=%d model=%s request_id=%s)", result.PluginID, status, requested, req.RequestID)
		return &relay.Result{StatusCode: status, Err: fmt.Errorf("%w: %s", ErrPluginRejected, message)}
	}
	chosen := strings.TrimSpace(result.Model)
	if chosen == "" || chosen == requested {
		// Handled but unchanged: an observation, not a rewrite.
		if chosen != "" {
			decision.Model = chosen
		}
		recordHookDecision(req, decision)
		return nil
	}
	if len([]byte(chosen)) > 256 {
		decision.Error = "chosen model name is too long"
		recordHookDecision(req, decision)
		log.Printf("proxy: plugin %q chose an over-long model name (request_id=%s)", result.PluginID, req.RequestID)
		return nil
	}
	req.Model = chosen
	// The rewritten name is what routing, billing, and the log row use, so the
	// original is kept next to it for attribution.
	req.RequestedModel = requested
	req.ModelRewrittenBy = result.PluginID
	// The upstream receives req.Body, so the model field has to move with the
	// routing key. Without this the provider is asked for "auto".
	if rewritten, ok := rewriteRoutedModel(req.Body, requested, chosen); ok {
		req.Body = rewritten
	}
	decision.Model = chosen
	recordHookDecision(req, decision)
	log.Printf("proxy: plugin %q routed %s -> %s (confidence=%s reason=%q latency=%dms request_id=%s)",
		result.PluginID, requested, chosen, formatConfidence(result.Confidence), result.Reason, result.LatencyMs, req.RequestID)
	return nil
}

// requestHookOutcome carries the accepted parts of a HookRequest decision.
type requestHookOutcome struct {
	body        []byte
	headers     map[string]string
	rejected    *relay.Result
	decision    HookDecision
	hasDecision bool
}

// applyRequestHook offers the final upstream body to the plugin chain.
//
// Called per channel attempt (a different channel speaks a different upstream
// shape, so the plugin must see the body that will actually be sent). Returns
// nil when no plugin had an opinion.
func (s *Service) applyRequestHook(
	ctx context.Context,
	req *Request,
	candidate *domain.RoutingCandidate,
	upstreamURL string,
	upstreamModel string,
	attempt int,
	body []byte,
	interceptor Interceptor,
) *requestHookOutcome {
	if req == nil || candidate == nil || !interceptor.Wants(HookRequest, req.Model) {
		return nil
	}
	hookReq := HookInput{
		Point:         HookRequest,
		RequestID:     req.RequestID,
		Model:         req.Model,
		Stream:        req.Stream,
		Body:          jsonPayload(body),
		Headers:       hookVisibleHeaders(req.Headers),
		RouteGroup:    req.RouteGroup,
		Attempt:       attempt,
		ChannelID:     candidate.Channel.ID,
		ChannelName:   candidate.Channel.Name,
		UpstreamModel: upstreamModel,
		UpstreamURL:   upstreamURL,
	}
	result := interceptor.Decide(ctx, hookReq)
	if result == nil {
		return nil
	}
	decision := HookDecision{
		Point:      HookRequest,
		PluginID:   result.PluginID,
		Reason:     result.Reason,
		Confidence: result.Confidence,
		LatencyMs:  result.LatencyMs,
		Fallback:   result.Fallback,
	}
	if result.Err != nil {
		decision.Error = result.Err.Error()
	}
	outcome := &requestHookOutcome{decision: decision, hasDecision: true}
	if !result.Handled {
		return outcome
	}
	if result.Reject != nil {
		status := result.Reject.Status
		if status < 400 || status > 599 {
			status = http.StatusForbidden
		}
		message := strings.TrimSpace(result.Reject.Message)
		if message == "" {
			message = "request rejected by plugin"
		}
		outcome.rejected = &relay.Result{StatusCode: status, Err: fmt.Errorf("%w: %s", ErrPluginRejected, message)}
		log.Printf("proxy: plugin %q rejected request at request hook (status=%d model=%s request_id=%s)", result.PluginID, status, req.Model, req.RequestID)
		return outcome
	}
	if rewritten, ok := hookBody(result.Body, body); ok {
		outcome.body = rewritten
	}
	if len(result.Headers) > 0 {
		outcome.headers = result.Headers
	}
	log.Printf("proxy: plugin %q rewrote upstream request (channel=%d model=%s body=%v headers=%d request_id=%s)",
		result.PluginID, candidate.Channel.ID, req.Model, outcome.body != nil, len(outcome.headers), req.RequestID)
	return outcome
}

// applyResponseHook offers a complete, non-streaming answer to the plugin
// chain before the client sees it, and returns the (possibly rewritten)
// result. Streaming answers are never offered — see HookResponse.
func (s *Service) applyResponseHook(
	ctx context.Context,
	req *Request,
	candidate *domain.RoutingCandidate,
	upstreamModel string,
	attempt int,
	result *relay.Result,
	interceptor Interceptor,
) *relay.Result {
	if req == nil || candidate == nil || result == nil {
		return result
	}
	// Only a complete JSON document can be rewritten. A streaming request keeps
	// its body as an open reader all the way to the client.
	if req.Stream || result.Body == nil || result.Err != nil {
		return result
	}
	if result.StatusCode < 200 || result.StatusCode >= 300 {
		return result
	}
	if !interceptor.Wants(HookResponse, req.Model) {
		return result
	}
	raw, err := readResponseBody(result.Body, preserveBodyReadLimit)
	_ = result.Body.Close()
	if err != nil {
		// The body could not be buffered: hand back what is left rather than
		// failing a request the upstream already answered.
		result.Body = io.NopCloser(bytes.NewReader(nil))
		result.Err = err
		return result
	}
	hookReq := HookInput{
		Point:         HookResponse,
		RequestID:     req.RequestID,
		Model:         req.Model,
		Stream:        false,
		Body:          jsonPayload(raw),
		Headers:       hookVisibleHeaders(req.Headers),
		RouteGroup:    req.RouteGroup,
		Attempt:       attempt,
		ChannelID:     candidate.Channel.ID,
		ChannelName:   candidate.Channel.Name,
		UpstreamModel: upstreamModel,
		StatusCode:    result.StatusCode,
		LatencyMs:     result.LatencyMs,
	}
	hookResult := interceptor.Decide(ctx, hookReq)
	restore := func(payload []byte) *relay.Result {
		result.Body = io.NopCloser(bytes.NewReader(payload))
		return result
	}
	if hookResult == nil {
		return restore(raw)
	}
	decision := HookDecision{
		Point:      HookResponse,
		PluginID:   hookResult.PluginID,
		Reason:     hookResult.Reason,
		Confidence: hookResult.Confidence,
		LatencyMs:  hookResult.LatencyMs,
		Fallback:   hookResult.Fallback,
	}
	if hookResult.Err != nil {
		decision.Error = hookResult.Err.Error()
	}
	if !hookResult.Handled {
		if decision.Error != "" || decision.Reason != "" || hookResult.Fallback {
			recordHookDecision(req, decision)
		}
		return restore(raw)
	}
	changed := false
	if rewritten, ok := hookBody(hookResult.Body, raw); ok {
		raw = rewritten
		changed = true
	}
	if hookResult.Status >= 100 && hookResult.Status <= 599 && hookResult.Status != result.StatusCode {
		result.StatusCode = hookResult.Status
		changed = true
	}
	if len(hookResult.Headers) > 0 {
		if result.Header == nil {
			result.Header = make(http.Header)
		}
		for key, value := range hookResult.Headers {
			if strings.TrimSpace(key) == "" {
				continue
			}
			result.Header.Set(key, value)
		}
		changed = true
	}
	recordHookDecision(req, decision)
	if changed {
		log.Printf("proxy: plugin %q rewrote upstream response (channel=%d model=%s status=%d request_id=%s)",
			hookResult.PluginID, candidate.Channel.ID, req.Model, result.StatusCode, req.RequestID)
	}
	return restore(raw)
}

// jsonPayload returns the payload as raw JSON, or nil when it is not valid
// JSON. A non-JSON body still lets a plugin decide on the other fields; it just
// cannot be rewritten through this channel.
func jsonPayload(body []byte) json.RawMessage {
	if len(body) == 0 || !json.Valid(body) {
		return nil
	}
	return json.RawMessage(body)
}

// hookBody accepts a plugin-supplied replacement only when it is valid JSON:
// a malformed rewrite would otherwise turn a working upstream answer into a
// client-side parse error.
func hookBody(replacement json.RawMessage, current []byte) ([]byte, bool) {
	if len(replacement) == 0 || !json.Valid(replacement) {
		return nil, false
	}
	return append([]byte(nil), replacement...), true
}

// hookVisibleHeaders strips credentials before headers reach a plugin. A
// plugin is an operator-installed sidecar, but the client's Authorization
// header belongs to the downstream key and has no business leaving the
// gateway.
func hookVisibleHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]string, len(headers))
	for key, value := range headers {
		switch strings.ToLower(key) {
		case "authorization", "cookie", "proxy-authorization", "x-api-key", "api-key":
			continue
		}
		out[http.CanonicalHeaderKey(key)] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// rewriteRoutedModel replaces the body's model field after a route hook chose a
// different model.
//
// The upstream receives the BODY, not the routing key, so rewriting only
// req.Model would send the client's virtual name ("auto") to a provider that
// has never heard of it. Returns false when the body is not a JSON object, has
// no model field, or carries a different model than expected — every one of
// those means "leave it alone" rather than "fail the request".
func rewriteRoutedModel(body []byte, from, to string) ([]byte, bool) {
	if len(body) == 0 || from == "" || to == "" {
		return body, false
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return body, false
	}
	raw, ok := payload["model"]
	if !ok {
		return body, false
	}
	var current string
	if err := json.Unmarshal(raw, &current); err != nil || current != from {
		return body, false
	}
	encoded, err := json.Marshal(to)
	if err != nil {
		return body, false
	}
	payload["model"] = encoded
	updated, err := json.Marshal(payload)
	if err != nil {
		return body, false
	}
	return updated, true
}

// formatConfidence renders an optional confidence for a log line.
func formatConfidence(confidence *float64) string {
	if confidence == nil {
		return "n/a"
	}
	return fmt.Sprintf("%.2f", *confidence)
}

// pluginForbiddenHeaders protects the outbound credential from a plugin
// rewrite. A hook may add upstream headers the channel config cannot express,
// but it must not replace the key the gateway injected for this channel — that
// is the channel's own credential, and letting a plugin swap it would turn an
// interception point into a key-exfiltration path.
var pluginForbiddenHeaders = map[string]struct{}{
	"authorization": {},
	"cookie":        {},
	"host":          {},
}

// mergePluginHeaders applies plugin-supplied headers onto the outbound request
// after the channel's own header overrides, skipping transport-level names,
// credential headers, and any value carrying a CR/LF (header injection).
func mergePluginHeaders(headers http.Header, overrides map[string]string) {
	for name, value := range overrides {
		key := http.CanonicalHeaderKey(strings.TrimSpace(name))
		if key == "" {
			continue
		}
		lower := strings.ToLower(key)
		if _, blocked := forbiddenOverrideHeaders[lower]; blocked {
			continue
		}
		if _, blocked := pluginForbiddenHeaders[lower]; blocked {
			continue
		}
		if strings.ContainsAny(value, "\r\n") {
			continue
		}
		headers.Set(key, value)
	}
}

package proxy

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/lan/meta-gateway/internal/adapters"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/relay"
)

// forward_plan.go holds the per-attempt preparation: everything that turns a
// selected candidate into the bytes and the URL the send step will use.
//
// It was the first ~200 lines of ForwardWithMeta's attempt loop. It shares no
// state with the failover bookkeeping around it — only inputs and one work
// product — so it lives here, where the local-validation rules it implements
// (adapter, protocol plan, stream policy, aliases, prompt/reasoning rewrites,
// payload rules, endpoint resolution, plugin request hook) can be read on their
// own.

// attemptPlan is what the send step needs. `protocol` stays whole because the
// response-conversion block later reads its translation and native-fallback
// modes; everything else is already resolved.
type attemptPlan struct {
	adapter                 adapters.ForwardAdapter
	channelMap              UpstreamMap
	protocol                protocolPlan
	effectivePath           string
	upstreamStream          bool
	aggregateUpstreamStream bool
	synthesizeClientStream  bool
	upstreamURL             string
	requestBody             []byte
	requestSource           []byte
	hookHeaders             map[string]string
}

// prepareAttempt builds the plan for one attempt, or returns a terminal result
// when the attempt was rejected locally. Every rejection path records the
// attempt itself, so the caller only has to return what it is handed.
//
// Not one of these checks is an upstream health signal: a payload rule that
// filters the request, an adapter that cannot translate it, an unbuildable
// endpoint, a plugin that says no — each of those is a local decision and must
// leave the breaker, the cooldown and every key untouched. That is why they end
// the request here instead of failing over to another channel.
func (s *Service) prepareAttempt(
	ctx context.Context,
	req *Request,
	candidate domain.RoutingCandidate,
	effectiveModel, mappingJSON string,
	body []byte,
	round int,
) (*attemptPlan, *relay.Result) {
	adapter := s.resolveForward(candidate.Channel)
	// Channel endpoint/field mapping, parsed once per attempt. Empty for every
	// channel that does not opt in, so the hot path stays untouched.
	channelMap := ParseUpstreamMap(candidate.Channel.UpstreamPathOverride, candidate.Channel.UpstreamPathMap, candidate.Channel.UpstreamRequestMap, candidate.Channel.UpstreamResponseMap)

	protocol := s.planProtocol(adapter, *req, candidate.Channel.SystemPrompt)
	adapter = protocol.adapter
	downstreamAnthropic, downstreamResponses := protocol.anthropic, protocol.responses
	registryTranslation := protocol.translation

	// Channel-scoped model aliases: when the matched route or the selected
	// channel's member carries a mapping_json of {"real":"…"}, clients requested
	// the alias and we must rewrite the body back to the upstream's real model
	// name. Member-level mapping wins (shared aliases rewrite per channel);
	// route-level mapping is the legacy/fallback form for aliases created before
	// per-member mappings existed.
	mappedBody := body
	if mappingJSON != "" {
		mappedBody = rewriteModelName(body, req.Model, mappingJSON, req.ContentType)
	}

	effectivePath := req.OpenAIPath

	// Per-channel stream policy: override the client's stream choice for this
	// attempt. Effective only for OpenAI-shaped chat exchanges — the native
	// Anthropic passthrough and the Responses API have protocol-specific stream
	// machinery the policy does not synthesize for. upstreamStream is what the
	// upstream will actually speak: it governs the non-stream budget, while the
	// client's choice still governs the response shape the handler writes.
	upstreamStream := req.Stream
	aggregateUpstreamStream := false
	synthesizeClientStream := false
	if !req.Probe && (effectivePath == "chat/completions" || effectivePath == "completions") {
		nativePassthrough := downstreamAnthropic && adapter.Name() == "anthropic"
		switch candidate.Channel.StreamPolicy {
		case domain.StreamPolicyForceStream:
			if !upstreamStream && !nativePassthrough {
				upstreamStream = true
				aggregateUpstreamStream = true
			}
		case domain.StreamPolicyForceNonStream:
			if upstreamStream && !nativePassthrough {
				upstreamStream = false
				synthesizeClientStream = true
			}
		}
	}
	requestSource := mappedBody
	if !downstreamAnthropic || adapter.Name() == "anthropic" {
		// Channel-level system prompt injection (OpenAI-format chat bodies only;
		// translated requests are injected inside the composed adapter at the
		// pivot step).
		if prompt := strings.TrimSpace(candidate.Channel.SystemPrompt); prompt != "" && effectivePath == "chat/completions" {
			requestSource = injectSystemPrompt(requestSource, prompt)
		}
	}
	// Channel capability-aware reasoning effort rewrite: the operator's declared
	// ceiling and the provider's own accepted rungs both apply (see
	// downgradeReasoningEffort), so a request the upstream would have rejected
	// with a 400 comes back as an answer instead of burning a failover round. The
	// original value is kept in the log; the mapping is recorded as "max→xhigh".
	mappedReasoning := ""
	maxEffort := strings.TrimSpace(candidate.Channel.MaxReasoningEffort)
	// The endpoint decides too: a channel pointed straight at System One carries
	// the provider's vocabulary even when its type says New API.
	acceptedEffort := AcceptedReasoningLevels(candidate.Channel.TypeHint, channelMap.ResolvePath(effectivePath, req.Model))
	if maxEffort != "" || len(acceptedEffort) > 0 {
		if downgraded, note := downgradeReasoningEffort(requestSource, maxEffort, acceptedEffort); downgraded != nil {
			requestSource = downgraded
			mappedReasoning = note
		}
	}
	if mappedReasoning != "" {
		req.MappedReasoningEffort = mappedReasoning
	}

	// Channel-level payload rules (body rewrite chain): model/protocol/header/
	// payload conditions → set/delete/filter actions. A filter short-circuits
	// with a synthesized 403 so the channel is skipped like any other local
	// rejection (it is not an upstream health signal).
	if rulesJSON := strings.TrimSpace(candidate.Channel.PayloadRules); rulesJSON != "" {
		out, filter, err := ApplyPayloadRules(requestSource, rulesJSON, req.Model, req.DownstreamProtocol, req.Headers)
		if err != nil {
			log.Printf("proxy: payload rules channel=%d model=%s: %v", candidate.Channel.ID, req.Model, err)
		} else if filter != nil {
			rejected := &relay.Result{
				StatusCode: http.StatusForbidden,
				Err:        fmt.Errorf("%w: %s (rule %q)", ErrPayloadFiltered, filter.Reason, filter.Rule),
			}
			s.recordAttempt(*req, candidate, round, rejected, "payload_filter", "", 0)
			return nil, rejected
		} else {
			requestSource = out
		}
	}

	if aggregateUpstreamStream {
		if forced, ok := forceStreamRequestBody(requestSource); ok {
			requestSource = forced
		} else {
			// Undecodable body: fail open, drop the policy for this attempt.
			aggregateUpstreamStream = false
			upstreamStream = req.Stream
		}
	} else if synthesizeClientStream {
		if forced, ok := forceNonStreamRequestBody(requestSource); ok {
			requestSource = forced
		} else {
			synthesizeClientStream = false
			upstreamStream = req.Stream
		}
	}
	upstreamPath, requestBody, translateErr := adapter.TransformRequest(effectivePath, requestSource)
	if translateErr != nil {
		// Request conversion is local validation, not an upstream health signal.
		// Return it directly instead of retrying the same malformed request on
		// every channel.
		rejected := &relay.Result{
			StatusCode: adapterErrorStatus(translateErr, http.StatusBadRequest),
			Err:        fmt.Errorf("proxy: %s translate: %w", adapter.Name(), translateErr),
		}
		s.recordAttempt(*req, candidate, round, rejected, adapterErrorCategory(translateErr), "", 0)
		return nil, rejected
	}
	// Registered N×M translation path: the (protocol → upstream family) pair
	// exists in the matrix, so translate directly instead of going through the
	// composed adapter. The translation returns the upstream body; the
	// response/stream conversion happens via the pair's Response/Stream modes in
	// the caller's conversion block.
	if registryTranslation != nil {
		translateProto := "anthropic"
		if downstreamResponses {
			translateProto = "responses"
		}
		toPath, out, tr, ok, trErr := s.registry.Translations.Translate(translateProto, adapters.CanonicalFamily(adapter.Name()), effectivePath, requestSource)
		if trErr != nil || !ok || tr.Body == nil {
			rejected := &relay.Result{
				StatusCode: http.StatusBadRequest,
				Err:        fmt.Errorf("proxy: %s translation: %w", translateProto, trErr),
			}
			s.recordAttempt(*req, candidate, round, rejected, "translate", "", 0)
			return nil, rejected
		}
		// Channel-level system prompt injection happens on the translated
		// OpenAI-format body (same point as the composed adapter's pivot).
		if prompt := strings.TrimSpace(candidate.Channel.SystemPrompt); prompt != "" && toPath == "chat/completions" {
			out = injectSystemPrompt(out, prompt)
		}
		upstreamPath = toPath
		requestBody = out
		_ = tr
	}
	upstreamURL, err := s.resolveUpstreamURL(candidate.Channel, upstreamPath, adapter, effectiveModel)
	if err != nil {
		// URL construction is local configuration validation. Do not treat it as
		// an upstream health signal or retry it on another channel: a local
		// adapter/configuration failure must not mutate breaker, cooldown, or
		// API-key state.
		rejected := &relay.Result{Err: fmt.Errorf("proxy: %w: %v", adapters.ErrInvalidURL, err)}
		s.recordAttempt(*req, candidate, round, rejected, "invalid_url", "", 0)
		return nil, rejected
	}
	// One model, one upstream endpoint: the caller's own choice (body field or
	// payload-rule header) wins over the configured channel mapping, because the
	// caller describes the endpoint it wants while the mapping describes the
	// channel default. Decoded here, after the payload rules, so a rule that
	// targets one model can retarget its endpoint.
	overridePath, overrideURL, fieldErr := upstreamFields(requestSource, req.Headers)
	if fieldErr != nil {
		rejected := &relay.Result{StatusCode: http.StatusBadRequest, Err: fmt.Errorf("proxy: %w", fieldErr)}
		s.recordAttempt(*req, candidate, round, rejected, "invalid_url", "", 0)
		return nil, rejected
	}
	if overridePath != "" || overrideURL != "" {
		resolved, overrideErr := adapters.EndpointOverrideURL(upstreamURL, overridePath, overrideURL)
		if overrideErr != nil {
			rejected := &relay.Result{StatusCode: http.StatusBadRequest, Err: fmt.Errorf("proxy: %w", overrideErr)}
			s.recordAttempt(*req, candidate, round, rejected, "invalid_url", "", 0)
			return nil, rejected
		}
		upstreamURL = resolved
	}
	// Recorded on the log row so a relocated endpoint (channel mapping,
	// per-request override, custom path) stays attributable after the fact.
	req.UpstreamURLActual = adapters.SafeURL(upstreamURL)

	// Channel endpoint/field mapping (row-level protocol escape hatch). The path
	// resolver above already redirected the endpoint; the body maps run now so
	// the upstream receives the shape it expects. Fail-open: a malformed map is
	// logged and the body forwards unchanged.
	if !channelMap.Empty() {
		if mapped, changed, mapErr := channelMap.MapRequest(requestBody); mapErr != nil {
			log.Printf("proxy: request map channel=%d path=%s: %v", candidate.Channel.ID, effectivePath, mapErr)
			requestBody = mapped
			_ = changed
		} else if changed {
			requestBody = mapped
		}
	}
	// Plugin request hook: the upstream body and endpoint are final here, so a
	// rewrite applies to this channel's whole key/retry sequence. Channel scoped
	// by construction — a different channel speaks a different upstream shape, so
	// the plugin must see the body that will be sent.
	hookHeaders := map[string]string(nil)
	if interceptor := s.hookEnabled(); interceptor != nil {
		outcome := s.applyRequestHook(ctx, req, &candidate, upstreamURL, effectiveModel, round, requestBody, interceptor)
		if outcome != nil {
			if outcome.hasDecision {
				recordHookDecision(req, outcome.decision)
			}
			if outcome.rejected != nil {
				// A plugin rejection is a deliberate local decision, not an
				// upstream fault: return it instead of failing over.
				s.recordAttempt(*req, candidate, round, outcome.rejected, "plugin_reject", "", 0)
				return nil, outcome.rejected
			}
			if outcome.body != nil {
				requestBody = outcome.body
			}
			hookHeaders = outcome.headers
		}
	}
	return &attemptPlan{
		adapter:                 adapter,
		channelMap:              channelMap,
		protocol:                protocol,
		effectivePath:           effectivePath,
		upstreamStream:          upstreamStream,
		aggregateUpstreamStream: aggregateUpstreamStream,
		synthesizeClientStream:  synthesizeClientStream,
		upstreamURL:             upstreamURL,
		requestBody:             requestBody,
		requestSource:           requestSource,
		hookHeaders:             hookHeaders,
	}, nil
}

// normalizeAndAuthorize applies the request defaults every relay entry point
// relies on and enforces the team endpoint gate.
//
// A public channel grant delegates INFERENCE, not arbitrary use of its
// credential: a team request may only name a standard /v1 endpoint and may not
// pin or override the upstream path (via the X-Meta-Upstream-Path header or an
// upstream_path / upstream_url field). Operator profiles and payload rules run
// later on the gateway's own authority, which is why only the caller-supplied
// forms are refused here.
//
// A non-nil result means the request must not be forwarded at all.
func normalizeAndAuthorize(req *Request) *relay.Result {
	req.Model = strings.TrimSpace(req.Model)
	if len([]byte(req.Model)) > 256 {
		return &relay.Result{StatusCode: http.StatusBadRequest, Err: ErrModelTooLong}
	}
	if strings.TrimSpace(req.OpenAIPath) == "" {
		req.OpenAIPath = "chat/completions"
	}
	if strings.TrimSpace(req.Method) == "" {
		req.Method = http.MethodPost
	}
	if req.TeamAccess == nil {
		return nil
	}
	standard := map[string]bool{
		"chat/completions": true, "completions": true, "embeddings": true, "responses": true,
		"messages": true, "messages/count_tokens": true, "images/generations": true,
		"images/edits": true, "images/variations": true, "audio/speech": true,
		"audio/transcriptions": true, "audio/translations": true, "moderations": true,
	}
	pinned := false
	for name, value := range req.Headers {
		if strings.EqualFold(name, "X-Meta-Upstream-Path") && strings.TrimSpace(value) != "" {
			pinned = true
		}
	}
	if !standard[req.OpenAIPath] || pinned ||
		upstreamFieldValue(req.Body, req.Headers, "upstream_path") != "" ||
		upstreamFieldValue(req.Body, req.Headers, "upstream_url") != "" {
		return &relay.Result{StatusCode: http.StatusForbidden, Err: errors.New("team endpoint override not allowed")}
	}
	return nil
}

// Package proxy orchestrates routing, retries, upstream relay, and attempt logs.
package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/lan/meta-gateway/internal/adapters"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/outbound"
	"github.com/lan/meta-gateway/internal/relay"
	"github.com/lan/meta-gateway/internal/routing"
	"github.com/lan/meta-gateway/internal/store"
)

// replayReadCloser replays a prefix consumed for stream validation while
// preserving the upstream body's Close semantics.
type replayReadCloser struct {
	io.ReadCloser
	prefix *bytes.Reader
}

func (r *replayReadCloser) Read(p []byte) (int, error) {
	if r.prefix != nil && r.prefix.Len() > 0 {
		return r.prefix.Read(p)
	}
	return r.ReadCloser.Read(p)
}

func readResponseBody(body io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		limit = preserveBodyReadLimit
	}
	data, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%w: exceeds %d bytes", ErrResponseTooLarge, limit)
	}
	return data, nil
}

// cancelBoundBody keeps a per-attempt timeout context alive for a streaming
// response and cancels it exactly when the client closes the stream.
type cancelBoundBody struct {
	io.ReadCloser
	once     sync.Once
	cancel   context.CancelFunc
	closeErr error
}

type idleTimeoutBody struct {
	io.ReadCloser
	timeout  time.Duration
	once     sync.Once
	closeErr error
}

func (b *idleTimeoutBody) Read(p []byte) (int, error) {
	type readResult struct {
		n   int
		err error
	}
	// Read into an owned buffer. If the timeout wins, the underlying Read may
	// still be unwinding after Close; allowing that goroutine to write into the
	// caller's buffer would race with the caller reusing it for the next read.
	buffer := make([]byte, len(p))
	result := make(chan readResult, 1)
	go func() {
		n, err := b.ReadCloser.Read(buffer)
		result <- readResult{n: n, err: err}
	}()
	timer := time.NewTimer(b.timeout)
	defer timer.Stop()
	select {
	case outcome := <-result:
		if outcome.n > 0 {
			copy(p, buffer[:outcome.n])
		}
		return outcome.n, outcome.err
	case <-timer.C:
		_ = b.Close()
		return 0, fmt.Errorf("proxy: upstream stream idle for %s", b.timeout)
	}
}

func (b *idleTimeoutBody) Close() error {
	b.once.Do(func() { b.closeErr = b.ReadCloser.Close() })
	return b.closeErr
}

func (b *cancelBoundBody) Close() error {
	b.once.Do(func() {
		b.closeErr = b.ReadCloser.Close()
		if b.cancel != nil {
			b.cancel()
		}
	})
	return b.closeErr
}

func (s *Service) ChatCompletions(ctx context.Context, req Request) *relay.Result {
	result, _ := s.ForwardWithMeta(ctx, req)
	return result
}

// ChatCompletionsWithMeta is the same as ChatCompletions but also reports which upstream was used.
func (s *Service) ChatCompletionsWithMeta(ctx context.Context, req Request) (*relay.Result, *AttemptMeta) {
	return s.ForwardWithMeta(ctx, req)
}

// ForwardWithMeta selects a channel, resolves credentials, and forwards to the OpenAI-compatible path.
func (s *Service) ForwardWithMeta(ctx context.Context, req Request) (*relay.Result, *AttemptMeta) {
	req.Model = strings.TrimSpace(req.Model)
	if len([]byte(req.Model)) > 256 {
		return &relay.Result{StatusCode: http.StatusBadRequest, Err: ErrModelTooLong}, nil
	}
	if strings.TrimSpace(req.OpenAIPath) == "" {
		req.OpenAIPath = "chat/completions"
	}
	if strings.TrimSpace(req.Method) == "" {
		req.Method = http.MethodPost
	}
	// Failover state for one relay request: channels retired entirely
	// (channel-wide failure), individual alias variants retired (the upstream
	// name failed while its channel siblings may be healthy), and the two-layer
	// fallback preference — stay on the last failing channel to exhaust its
	// variants before moving to another channel. constraint is passed into every
	// round's selector call and mutated as attempts fail.
	excludedChannels := make(map[int64]struct{})
	excludedMembers := make(map[int64]struct{})
	constraint := &routing.SelectionConstraint{ExcludedMembers: excludedMembers, RouteGroup: req.RouteGroup}
	// Resolve the sticky session key: an explicit client header wins;
	// otherwise derive a content digest from the request body (stateless
	// clients get affinity through their conversation content).
	sessionKey := routing.SessionKeyFromRequest(req.Body, req.SessionKey)
	req.SessionKey = sessionKey
	stickyStore := s.sticky.Load()
	var last *relay.Result
	var lastMeta *AttemptMeta
	retrySafe := retrySafeRequest(req)
	// Route-level retry overrides tune the round counts (retry_times /
	// channel_retry_times) but cannot re-enable cross-channel failover when
	// the process default is off, bypass an admin channel pin, or skip the
	// non-idempotent-write safety gate.
	allowCrossChannelRetries := s.crossChannelFailoverEnabled.Load() && req.PreferChannelID <= 0 && retrySafe
	maxAttempts := int(s.retryTimes.Load())
	if !allowCrossChannelRetries {
		maxAttempts = 0
	}
	cooldown := time.Duration(s.cooldownNs.Load())
	if !retrySafe {
		// A lost response after a successful generation/charge must not be
		// replayed against another key or channel unless the caller supplied an
		// idempotency key that the upstream can honor.
		maxAttempts = 0
	}
	// Evaluate prompt guards once per request. Re-running them for every
	// channel retry caused repeated DB reads and could apply masking/exclusion
	// differently after the first attempt.
	var promptGuardRules []store.PromptGuardRule
	if req.OpenAIPath == "chat/completions" && s.db != nil && s.db.PromptGuard != nil {
		if guardRules, gErr := s.db.PromptGuard.ListEnabled(); gErr == nil {
			promptGuardRules = guardRules
			globalRules := promptGuardRulesForChannel(promptGuardRules, 0)
			guarded, hit, guardErr := ApplyPromptGuards(req.Body, globalRules)
			if guardErr != nil {
				log.Printf("proxy: prompt guard eval model=%s: %v", req.Model, guardErr)
			} else if hit != nil {
				switch hit.Action {
				case "reject":
					return &relay.Result{StatusCode: http.StatusBadRequest, Err: fmt.Errorf("%w: %s", ErrGuardRejected, hit.Message)}, nil
				case "exclude":
					for _, id := range hit.Exclude {
						excludedChannels[id] = struct{}{}
					}
					log.Printf("proxy: prompt guard %q excludes channels %v for request (request_id=%s)", hit.Rule, hit.Exclude, req.RequestID)
				default:
					req.Body = guarded
					log.Printf("proxy: prompt guard %q masked request body (request_id=%s)", hit.Rule, req.RequestID)
				}
			}
		}
	}
	// Route-level retry overrides: the first selection decision carries the
	// model's policy (nil = follow the global setting). The override can tune
	// the retry count only after the global failover and request-safety gates
	// have allowed cross-channel retries.
	var retryOverride *int
	var channelRetryOverride *int
	for attempt := 0; attempt <= maxAttempts; attempt++ {
		// refreshed tracks whether a 401 triggered a credential refresh for
		// this request; a successful replay is logged as refresh_retry.
		refreshed := false
		// attemptDeadlineFired records that the non-stream per-attempt timeout
		// (fwdCtx) expired — our own patience cap, after which failover stops.
		attemptDeadlineFired := false
		// monitorSkipped is set when an ignore_monitor error-passthrough rule
		// fired; the breaker/cooldown bookkeeping is skipped for that attempt.
		monitorSkipped := false
		// memberScopedFailure marks an outgoing failure as variant-specific: the
		// upstream answered for this real name while its channel siblings may
		// behave differently. The failover tail then retires only the member and
		// keeps the fallback walk on this channel instead of leaving it entirely.
		memberScopedFailure := false
		// responsesPassthroughFallback state: whether the Responses→chat
		// fallback already ran for this attempt, and the category label attached
		// when the translated retry succeeds.
		responsesFallbackTried := false
		responsesFallbackCategory := ""
		decision, err := s.selector.SelectSticky(ctx, req.Model, excludedChannels, sessionKey, constraint)
		// Persist a decision snapshot for audit: the full explanation
		// (candidates, scores, reasons, sticky/stable-first state) survives
		// even when the request later fails or the UI is long gone. Errors
		// carry whatever partial explanation the selector produced.
		if payload, marshalErr := json.Marshal(decision.Explanation); marshalErr == nil && len(payload) > 0 {
			selectedID := int64(0)
			if decision.Selected.Channel.ID > 0 {
				selectedID = decision.Selected.Channel.ID
			}
			// attempt+1 matches the proxy_logs row this selection produces, so
			// the log UI can show the decision behind EACH attempt.
			if snapErr := s.db.InsertDecisionSnapshot(req.RequestID, req.Model, decision.RouteID, selectedID, attempt+1, payload, s.now()); snapErr != nil {
				log.Printf("proxy: decision snapshot request_id=%s: %v", req.RequestID, snapErr)
			}
		}
		if err != nil {
			if last != nil {
				return last, lastMeta
			}
			return &relay.Result{Err: err}, nil
		}
		if retryOverride == nil && decision.RetryTimesOverride != nil {
			retryOverride = decision.RetryTimesOverride
			if allowCrossChannelRetries {
				// Route rows are validated by the Admin API, but clamp here as a
				// last line of defence for old/corrupt rows and non-HTTP callers.
				maxAttempts = clampInt(*retryOverride, 0, 100)
			}
		}
		if channelRetryOverride == nil && decision.ChannelRetryTimesOverride != nil {
			channelRetryOverride = decision.ChannelRetryTimesOverride
		}
		candidate := decision.Selected
		req.RouteID = decision.RouteID
		if req.PreferChannelID > 0 {
			pinned, ok := pickPreferred(decision, req.PreferChannelID)
			if !ok {
				return &relay.Result{Err: ErrPreferredChannel}, nil
			}
			candidate = pinned
		}
		modelGrayPool := decision.StableFirstOverride != nil && *decision.StableFirstOverride
		req.GrayAttempt = candidate.Channel.StableFirst && (decision.StableFirstHit || modelGrayPool)
		// Effective upstream name: members carrying {"real":"…"} rewrite the
		// client-facing alias into the channel's own name. Health bookkeeping
		// keyed on the request alias would conflate every variant sharing it —
		// blacklists below MUST use this real name instead.
		mappingJSON := strings.TrimSpace(decision.RouteMappingJSON)
		if memberMapping := strings.TrimSpace(candidate.Member.MappingJSON); memberMapping != "" {
			mappingJSON = memberMapping
		}
		effectiveModel := req.Model
		if mappingJSON != "" {
			if real := realModelFromMapping(mappingJSON); real != "" {
				effectiveModel = real
			}
		}
		// Channel-scoped rules are evaluated only once the candidate is known;
		// applying them before selection would incorrectly affect every channel.
		requestBody := req.Body
		if scopedRules := promptGuardRulesForChannel(promptGuardRules, candidate.Channel.ID); len(scopedRules) > 0 {
			guarded, hit, guardErr := ApplyPromptGuards(requestBody, scopedRules)
			if guardErr != nil {
				log.Printf("proxy: scoped prompt guard eval model=%s channel=%d: %v", req.Model, candidate.Channel.ID, guardErr)
			} else if hit != nil {
				switch hit.Action {
				case "reject":
					return &relay.Result{StatusCode: http.StatusBadRequest, Err: fmt.Errorf("%w: %s", ErrGuardRejected, hit.Message)}, nil
				case "exclude":
					excludedChannels[candidate.Channel.ID] = struct{}{}
					continue
				default:
					requestBody = guarded
				}
			}
		}
		// Model-not-found blacklist: skip a channel×real-name combination the
		// upstream permanently reported as unknown before spending an attempt.
		// Only the affected VARIANT retires — sibling names on the channel stay
		// eligible, and the fallback walk prefers their remaining ones.
		if blocked, err := s.db.IsModelBlocked(candidate.Channel.ID, effectiveModel); err == nil && blocked {
			excludedMembers[candidate.Member.ID] = struct{}{}
			constraint.PreferChannel = candidate.Channel.ID
			if last != nil && last.Body != nil {
				_ = last.Body.Close()
			}
			last = preserve(&relay.Result{StatusCode: http.StatusServiceUnavailable, Err: fmt.Errorf("%w: model %s on channel %d", ErrModelBlacklisted, effectiveModel, candidate.Channel.ID)})
			lastMeta = &AttemptMeta{
				ChannelID:   candidate.Channel.ID,
				ChannelName: candidate.Channel.Name,
				MemberID:    candidate.Member.ID,
				Priority:    candidate.Member.Priority,
				Weight:      candidate.Member.Weight,
			}
			continue
		}
		// Burst guard: reserve one in-flight slot for the whole attempt sequence
		// on this channel (all keys + retries), so the selector sees real
		// occupancy while the request is in flight.
		s.acquireChannel(candidate.Channel.ID)
		released := false
		releaseAttempt := func() {
			if released {
				return
			}
			s.releaseChannel(candidate.Channel.ID)
			released = true
		}
		defer releaseAttempt()
		meta := &AttemptMeta{
			RouteID:     decision.RouteID,
			GrayAttempt: req.GrayAttempt,
			ChannelID:   candidate.Channel.ID,
			ChannelName: candidate.Channel.Name,
			MemberID:    candidate.Member.ID,
			Priority:    candidate.Member.Priority,
			Weight:      candidate.Member.Weight,
		}
		if observer := s.liveTraceObserver.Load(); observer != nil && req.RequestID != "" {
			(*observer).Attempt(req.RequestID, attempt+1, candidate.Channel.Name, req.DownstreamProtocol, "")
		}
		gateHeld := false
		var gateGen uint64
		releaseGate := func() {
			if !gateHeld {
				return
			}
			s.gate.Release(candidate.Channel.ID, gateGen)
			gateHeld = false
		}
		defer releaseGate()
		// Hard per-channel concurrency ceiling: the slot is acquired ONCE per
		// channel attempt — outside the key/retry loops so a retryable failure
		// can never deadlock against the goroutine's own slot — and held until
		// the channel attempt ends (or, on success, handed to the response
		// body so concurrent streams are genuinely capped).
		maxConc := candidate.Channel.MaxConcurrent
		acquiredGen, gateErr := s.gate.Acquire(ctx, candidate.Channel.ID, maxConc)
		if gateErr != nil {
			return &relay.Result{Err: gateErr}, meta
		}
		gateGen = acquiredGen
		gateHeld = true
		var result *relay.Result
		var category string
		var retryable bool
		adapter := s.resolveForward(candidate.Channel)

		// Downstream protocol handling. OpenAI is the pivot contract; native
		// Anthropic and Responses clients are translated per upstream family.
		// Prefer the registered N×M translation pair; when absent, compose the
		// upstream adapter with the protocol's pivot segment.
		downstreamProto := strings.ToLower(strings.TrimSpace(req.DownstreamProtocol))
		if downstreamProto == "" {
			downstreamProto = "openai"
		}
		downstreamAnthropic := downstreamProto == "anthropic"
		downstreamResponses := downstreamProto == "responses" && req.OpenAIPath == "responses"
		var registryTranslation *adapters.Translation
		// Responses passthrough on OpenAI-compatible upstreams stays native
		// FIRST (an upstream with real /v1/responses keeps full semantics); a
		// 404/405 later falls back to a translated chat/completions retry.
		responsesPassthroughFallback := false
		if downstreamAnthropic && req.OpenAIPath == "messages" && adapter.Name() != "anthropic" {
			if tr, ok := s.registry.Translations.Lookup("anthropic", adapters.CanonicalFamily(adapter.Name())); ok && tr.Body != nil {
				registryTranslation = &tr
			} else {
				composed := adapters.ComposeDownstream(adapter, "anthropic")
				if c, ok := composed.(*adapters.ComposeForwardAdapter); ok {
					prompt := strings.TrimSpace(candidate.Channel.SystemPrompt)
					c.OnOpenAI = func(openaiBody []byte) ([]byte, error) {
						if prompt != "" {
							return injectSystemPrompt(openaiBody, prompt), nil
						}
						return openaiBody, nil
					}
				}
				adapter = composed
			}
		} else if downstreamResponses {
			family := adapters.CanonicalFamily(adapter.Name())
			if family == "openai" {
				// Native passthrough first; arm the in-place translated fallback.
				if tr, ok := s.registry.Translations.Lookup("responses", "openai"); ok && tr.Body != nil && tr.Response != nil {
					responsesPassthroughFallback = true
				}
			} else if tr, ok := s.registry.Translations.Lookup("responses", family); ok && tr.Body != nil {
				registryTranslation = &tr
			} else {
				composed := adapters.ComposeDownstream(adapter, "responses")
				if c, ok := composed.(*adapters.ComposeForwardAdapter); ok {
					prompt := strings.TrimSpace(candidate.Channel.SystemPrompt)
					c.OnOpenAI = func(openaiBody []byte) ([]byte, error) {
						if prompt != "" {
							return injectSystemPrompt(openaiBody, prompt), nil
						}
						return openaiBody, nil
					}
				}
				adapter = composed
			}
		}

		// Channel-scoped model aliases: when the matched route or the selected
		// channel's member carries a mapping_json of {"real":"…"}, clients
		// requested the alias and we must rewrite the body back to the
		// upstream's real model name. Member-level mapping wins (shared aliases
		// rewrite per channel); route-level mapping is the legacy/fallback
		// form for aliases created before per-member mappings existed.
		mappedBody := requestBody
		if mappingJSON != "" {
			mappedBody = rewriteModelName(requestBody, req.Model, mappingJSON)
		}

		effectivePath := req.OpenAIPath

		// Per-channel stream policy: override the client's stream choice for
		// this attempt. Effective only for OpenAI-shaped chat exchanges — the
		// native Anthropic passthrough and the Responses API have protocol-
		// specific stream machinery the policy does not synthesize for.
		// upstreamStream is what the upstream will actually speak: it governs
		// the non-stream budget below, while the client's choice still governs
		// the response shape the handler writes.
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
			// Channel-level system prompt injection (OpenAI-format chat bodies
			// only; translated requests are injected inside the composed
			// adapter at the pivot step).
			if prompt := strings.TrimSpace(candidate.Channel.SystemPrompt); prompt != "" && effectivePath == "chat/completions" {
				requestSource = injectSystemPrompt(requestSource, prompt)
			}
		}
		// Channel capability-aware reasoning effort downgrade: when the channel
		// declares a max_reasoning_effort and the client asked for more, rewrite
		// the body so the request succeeds instead of burning a failover round
		// (e.g. gateways that reject reasoning_effort=max). The original value is
		// kept in the log; the mapping is recorded as "max→high".
		mappedReasoning := ""
		if maxEffort := strings.TrimSpace(candidate.Channel.MaxReasoningEffort); maxEffort != "" {
			if downgraded, note := downgradeReasoningEffort(requestSource, maxEffort); downgraded != nil {
				requestSource = downgraded
				mappedReasoning = note
			}
		}
		if mappedReasoning != "" {
			req.MappedReasoningEffort = mappedReasoning
		}

		// Channel-level payload rules (body rewrite chain): model/protocol/
		// header/payload conditions → set/delete/filter actions. A filter
		// short-circuits with a synthesized 403 so the channel is skipped like
		// any other local rejection (it is not an upstream health signal).
		if rulesJSON := strings.TrimSpace(candidate.Channel.PayloadRules); rulesJSON != "" {
			out, filter, err := ApplyPayloadRules(requestSource, rulesJSON, req.Model, req.DownstreamProtocol, req.Headers)
			if err != nil {
				log.Printf("proxy: payload rules channel=%d model=%s: %v", candidate.Channel.ID, req.Model, err)
			} else if filter != nil {
				result = &relay.Result{
					StatusCode: http.StatusForbidden,
					Err:        fmt.Errorf("%w: %s (rule %q)", ErrPayloadFiltered, filter.Reason, filter.Rule),
				}
				category = "payload_filter"
				s.recordAttempt(req, candidate, attempt+1, result, category, "")
				return result, meta
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
			result = &relay.Result{
				StatusCode: adapterErrorStatus(translateErr, http.StatusBadRequest),
				Err:        fmt.Errorf("proxy: %s translate: %w", adapter.Name(), translateErr),
			}
			category = adapterErrorCategory(translateErr)
			s.recordAttempt(req, candidate, attempt+1, result, category, "")
			return result, meta
		}
		// Registered N×M translation path: the (protocol → upstream family)
		// pair exists in the matrix, so translate directly instead of going
		// through the composed adapter. The translation returns the upstream
		// body; the response/stream conversion happens via the pair's
		// Response/Stream modes inside the relay conversion block below.
		if registryTranslation != nil {
			translateProto := "anthropic"
			if downstreamResponses {
				translateProto = "responses"
			}
			toPath, out, tr, ok, trErr := s.registry.Translations.Translate(translateProto, adapters.CanonicalFamily(adapter.Name()), effectivePath, requestSource)
			if trErr != nil || !ok || tr.Body == nil {
				result = &relay.Result{
					StatusCode: http.StatusBadRequest,
					Err:        fmt.Errorf("proxy: %s translation: %w", translateProto, trErr),
				}
				s.recordAttempt(req, candidate, attempt+1, result, "translate", "")
				return result, meta
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
		upstreamURL, err := s.resolveUpstreamURL(candidate.Channel, upstreamPath, adapter)
		if err != nil {
			// URL construction is local configuration validation. Do not treat it
			// as an upstream health signal or retry it on another channel: a local
			// adapter/configuration failure must not mutate breaker, cooldown, or
			// API-key state.
			result = &relay.Result{Err: err}
			category = "invalid_url"
			result.Err = fmt.Errorf("proxy: %w: %v", adapters.ErrInvalidURL, result.Err)
			s.recordAttempt(req, candidate, attempt+1, result, category, "")
			return result, meta
		}
		// Aggregate all enabled site API keys; failover keys before leaving the channel.
		apiKeys, err := s.resolveAPIKeyPool(candidate.Channel, req.Model)
		if err != nil || len(apiKeys) == 0 {
			result = &relay.Result{Err: ErrCredential}
			category = "no_credential"
			retryable = true
			s.recordAttempt(req, candidate, attempt+1, result, category, "")
			s.recordMemberFailure(req, candidate.Member.ID, candidate.Channel.ID, req.Model, cooldown, category)

			excludedChannels[candidate.Channel.ID] = struct{}{}
			constraint.PreferChannel = 0
			last = preserve(result)
			lastMeta = meta
			releaseAttempt()
			releaseGate()
			continue
		}

	restartKeyPool:
		// A non-idempotent write must never be replayed with another site key:
		// the first upstream may have accepted/charged it before the transport
		// error reached us. Re-apply this cap at the label because a successful
		// credential refresh replaces the pool before jumping back here.
		if !retrySafe && len(apiKeys) > 1 {
			apiKeys = apiKeys[:1]
		}
		for keyIndex, apiKey := range apiKeys {
			keyRetries := int(s.channelRetryTimes.Load())
			if channelRetryOverride != nil {
				keyRetries = clampInt(*channelRetryOverride, 0, 5)
			}
			if !retrySafe {
				keyRetries = 0
			}
			for keyAttempt := 0; ; keyAttempt++ {
				headers := adapter.AuthHeaders(apiKey)
				if headers.Get("Content-Type") == "" && strings.TrimSpace(req.ContentType) != "" {
					headers.Set("Content-Type", req.ContentType)
				}
				if overrideErr := mergeHeaderOverrides(headers, candidate.Channel.HeaderOverride); overrideErr != nil {
					result = &relay.Result{Err: fmt.Errorf("proxy: header override: %w", overrideErr)}
					category, retryable = classifyForChannel(result, domain.ParseRetryConfig(candidate.Channel.RetryConfig))
					break
				}
				if idempotencyKey := requestHeader(req.Headers, "Idempotency-Key"); idempotencyKey != "" {
					headers.Set("Idempotency-Key", idempotencyKey)
				}
				// Time the upstream round trip for first-byte latency on streams.
				forwardStarted := s.now()
				// Non-streaming requests get an overall cap: an upstream that answers
				// headers and then stalls mid-body would otherwise pin this goroutine
				// until the client disconnects. Streaming requests are exempt — long
				// SSE sessions legitimately exceed any fixed budget, and their idle
				// detection is the stream path's responsibility. A channel-level
				// override serves slow upstreams (deep-reasoning models routinely
				// exceed the global five-minute budget).
				fwdCtx := ctx
				var attemptCancel context.CancelFunc
				if !upstreamStream {
					timeout := s.nonStreamTimeout
					if timeout <= 0 {
						timeout = nonStreamRequestTimeout
					}
					if seconds := candidate.Channel.NonStreamTimeoutSeconds; seconds > 0 {
						timeout = time.Duration(seconds) * time.Second
					}
					fwdCtx, attemptCancel = context.WithTimeout(ctx, timeout)
				}
				// Per-channel outbound proxy: an override on the channel routes this
				// request through it (the transport's proxy hook reads the context).
				if proxyURL := strings.TrimSpace(candidate.Channel.ProxyURL); proxyURL != "" {
					fwdCtx = outbound.WithChannelProxy(fwdCtx, proxyURL)
				}
				// (gate slot is held at the channel-attempt level, above the retry
				// loops — see the Acquire/releaseGate pair near the meta setup)
				result = s.relay.ForwardWithHeaders(fwdCtx, req.Method, upstreamURL, headers, requestBody)
				// Responses passthrough fallback: an OpenAI-compatible channel that
				// lacks a native /v1/responses surface answers 404/405 (endpoint
				// missing). Replay ONCE with the request pivoted to chat/completions;
				// the pair's Response/Stream modes below convert the upstream
				// answer back to the Responses contract.
				if responsesPassthroughFallback && !responsesFallbackTried &&
					result != nil && result.Err == nil &&
					(result.StatusCode == http.StatusNotFound || result.StatusCode == http.StatusMethodNotAllowed) {
					_ = result.Body.Close()
					translated, translateErr := adapters.ResponsesToChat(requestSource)
					if prompt := strings.TrimSpace(candidate.Channel.SystemPrompt); prompt != "" && translateErr == nil {
						translated = injectSystemPrompt(translated, prompt)
					}
					chatURL, urlErr := s.resolveUpstreamURL(candidate.Channel, "chat/completions", adapter)
					if translateErr == nil && urlErr == nil {
						fallbackTranslation, fallbackOK := s.registry.Translations.Lookup("responses", "openai")
						if fallbackOK && fallbackTranslation.Response != nil {
							responsesFallbackTried = true
							registryTranslation = &fallbackTranslation
							responsesFallbackCategory = "responses_translated"
							result = s.relay.ForwardWithHeaders(fwdCtx, req.Method, chatURL, headers, translated)
						} else {
							log.Printf("proxy: responses fallback translation missing (request_id=%s)", req.RequestID)
						}
					}
				}
				// Convert upstream 2xx bodies back to the OpenAI contract.
				if result != nil && result.Err == nil && result.StatusCode >= 200 && result.StatusCode < 300 && result.Body != nil {
					// Stream policy: fold a forced upstream stream into one
					// completion, or expand a forced non-stream answer into a
					// single-chunk SSE replay — both before the protocol
					// translation below, which then sees the shape it expects.
					if aggregateUpstreamStream {
						aggregated, aggErr := aggregateChatStream(result.Body)
						_ = result.Body.Close()
						if aggErr != nil {
							result = &relay.Result{Header: result.Header, LatencyMs: result.LatencyMs, Err: fmt.Errorf("stream aggregation failed: %w", aggErr)}
						} else {
							result.Body = io.NopCloser(bytes.NewReader(aggregated))
							if result.Header == nil {
								result.Header = make(http.Header)
							}
							result.Header.Set("Content-Type", "application/json")
						}
					} else if synthesizeClientStream {
						raw, readErr := io.ReadAll(result.Body)
						_ = result.Body.Close()
						var synthesized []byte
						if readErr == nil {
							synthesized, readErr = synthesizeStreamFromCompletion(raw)
						}
						if readErr != nil {
							result = &relay.Result{Header: result.Header, LatencyMs: result.LatencyMs, Err: fmt.Errorf("stream synthesis failed: %w", readErr)}
						} else {
							result.Body = io.NopCloser(bytes.NewReader(synthesized))
							if result.Header == nil {
								result.Header = make(http.Header)
							}
							result.Header.Set("Content-Type", "text/event-stream")
						}
					}
					// N×M matrix path: the (anthropic → family) pair's Response/Stream
					// modes convert upstream output back to the Anthropic contract.
					if registryTranslation != nil {
						if req.Stream && registryTranslation.Stream != nil {
							wrapped, wrapErr := registryTranslation.Stream(effectivePath, result.Body)
							if wrapErr != nil {
								_ = result.Body.Close()
								result = &relay.Result{Header: result.Header, LatencyMs: result.LatencyMs, Err: wrapErr}
							} else {
								result.Body = wrapped
								if result.Header == nil {
									result.Header = make(http.Header)
								}
								if !isBinaryResponsePath(effectivePath) {
									result.Header.Set("Content-Type", "text/event-stream")
								}
							}
						} else if !req.Stream && registryTranslation.Response != nil {
							raw, readErr := readResponseBody(result.Body, preserveBodyReadLimit)
							_ = result.Body.Close()
							if readErr != nil {
								result = &relay.Result{StatusCode: result.StatusCode, Header: result.Header, LatencyMs: result.LatencyMs, Err: readErr}
							} else if converted, convErr := registryTranslation.Response(effectivePath, raw); convErr != nil {
								result = &relay.Result{
									StatusCode: adapterErrorStatus(convErr, http.StatusBadGateway),
									Header:     result.Header,
									LatencyMs:  result.LatencyMs,
									Err:        fmt.Errorf("proxy: anthropic response: %w", convErr),
								}
							} else {
								result.Body = io.NopCloser(bytes.NewReader(converted))
								if result.Header == nil {
									result.Header = make(http.Header)
								}
								result.Header.Set("Content-Type", transformedContentType(effectivePath, adapter.Name(), result.Header.Get("Content-Type")))
							}
						}
					} else if req.Stream {
						// Reshape native/upstream SSE into the downstream contract (the
						// composed adapter pivots through OpenAI SSE internally).
						wrapped, wrapErr := adapter.WrapStream(effectivePath, result.Body)
						if wrapErr != nil {
							// The upstream stream is not handed to the client; close it so
							// the connection returns to the pool instead of leaking.
							_ = result.Body.Close()
							result = &relay.Result{Header: result.Header, LatencyMs: result.LatencyMs, Err: wrapErr}
						} else {
							result.Body = wrapped
							if result.Header == nil {
								result.Header = make(http.Header)
							}
							if !isBinaryResponsePath(effectivePath) {
								result.Header.Set("Content-Type", "text/event-stream")
							}
						}
					} else {
						raw, readErr := readResponseBody(result.Body, preserveBodyReadLimit)
						_ = result.Body.Close()
						if readErr != nil {
							result = &relay.Result{StatusCode: result.StatusCode, Header: result.Header, LatencyMs: result.LatencyMs, Err: readErr}
						} else if converted, convErr := adapter.TransformResponse(effectivePath, raw); convErr != nil {
							result = &relay.Result{
								StatusCode: adapterErrorStatus(convErr, http.StatusBadGateway),
								Header:     result.Header,
								LatencyMs:  result.LatencyMs,
								Err:        fmt.Errorf("proxy: %s response: %w", adapter.Name(), convErr),
							}
						} else {
							// Empty-success check: a 2xx chat completion with no
							// choices or an empty message is a silent upstream
							// failure — fail over instead of returning emptiness.
							if effectivePath == "chat/completions" && isEmptyChatSuccess(converted) {
								result = &relay.Result{
									StatusCode: result.StatusCode,
									Header:     result.Header,
									LatencyMs:  result.LatencyMs,
									Err:        ErrEmptyCompletion,
								}
							} else {
								result.Body = io.NopCloser(bytes.NewReader(converted))
								if result.Header == nil {
									result.Header = make(http.Header)
								}
								result.Header.Set("Content-Type", transformedContentType(effectivePath, adapter.Name(), result.Header.Get("Content-Type")))
							}
						}
					}
				}
				streamInterrupted := false
				if req.Stream && result.Err == nil && result.StatusCode >= 200 && result.StatusCode < 300 {
					first, silent, peekErr := peekStreamStartWithTimeout(result.Body, streamFirstByteTimeout)
					switch {
					case peekErr != nil:
						// Upstream answered 200 and then died before emitting any
						// data. The client has not received a byte yet, so this is a
						// normal retryable failure — fail over to the next key/channel.
						_ = result.Body.Close()
						result = &relay.Result{
							StatusCode: result.StatusCode,
							Header:     result.Header,
							LatencyMs:  result.LatencyMs,
							Err:        fmt.Errorf("proxy: stream closed before first byte: %w", peekErr),
						}
						streamInterrupted = true
					case silent:
						// A 200 stream that ended without delivering any content
						// frame ([DONE]/EOF after only role/usage frames) is a
						// silent failure — the client would receive an empty
						// response. Treat it like a first-byte death and fail over.
						_ = result.Body.Close()
						result = &relay.Result{
							StatusCode: result.StatusCode,
							Header:     result.Header,
							LatencyMs:  result.LatencyMs,
							Err:        fmt.Errorf("proxy: stream ended silently before any content: %w", ErrEmptyCompletion),
						}
						streamInterrupted = true
					default:
						// Replay the buffered prefix, then continue streaming the rest.
						result.Body = &replayReadCloser{
							ReadCloser: result.Body,
							prefix:     bytes.NewReader(first),
						}
						result.Body = &idleTimeoutBody{ReadCloser: result.Body, timeout: streamIdleTimeout}
						// First-byte latency measured from relay start to the first
						// upstream byte (the peek above consumed it).
						result.FirstByteMs = int(s.now().Sub(forwardStarted).Milliseconds())
					}
				}
				if attemptCancel != nil {
					if result != nil && result.Err == nil && result.Body != nil {
						result.Body = &cancelBoundBody{ReadCloser: result.Body, cancel: attemptCancel}
					} else {
						// Capture the cap state before the cancel wipes fwdCtx.Err().
						if errors.Is(fwdCtx.Err(), context.DeadlineExceeded) {
							attemptDeadlineFired = true
						}
						attemptCancel()
					}
					attemptCancel = nil
				}
				category, retryable = classifyForChannel(result, domain.ParseRetryConfig(candidate.Channel.RetryConfig))
				// A successful Responses→chat fallback retry carries its own
				// category so logs show the protocol pivot instead of a plain 200.
				if responsesFallbackCategory != "" && result != nil && result.Err == nil &&
					result.StatusCode >= 200 && result.StatusCode < 300 {
					category = responsesFallbackCategory
				}
				// A transport-level timeout (outbound header/TLS timeout) surfaces
				// as context.DeadlineExceeded even though the client request
				// context is still alive and waiting. Reclassify it as a retryable
				// transport failure so the failover walk can try a faster channel;
				// only our own per-attempt cap (attemptDeadlineFired) or a dead
				// request context keeps the terminal "cancelled" semantics.
				if errors.Is(result.Err, context.DeadlineExceeded) && ctx.Err() == nil && !attemptDeadlineFired {
					category = "transport"
					retryable = true
				}
				localAdapterFailure := isLocalFailure(result.Err)
				// 401 refresh-retry: an expired session/access-token credential is
				// re-established through the check-in machinery exactly once per
				// request, then the request is replayed from the first key in the
				// refreshed pool. A successful replay is logged as refresh_retry.
				if !refreshed && !localAdapterFailure && result.Err == nil && result.StatusCode == http.StatusUnauthorized && s.credentialRefresher != nil {
					if s.refreshCredentialAndReplay(ctx, &candidate, &apiKeys, req.Model) {
						if result.Body != nil {
							_ = result.Body.Close()
							result.Body = nil
						}
						refreshed = true
						category = "refresh_retry"
						// Restart the range from the fresh, model-filtered pool without
						// consuming a channel retry budget.
						goto restartKeyPool
					}
				}
				if streamInterrupted && !localAdapterFailure {
					// The silent-empty variant keeps the finer empty_response
					// category; plain first-byte deaths read as interrupted.
					if !errors.Is(result.Err, ErrEmptyCompletion) {
						category = "stream_interrupted"
					}
					retryable = true
				}
				// The channel consecutive-failure counter is incremented exactly once
				// per failed attempt (inside recordMemberFailure below); counting here
				// too would over-count a multi-key pool (3 keys + member = 4) and
				// trip auto-disable prematurely.
				if result.Err == nil && !retryable {
					break
				}
				if !retryable {
					// Local adapter/configuration errors do not heal by re-sending;
					// move to the next key (the channel-level branch decides failover).
					break
				}
				if errors.Is(result.Err, context.Canceled) || errors.Is(result.Err, context.DeadlineExceeded) || ctx.Err() != nil {
					break
				}
				// Retryable transport/business failures are re-sent on the same key
				// up to channelRetryTimes. Once exhausted, the key pool and then the
				// configured cross-channel retry rounds continue normally.
				if keyAttempt >= keyRetries {
					break
				}
				if result.Body != nil {
					_ = result.Body.Close()
				}
			}
			// Only the last key attempt for this channel is logged at the attempt counter
			// used for cross-channel retry; intermediate key fails stay on the same attempt.
			// A successful post-refresh replay keeps the refresh_retry attribution.
			if refreshed && result.Err == nil && result.StatusCode >= 200 && result.StatusCode < 300 {
				category = "refresh_retry"
			}
			if keyIndex == len(apiKeys)-1 || (result.Err == nil && !retryable) {
				s.recordAttempt(req, candidate, attempt+1, result, category, keyFingerprint(apiKey))
			}
			if result.Err == nil && !retryable {
				break
			}
			if errors.Is(result.Err, context.Canceled) || errors.Is(result.Err, context.DeadlineExceeded) || ctx.Err() != nil {
				break
			}
			if result.Body != nil {
				_ = result.Body.Close()
			}
		}
		// Keep the soft in-flight count for every key and retry on this channel;
		// release only after the complete channel attempt has been exhausted.
		releaseAttempt()
		if result == nil {
			return &relay.Result{Err: ErrCredential}, meta
		}

		// Success is a 2xx upstream result. A 4xx client error is NOT success:
		// it falls through to the failover branch below so the next channel
		// gets a chance (channel capabilities are heterogeneous).
		if result.Err == nil && result.StatusCode >= 200 && result.StatusCode < 300 {
			if last != nil && last.Body != nil {
				_ = last.Body.Close()
			}
			// A probe success is the answer the operator asked for, not a health
			// signal: member cooldown reset, channel counter, error decay, and
			// latency sampling stay untouched so probe traffic never looks like
			// real traffic to the bookkeeping.
			if !req.Probe {
				if err := s.db.RouteMember.RecordSuccess(candidate.Member.ID, s.now()); err != nil {
					log.Printf("proxy: record success member_id=%d: %v", candidate.Member.ID, err)
				}
				s.decayError(candidate.Channel.ID, req.Model)
				s.recordMemberSuccess(candidate.Channel.ID)
				s.resetTransportFails(candidate.Member.ID)
				if s.latencyAware.Load() && result.LatencyMs > 0 {
					s.observeLatency(candidate.Channel.ID, req.Model, result.LatencyMs)
				}
			}
			// Bind the successful relay to its session key so the next request
			// of the same conversation prefers this channel (prompt-cache and
			// multi-turn continuity). Admin-pinned probes never bind.
			if stickyStore != nil && sessionKey != "" && req.PreferChannelID == 0 {
				stickyStore.Bind(sessionKey, candidate.Channel.ID, s.now())
			}
			// Hand the gate slot to the response body: the hard ceiling now
			// covers the full stream lifetime (released when the handler closes
			// the body), not just the header phase. The relay handler always
			// closes the body it copies, so non-stream bodies release too.
			if gateHeld && result.Body != nil {
				body := result.Body
				// Direct release (not the guarded releaseGate): the guard is
				// for the defer/early-exit paths; ownership has moved to the
				// body wrapper, which fires exactly once via sync.Once.
				result.Body = &gateBoundBody{ReadCloser: body, release: func() { s.gate.Release(candidate.Channel.ID, gateGen) }}
				gateHeld = false
			}
			return result, meta
		}
		// A dead request context (client gone, request deadline) or our own
		// per-attempt patience cap ends the walk. Transport-level timeouts with
		// a still-waiting client were reclassified above and keep failing over.
		if ctx.Err() != nil || errors.Is(result.Err, context.Canceled) || attemptDeadlineFired {
			return result, meta
		}
		// Configurable error passthrough rules (error_passthrough_rules)
		// override the default 4xx behavior for ALL 4xx responses — including
		// retryable ones (429 rate limits are the classic case). Rules are
		// read live on every request so edits apply instantly.
		//   passthrough     → return the upstream error, no failover/cooldown
		//   rewrite         → passthrough with a rewritten status code
		//   ignore_monitor  → keep failover, skip breaker/cooldown/counters
		if result.Err == nil && result.StatusCode >= 400 && result.StatusCode < 500 {
			errorText := modelNotFoundText(result)
			rule, mErr := s.db.ErrorRule.MatchErrorRule(result.StatusCode, errorText, req.Model, candidate.Channel.ID)
			if mErr == nil && rule != nil {
				switch rule.Action {
				case store.ErrorRulePassthrough, store.ErrorRuleRewrite:
					if rule.Action == store.ErrorRuleRewrite && rule.RewriteTo >= 100 && rule.RewriteTo <= 599 {
						result.StatusCode = rule.RewriteTo
					}
					category = "error_rule_" + rule.Action
					s.recordAttempt(req, candidate, attempt+1, result, category, "")
					log.Printf("proxy: error rule %q passthrough status=%d model=%s channel=%d (request_id=%s)", rule.Name, result.StatusCode, req.Model, candidate.Channel.ID, req.RequestID)
					return result, meta
				case store.ErrorRuleIgnoreMonitor:
					monitorSkipped = true
					log.Printf("proxy: error rule %q ignore-monitor status=%d model=%s channel=%d (request_id=%s)", rule.Name, result.StatusCode, req.Model, candidate.Channel.ID, req.RequestID)
				}
			}
		}
		if retryable {
			// 429/5xx take the fixed cooldown (extended by Retry-After).
			// Transport failures (refused/TLS/timeout) get the jitter
			// exemption: the first failure of a consecutive streak earns no
			// cooldown, a repeat does — the channel never silently keeps
			// eating full timeouts on every request. ignore_monitor rules
			// skip bookkeeping entirely.
			if !monitorSkipped && !req.Probe {
				penalty := retryAfterCooldown(result.Header, s.now(), cooldown)
				if category == "transport" {
					penalty = s.transportPenalty(candidate.Member.ID, cooldown)
					s.observeTransportFailure(candidate.Member.ID)
				}
				s.recordMemberFailure(req, candidate.Member.ID, candidate.Channel.ID, req.Model, penalty, category)
			}
			// Two-layer fallback scope: an upstream that ANSWERED (any status,
			// or a stream that died mid-flight — surfaced as the
			// stream_interrupted category) speaks for this name's account;
			// retire the variant and keep walking the channel's other names.
			// No HTTP response at all (connection refused, TLS, DNS) or an auth
			// failure applies to everything on the channel.
			switch {
			case category == "stream_interrupted":
				memberScopedFailure = true
			case errors.Is(result.Err, ErrEmptyCompletion):
				// An empty 2xx (body or stream) still speaks for this name's
				// account — retire the variant, keep walking this channel.
				memberScopedFailure = true
			case result.Err == nil && result.StatusCode >= 400 &&
				result.StatusCode != http.StatusUnauthorized && result.StatusCode != http.StatusForbidden:
				memberScopedFailure = true
			}
			if req.PreferChannelID > 0 {
				return result, meta
			}
		} else if result.Err == nil && result.StatusCode >= 400 && result.StatusCode < 500 {
			// A model_not_found / unknown-model response is permanent for this
			// channel × real-name combination. Detected once here: the blacklist
			// write below keys on the EFFECTIVE (mapped) name so sibling
			// variants sharing the alias stay untouched, and the failover tail
			// retires only this member even when monitor bookkeeping is skipped.
			notFound := isModelNotFoundError(result.StatusCode, modelNotFoundText(result))
			if notFound {
				memberScopedFailure = true
			}
			// 4xx client error: fail over to the next channel — a different
			// upstream may accept the same request (channel capabilities are
			// heterogeneous: one gateway may reject reasoning_effort=max while
			// another accepts it). Cool the member down so this channel is
			// skipped for a while, but do NOT count toward the channel's
			// consecutive-failure tally or auto-disable: the request itself may
			// be at fault, and counting would let a bad client request take
			// down every channel via repeated 4xx failover.
			if !monitorSkipped {
				if notFound {
					// The blacklist write stays probe-visible: a probe discovering
					// a dead model name on a channel is exactly what the blacklist
					// exists to record.
					if err := s.db.BlockModel(candidate.Channel.ID, effectiveModel, category); err != nil {
						log.Printf("proxy: block model channel=%d model=%s: %v", candidate.Channel.ID, effectiveModel, err)
					}
					log.Printf("proxy: model %s not found on channel %d — blacklisted (request_id=%s)", effectiveModel, candidate.Channel.ID, req.RequestID)
				}
				// Probes are pinned synthetic traffic: a 4xx here is information,
				// not a fault, so member cooldown and error bookkeeping stay
				// untouched (recordMemberFailure guards the same way on the
				// retryable path).
				if !req.Probe {
					if s.faultProtectionEnabled.Load() {
						if err := s.db.RouteMember.RecordFailure(candidate.Member.ID, s.now(), cooldown, category); err != nil {
							log.Printf("proxy: record 4xx member failure member_id=%d: %v", candidate.Member.ID, err)
						}
					}
					s.observeError(candidate.Channel.ID, req.Model)
				}
			}
			if req.PreferChannelID > 0 {
				return result, meta
			}
			goto nextCandidate
		} else {
			// Local adapter/configuration errors and non-4xx client errors are
			// returned directly: the same adapter logic would fail identically
			// on every channel, so failover cannot help.
			return result, meta
		}

	nextCandidate:
		if memberScopedFailure {
			excludedMembers[candidate.Member.ID] = struct{}{}
			constraint.PreferChannel = candidate.Channel.ID
		} else {
			excludedChannels[candidate.Channel.ID] = struct{}{}
			constraint.PreferChannel = 0
		}
		if last != nil && last.Body != nil {
			_ = last.Body.Close()
		}
		last = preserve(result)
		lastMeta = meta
		releaseGate()
		if result.Body != nil {
			_ = result.Body.Close()
		}
	}
	if last != nil {
		return last, lastMeta
	}
	return &relay.Result{Err: routing.ErrNoEligible}, lastMeta
}

// refreshCredentialAndReplay re-establishes the channel's session-style
// credential (kind session/access_token) after an upstream 401, then
// re-resolves the key pool so the replay uses the refreshed secret. Reports
// whether the refresh succeeded AND the pool was replaced.
func (s *Service) refreshCredentialAndReplay(ctx context.Context, candidate *domain.RoutingCandidate, apiKeys *[]string, model string) bool {
	if candidate == nil || candidate.Channel.CredentialID == nil || *candidate.Channel.CredentialID <= 0 {
		return false
	}
	cred, err := s.db.Credential.GetByID(*candidate.Channel.CredentialID)
	if err != nil || cred == nil {
		return false
	}
	kind := strings.ToLower(strings.TrimSpace(cred.Kind))
	if kind != "session" && kind != "access_token" {
		return false
	}
	ok, refreshErr := s.credentialRefresher.RefreshForRelay(ctx, cred.ID)
	if refreshErr != nil {
		log.Printf("proxy: 401 refresh credential=%d channel=%d: %v", cred.ID, candidate.Channel.ID, refreshErr)
		return false
	}
	if !ok {
		return false
	}
	freshKeys, err := s.resolveAPIKeyPool(candidate.Channel, model)
	if err != nil || len(freshKeys) == 0 {
		return false
	}
	*apiKeys = freshKeys
	log.Printf("proxy: 401 refreshed credential=%d channel=%d, replaying (request_id=%s)", cred.ID, candidate.Channel.ID, "")
	return true
}

func pickPreferred(decision routing.Decision, channelID int64) (domain.RoutingCandidate, bool) {
	for _, evaluation := range decision.Candidates {
		if evaluation.Eligible && evaluation.Candidate.Channel.ID == channelID {
			return evaluation.Candidate, true
		}
	}
	return domain.RoutingCandidate{}, false
}

func isBinaryResponsePath(path string) bool {
	return strings.EqualFold(strings.Trim(path, "/"), "audio/speech")
}

func transformedContentType(path, adapterName, upstream string) string {
	if isBinaryResponsePath(path) {
		if strings.TrimSpace(upstream) != "" {
			return upstream
		}
		return "application/octet-stream"
	}
	// OpenAI-compatible passthrough may expose a non-JSON media type for
	// future binary endpoints; do not overwrite it during the no-op transform.
	if adapterName == "openai-compatible" && strings.TrimSpace(upstream) != "" {
		media := strings.ToLower(strings.TrimSpace(strings.SplitN(upstream, ";", 2)[0]))
		if media != "application/json" && media != "text/json" {
			return upstream
		}
	}
	return "application/json"
}

func clampInt(value, minValue, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func requestHeader(headers map[string]string, name string) string {
	for key, value := range headers {
		if strings.EqualFold(key, name) {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func retrySafeRequest(req Request) bool {
	method := strings.ToUpper(strings.TrimSpace(req.Method))
	if method != http.MethodPost && method != http.MethodPut && method != http.MethodPatch {
		return true
	}
	if requestHeader(req.Headers, "Idempotency-Key") != "" {
		return true
	}
	path := strings.Trim(strings.ToLower(req.OpenAIPath), "/")
	for _, prefix := range []string{"images/", "audio/"} {
		if strings.HasPrefix(path, prefix) {
			return false
		}
	}
	if path == "responses" || strings.HasPrefix(path, "responses/") {
		return false
	}
	return true
}

func (s *Service) resolveUpstreamURL(channel domain.Channel, apiPath string, adapter adapters.ForwardAdapter) (string, error) {
	baseURL := strings.TrimSpace(channel.BaseURL)
	if baseURL == "" {
		if channel.SiteID == nil {
			return "", fmt.Errorf("proxy: channel base url unavailable")
		}
		site, err := s.db.Site.GetByID(*channel.SiteID)
		if err != nil || site == nil || strings.TrimSpace(site.BaseURL) == "" {
			return "", fmt.Errorf("proxy: channel base url unavailable")
		}
		baseURL = site.BaseURL
	}
	upstreamURL, err := adapter.BuildUpstreamURL(baseURL, apiPath)
	if err != nil {
		// Preserve the adapter's concrete reason for logs/tests while marking
		// this as a local configuration failure so retry and health accounting
		// never treat it as an upstream outage.
		return "", fmt.Errorf("%w: %v", adapters.ErrInvalidURL, err)
	}
	return upstreamURL, nil
}

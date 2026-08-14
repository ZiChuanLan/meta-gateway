// Package proxy orchestrates routing, retries, upstream relay, and attempt logs.
package proxy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lan/meta-gateway/internal/adapters"
	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/outbound"
	"github.com/lan/meta-gateway/internal/relay"
	"github.com/lan/meta-gateway/internal/routing"
	"github.com/lan/meta-gateway/internal/store"
	"github.com/lan/meta-gateway/internal/usage"
	"github.com/lan/meta-gateway/internal/webhook"
)

var (
	ErrCredential       = errors.New("proxy: upstream credential unavailable")
	ErrPreferredChannel = errors.New("proxy: preferred channel not eligible")
	// ErrModelBlacklisted marks a request whose only remaining candidate
	// reported this model as not found; the relay surfaces it as 503.
	ErrModelBlacklisted = errors.New("proxy: model blacklisted on channel")
	// ErrPayloadFiltered marks a request rejected by a channel payload rule's
	// filter action; the relay surfaces it as 403 with the rule reason.
	ErrPayloadFiltered = errors.New("proxy: request filtered by payload rule")
	// ErrGuardRejected marks a request refused by a sensitive-prompt guard
	// rule; the relay surfaces it as 400 with the policy message.
	ErrGuardRejected = errors.New("proxy: request rejected by prompt guard")
)

type Selector interface {
	Select(ctx context.Context, model string, excluded map[int64]struct{}) (routing.Decision, error)
	// SelectSticky is Select with an optional session key for affinity routing.
	SelectSticky(ctx context.Context, model string, excluded map[int64]struct{}, sessionKey string) (routing.Decision, error)
	// SetConcurrencyAware wires the in-flight burst guard into scoring.
	SetConcurrencyAware(enabled bool, limit int, provider routing.ConcurrencyProvider)
}

// CredentialRefresher re-establishes an expired session/access-token
// credential (the check-in machinery). The proxy calls it once per request
// when an upstream answers 401 and the channel's credential kind supports
// refreshing, then replays the request with the refreshed credential.
type CredentialRefresher interface {
	// RefreshForRelay performs one refresh pass; ok reports whether the
	// credential was re-established (checkin StatusSuccess).
	RefreshForRelay(ctx context.Context, credentialID int64) (ok bool, err error)
}
type Relay interface {
	ChatCompletionsContext(ctx context.Context, upstreamURL, apiKey string, body []byte, stream bool) *relay.Result
	ForwardContext(ctx context.Context, method, upstreamURL, apiKey string, body []byte) *relay.Result
	ForwardWithHeaders(ctx context.Context, method, upstreamURL string, headers http.Header, body []byte) *relay.Result
}

type Service struct {
	selector                    Selector
	relay                       Relay
	db                          *store.DB
	enc                         *crypto.Encrypter
	retryTimes                  atomic.Int64
	channelRetryTimes           atomic.Int64
	crossChannelFailoverEnabled atomic.Bool
	keyPoolRotation             atomic.Bool
	cooldownNs                  atomic.Int64
	now                         func() time.Time
	registry                    *adapters.Registry
	// breaker is the model-level circuit breaker (channel × model).
	breaker *ModelCircuitBreaker
	// keyErrCounts tracks per channel × api-key × status-code consecutive
	// failures (AxonHub-style triple-key counting); a key that hits the
	// auto-disable threshold is temporarily excluded from the pool.
	keyErrMu     sync.Mutex
	keyErrCounts map[int64]map[string]map[int]int
	// disabledKeys scopes the exclusion per channel (a key is only lifted by a
	// success on the same channel that disabled it).
	disabledKeys map[disabledKey]time.Time
	// autoDisableThreshold: consecutive member failures before a channel is
	// auto-disabled (0 = feature off).
	autoDisableThreshold atomic.Int64
	// keyFailThreshold: consecutive failures (per channel × key × status)
	// before an upstream API key is excluded from the pool (0 = feature off).
	keyFailThreshold atomic.Int64
	// latencyAware enables latency-weighted channel picking.
	latencyAware atomic.Bool
	latencyMu    sync.Mutex
	latencyEMA   map[channelModel]float64
	errorMu      sync.Mutex
	errorEMA     map[channelModel]float64
	// sticky is the optional session-affinity store; nil disables sticky routing.
	sticky atomic.Pointer[routing.StickyStore]
	// grayPromoteRequests is the stable-first promotion threshold (successful
	// grayscale relay attempts before the channel graduates; 0 disables).
	grayPromoteRequests atomic.Int64
	// inflight counts in-flight relays per channel for the burst guard.
	inflightMu sync.Mutex
	inflight   map[int64]int
	// concurrencyAware enables the burst guard (selector-side scoring);
	// concurrencyLimit is the per-channel ceiling.
	concurrencyAware atomic.Bool
	concurrencyLimit atomic.Int64
	// notifier delivers operational webhooks (auto-disable/recovery).
	notifier *webhook.Notifier
	// credentialRefresher re-establishes expired session credentials on 401
	// (see CredentialRefresher). nil disables the refresh-retry path.
	credentialRefresher CredentialRefresher
	// gate is the hard per-channel concurrency limiter (FIFO wait queue).
	gate *ChannelGate
	// nonStreamTimeout caps a non-streaming upstream attempt (request + body
	// read); 0 uses the package default nonStreamRequestTimeout. Streaming
	// requests are exempt. Injectable for tests.
	nonStreamTimeout time.Duration
}

// channelModel scopes adaptive latency/error EWMA to one channel on one model,
// so a slow or failing route on model X never drags down the same channel on
// model Y.
type channelModel struct {
	channelID int64
	model     string
}

// disabledKey identifies a per-channel key exclusion (channelID + fingerprint)
// so one channel's auto-disable is never lifted by another channel's success.
type disabledKey struct {
	channelID int64
	fp        string
}

type Request struct {
	RequestID string
	Model     string
	Body      []byte
	Stream    bool
	Method    string
	// OpenAIPath is the path under the upstream OpenAI root, e.g. "chat/completions".
	OpenAIPath string
	// DownstreamProtocol is the client-side wire contract: "openai" (default)
	// or "anthropic" (native /v1/messages clients). Non-anthropic channels
	// translate the request/response when "anthropic" is set.
	DownstreamProtocol string
	// Headers carries the client request headers (canonical keys) so payload
	// rules can match on header conditions. Nil when the relay did not supply
	// them (admin try / internal callers) — header rules simply won't fire.
	Headers map[string]string
	// PreferChannelID pins upstream selection (admin try). Zero means normal routing.
	PreferChannelID int64
	// DownstreamKeyID is the authenticated client key, used for usage metering.
	DownstreamKeyID int64
	// ContentType preserves client Content-Type for multipart passthrough.
	ContentType string
	// SessionKey is an explicit sticky-session identifier from the client
	// (e.g. X-Meta-Session-Id). When empty, the gateway derives a content
	// digest session key from the request body.
	SessionKey string
	// ReasoningEffort is the client-requested OpenAI-style reasoning effort
	// (low / medium / high / max / xhigh) for observability logging.
	ReasoningEffort string
	// MappedReasoningEffort records a capability downgrade ("max→high") applied
	// for the serving channel. Empty = unchanged.
	MappedReasoningEffort string
	// PromptTokens/CompletionTokens/TotalTokens optional post-response accounting.
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// AttemptMeta describes which upstream was used for an admin try / last attempt.
type AttemptMeta struct {
	ChannelID   int64  `json:"channel_id"`
	ChannelName string `json:"channel_name"`
	MemberID    int64  `json:"member_id"`
	Priority    int    `json:"priority"`
	Weight      int    `json:"weight"`
}

func New(selector Selector, upstream Relay, db *store.DB, enc *crypto.Encrypter, retryTimes int, cooldown time.Duration) *Service {
	if retryTimes < 0 {
		retryTimes = 0
	}
	if cooldown < 0 {
		cooldown = 0
	}
	service := &Service{selector: selector, relay: upstream, db: db, enc: enc, now: time.Now}
	service.breaker = NewModelCircuitBreaker(0)
	service.keyErrCounts = make(map[int64]map[string]map[int]int)
	service.disabledKeys = make(map[disabledKey]time.Time)
	service.retryTimes.Store(int64(retryTimes))
	service.channelRetryTimes.Store(1)
	service.crossChannelFailoverEnabled.Store(true)
	service.keyPoolRotation.Store(true)
	service.keyFailThreshold.Store(5)
	service.cooldownNs.Store(int64(cooldown))
	service.gate = NewChannelGate()
	return service
}

// SetAutoDisableThreshold enables channel auto-disable after N consecutive
// member failures (0 disables).
func (s *Service) SetAutoDisableThreshold(n int) {
	s.autoDisableThreshold.Store(int64(n))
}

// SetBreakerFailCount hot-applies the model circuit breaker's open threshold
// (MODEL_BREAKER_FAIL_COUNT / runtime settings). 0 disables the memory
// breaker; negative values reset to the default. Nil breaker (tests) is a
// no-op.
func (s *Service) SetBreakerFailCount(n int) {
	if s.breaker != nil {
		s.breaker.SetOpenThreshold(n)
	}
}

// CircuitWeight exposes the live channel×model breaker multiplier to the
// routing selector and Admin explanation endpoint. It is intentionally
// read-only; recovery still happens only through a successful probe/relay.
func (s *Service) CircuitWeight(channelID int64, model string) float64 {
	if s == nil || s.breaker == nil {
		return 1
	}
	return s.breaker.EffectiveWeight(channelID, model, 1)
}

// SetKeyFailThreshold hot-applies the per-key failure threshold (0 disables
// key auto-exclusion).
func (s *Service) SetKeyFailThreshold(n int) {
	s.keyFailThreshold.Store(int64(n))
}

// SetStableFirstPromote configures the grayscale promotion threshold: after
// that many successful relay attempts on a stable-first channel (with no
// consecutive failures), the channel is promoted. 0 disables promotion.
func (s *Service) SetStableFirstPromote(n int) {
	s.grayPromoteRequests.Store(int64(n))
}

// SetWebhookNotifier installs the operational webhook notifier (nil disables).
func (s *Service) SetWebhookNotifier(notifier *webhook.Notifier) {
	s.notifier = notifier
}
// SetCredentialRefresher wires the 401 session-refresh hook (check-in
// service). Optional: without it, 401s follow the normal failover path.
func (s *Service) SetCredentialRefresher(refresher CredentialRefresher) {
	s.credentialRefresher = refresher
}

func (s *Service) SetConcurrencyAware(enabled bool, limit int) {
	s.concurrencyLimit.Store(int64(limit))
	s.concurrencyAware.Store(enabled && limit > 0)
	if enabled && limit > 0 {
		s.selector.SetConcurrencyAware(true, limit, s.Inflight)
	} else {
		s.selector.SetConcurrencyAware(false, 0, nil)
	}
}

// Inflight returns the number of relay attempts currently occupying a channel.
func (s *Service) Inflight(channelID int64) int {
	s.inflightMu.Lock()
	defer s.inflightMu.Unlock()
	return s.inflight[channelID]
}

// acquireChannel reserves one in-flight slot for the channel; releaseChannel
// returns it. The slot is held for the whole attempt sequence (keys + retries)
// so the guard sees real occupancy, not just first-pick volume.
func (s *Service) acquireChannel(channelID int64) {
	if !s.concurrencyAware.Load() {
		return
	}
	s.inflightMu.Lock()
	if s.inflight == nil {
		s.inflight = make(map[int64]int)
	}
	s.inflight[channelID]++
	s.inflightMu.Unlock()
}

func (s *Service) releaseChannel(channelID int64) {
	s.inflightMu.Lock()
	if n := s.inflight[channelID]; n > 1 {
		s.inflight[channelID] = n - 1
	} else {
		delete(s.inflight, channelID)
	}
	s.inflightMu.Unlock()
}

// SetSticky installs the sticky-session store (nil disables). The service
// binds successful relays to their session key and passes the key to the
// selector so affinity is honored on the next request.
func (s *Service) SetSticky(store *routing.StickyStore) {
	s.sticky.Store(store)
}

// SetLatencyAware enables latency-weighted routing and installs this service
// as the latency provider (smoothed per-channel latency in ms).
func (s *Service) SetLatencyAware(enabled bool) {
	s.latencyAware.Store(enabled)
}

// ChannelErrorRate returns the EWMA failure propensity (0..1) for a channel on
// a model, false when no failure has been observed yet. Fresh channels score 0.
func (s *Service) ChannelErrorRate(channelID int64, model string) (float64, bool) {
	s.errorMu.Lock()
	defer s.errorMu.Unlock()
	value, ok := s.errorEMA[channelModel{channelID: channelID, model: model}]
	return value, ok
}

// observeError moves the channel's error EMA toward 1 for the model (alpha
// 0.5 — a single failure halves the channel's share on that model).
func (s *Service) observeError(channelID int64, model string) {
	if channelID <= 0 {
		return
	}
	key := channelModel{channelID: channelID, model: model}
	s.errorMu.Lock()
	defer s.errorMu.Unlock()
	if s.errorEMA == nil {
		s.errorEMA = make(map[channelModel]float64)
	}
	previous, ok := s.errorEMA[key]
	if !ok {
		s.errorEMA[key] = 0.5
		return
	}
	// EWMA toward 1: new = 0.5*1 + 0.5*previous.
	s.errorEMA[key] = 0.5 + 0.5*previous
}

// decayError moves the channel's error EMA toward 0 after a success (halving).
func (s *Service) decayError(channelID int64, model string) {
	if channelID <= 0 {
		return
	}
	key := channelModel{channelID: channelID, model: model}
	s.errorMu.Lock()
	defer s.errorMu.Unlock()
	previous, ok := s.errorEMA[key]
	if !ok || previous == 0 {
		return
	}
	s.errorEMA[key] = 0.5 * previous
}

// ChannelLatency returns the EWMA latency for a channel on a model, false if no
// sample exists yet.
func (s *Service) ChannelLatency(channelID int64, model string) (float64, bool) {
	s.latencyMu.Lock()
	defer s.latencyMu.Unlock()
	value, ok := s.latencyEMA[channelModel{channelID: channelID, model: model}]
	return value, ok
}

// observeLatency updates the per channel × model EWMA from a successful relay.
func (s *Service) observeLatency(channelID int64, model string, latencyMs int) {
	if channelID <= 0 || latencyMs < 0 {
		return
	}
	key := channelModel{channelID: channelID, model: model}
	s.latencyMu.Lock()
	defer s.latencyMu.Unlock()
	if s.latencyEMA == nil {
		s.latencyEMA = make(map[channelModel]float64)
	}
	previous, ok := s.latencyEMA[key]
	if !ok {
		s.latencyEMA[key] = float64(latencyMs)
		return
	}
	// EWMA with alpha 0.2: new = 0.2*sample + 0.8*previous.
	s.latencyEMA[key] = 0.2*float64(latencyMs) + 0.8*previous
}

// SetAdapterRegistry installs the platform adapter registry used to resolve
// per-channel forward adapters (OpenAI passthrough, Anthropic, Gemini, …).
func (s *Service) SetAdapterRegistry(registry *adapters.Registry) {
	s.registry = registry
}

// resolveForward returns the forward adapter for a channel, falling back to
// the OpenAI passthrough adapter when no registry is installed.
func (s *Service) resolveForward(channel domain.Channel) adapters.ForwardAdapter {
	platform := ""
	if channel.SiteID != nil {
		if site, err := s.db.Site.GetByID(*channel.SiteID); err == nil && site != nil {
			platform = site.Platform
		}
	}
	if s.registry != nil {
		return s.registry.ResolveForward(channel.TypeHint, platform)
	}
	return adapters.OpenAIPassthroughAdapter{}
}

// SetRetryPolicy hot-updates cross-channel retry count and cool-down base.
func (s *Service) SetRetryPolicy(retryTimes int, cooldown time.Duration) {
	if retryTimes < 0 {
		retryTimes = 0
	}
	if cooldown < 0 {
		cooldown = 0
	}
	s.retryTimes.Store(int64(retryTimes))
	s.cooldownNs.Store(int64(cooldown))
}

// SetCrossChannelFailoverEnabled hot-applies whether a request may move to a
// different channel after the first channel fails. The retry_times value is
// retained while disabled, so re-enabling restores the configured behavior.
func (s *Service) SetCrossChannelFailoverEnabled(enabled bool) {
	s.crossChannelFailoverEnabled.Store(enabled)
}

// SetChannelRetryTimes hot-updates how many times the same upstream key is
// re-sent after a retryable failure before moving to the next key/channel.
// Network (transport) errors fail fast after these retries instead of fanning
// out across every channel. 0 = no same-key re-send.
func (s *Service) SetChannelRetryTimes(times int) {
	if times < 0 {
		times = 0
	}
	s.channelRetryTimes.Store(int64(times))
}

// SetKeyPoolRotation hot-applies whether the site key pool is rotated through
// on failure. Off = only the channel's bound key (or the first pool key) is
// used; the pool is never rotated.
func (s *Service) SetKeyPoolRotation(enabled bool) {
	s.keyPoolRotation.Store(enabled)
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
	if strings.TrimSpace(req.OpenAIPath) == "" {
		req.OpenAIPath = "chat/completions"
	}
	if strings.TrimSpace(req.Method) == "" {
		req.Method = http.MethodPost
	}
	excluded := make(map[int64]struct{})
	// Resolve the sticky session key: an explicit client header wins;
	// otherwise derive a content digest from the request body (stateless
	// clients get affinity through their conversation content).
	sessionKey := strings.TrimSpace(req.SessionKey)
	if sessionKey == "" {
		sessionKey = routing.SessionKeyFromBody(req.Body)
	}
	req.SessionKey = sessionKey
	stickyStore := s.sticky.Load()
	var last *relay.Result
	var lastMeta *AttemptMeta
	maxAttempts := int(s.retryTimes.Load())
	if !s.crossChannelFailoverEnabled.Load() {
		maxAttempts = 0
	}
	cooldown := time.Duration(s.cooldownNs.Load())
	if req.PreferChannelID > 0 {
		// Admin pin: only hit the chosen channel once (no cross-channel retry).
		maxAttempts = 0
	}
	// Route-level retry overrides: the first selection decision carries the
	// model's policy (nil = follow the global setting). Applied to the loop
	// bound and the same-key re-send count for the whole request.
	var retryOverride *int
	var channelRetryOverride *int
	for attempt := 0; attempt <= maxAttempts; attempt++ {
		// refreshed tracks whether a 401 triggered a credential refresh for
		// this request; a successful replay is logged as refresh_retry.
		refreshed := false
		// monitorSkipped is set when an ignore_monitor error-passthrough rule
		// fired; the breaker/cooldown bookkeeping is skipped for that attempt.
		monitorSkipped := false
		// Sensitive prompt guards run before candidate selection: a reject
		// refuses the request outright, an exclude removes the listed
		// channels from this request's candidates, and a mask rewrites the
		// body once (all candidates see the masked body).
		if guardRules, gErr := s.db.PromptGuard.ListEnabled(); gErr == nil && len(guardRules) > 0 && req.OpenAIPath == "chat/completions" {
			guarded, hit, err := ApplyPromptGuards(req.Body, guardRules)
			if err != nil {
				log.Printf("proxy: prompt guard eval model=%s: %v", req.Model, err)
			} else if hit != nil {
				switch hit.Action {
				case "reject":
					result := &relay.Result{
						StatusCode: http.StatusBadRequest,
						Err:        fmt.Errorf("%w: %s", ErrGuardRejected, hit.Message),
					}
					log.Printf("proxy: prompt guard %q rejected request (request_id=%s)", hit.Rule, req.RequestID)
					return result, nil
				case "exclude":
					for _, id := range hit.Exclude {
						excluded[id] = struct{}{}
					}
					log.Printf("proxy: prompt guard %q excludes channels %v for request (request_id=%s)", hit.Rule, hit.Exclude, req.RequestID)
				default: // mask
					req.Body = guarded
					log.Printf("proxy: prompt guard %q masked request body (request_id=%s)", hit.Rule, req.RequestID)
				}
			}
		}
		decision, err := s.selector.SelectSticky(ctx, req.Model, excluded, sessionKey)
		// Persist a decision snapshot for audit: the full explanation
		// (candidates, scores, reasons, sticky/stable-first state) survives
		// even when the request later fails or the UI is long gone. Errors
		// carry whatever partial explanation the selector produced.
		if payload, marshalErr := json.Marshal(decision.Explanation); marshalErr == nil && len(payload) > 0 {
			selectedID := int64(0)
			if decision.Selected.Channel.ID > 0 {
				selectedID = decision.Selected.Channel.ID
			}
			if snapErr := s.db.InsertDecisionSnapshot(req.RequestID, req.Model, decision.RouteID, selectedID, payload, s.now()); snapErr != nil {
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
			if req.PreferChannelID <= 0 {
				// Route rows are validated by the Admin API, but clamp here as a
				// last line of defence for old/corrupt rows and non-HTTP callers.
				maxAttempts = clampInt(*retryOverride, 0, 100)
			}
		}
		if channelRetryOverride == nil && decision.ChannelRetryTimesOverride != nil {
			channelRetryOverride = decision.ChannelRetryTimesOverride
		}
		candidate := decision.Selected
		if req.PreferChannelID > 0 {
			pinned, ok := pickPreferred(decision, req.PreferChannelID)
			if !ok {
				return &relay.Result{Err: ErrPreferredChannel}, nil
			}
			candidate = pinned
		}
		// Model-not-found blacklist: skip a channel that reported this model as
		// unknown (permanent condition) before spending an attempt on it.
		if blocked, err := s.db.IsModelBlocked(candidate.Channel.ID, req.Model); err == nil && blocked {
			excluded[candidate.Channel.ID] = struct{}{}
			if last != nil && last.Body != nil {
				_ = last.Body.Close()
			}
			last = preserve(&relay.Result{StatusCode: http.StatusServiceUnavailable, Err: fmt.Errorf("%w: model %s on channel %d", ErrModelBlacklisted, req.Model, candidate.Channel.ID)})
			lastMeta = &AttemptMeta{
				ChannelID:   candidate.Channel.ID,
				ChannelName: candidate.Channel.Name,
				MemberID:    candidate.Member.ID,
				Priority:    candidate.Member.Priority,
				Weight:      candidate.Member.Weight,
			}
			continue
		}
		// Circuit breaker: an open channel × model is skipped entirely (weight
		// 0) until its probe window allows a single probe request through.
		if s.breaker != nil && s.breaker.EffectiveWeight(candidate.Channel.ID, req.Model, 1) <= 0 {
			excluded[candidate.Channel.ID] = struct{}{}
			if last != nil && last.Body != nil {
				_ = last.Body.Close()
			}
			last = preserve(&relay.Result{StatusCode: http.StatusServiceUnavailable, Err: fmt.Errorf("proxy: circuit open for %s on channel %d", req.Model, candidate.Channel.ID)})
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
			ChannelID:   candidate.Channel.ID,
			ChannelName: candidate.Channel.Name,
			MemberID:    candidate.Member.ID,
			Priority:    candidate.Member.Priority,
			Weight:      candidate.Member.Weight,
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
		if maxConc := candidate.Channel.MaxConcurrent; maxConc > 0 {
			acquiredGen, gateErr := s.gate.Acquire(ctx, candidate.Channel.ID, maxConc)
			if gateErr != nil {
				return &relay.Result{Err: gateErr}, meta
			}
			gateGen = acquiredGen
			gateHeld = true
		}
		probeReserved := false
		probeEnded := false
		endProbe := func() {
			if probeReserved && !probeEnded {
				s.breaker.EndProbe(candidate.Channel.ID, req.Model)
				probeEnded = true
			}
		}
		defer endProbe()
		// EffectiveWeight returns a half-open weight when an open breaker is
		// due for recovery. Reserve the single probe slot before doing any
		// request setup so concurrent requests cannot all pass as probes.
		if s.breaker != nil && s.breaker.IsOpen(candidate.Channel.ID, req.Model) {
			if !s.breaker.TryBeginProbe(candidate.Channel.ID, req.Model) {
				excluded[candidate.Channel.ID] = struct{}{}
				last = preserve(&relay.Result{
					StatusCode: http.StatusServiceUnavailable,
					Err:        fmt.Errorf("proxy: circuit probe busy for %s on channel %d", req.Model, candidate.Channel.ID),
				})
				lastMeta = meta
				releaseAttempt()
				releaseGate()
				continue
			}
			probeReserved = true
		}
		var result *relay.Result
		var category string
		var retryable bool
		adapter := s.resolveForward(candidate.Channel)

		// Downstream Anthropic clients (/v1/messages): prefer the registered
		// N×M translation for (anthropic → upstream family); when the pair is
		// not registered, fall back to composing the upstream adapter with the
		// Anthropic pivot segment (keeps the OpenAI pivot between protocols).
		downstreamAnthropic := strings.EqualFold(req.DownstreamProtocol, "anthropic")
		var registryTranslation *adapters.Translation
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
		}

		// Channel-scoped model aliases: when the matched route carries a
		// mapping_json of {"real":"…"}, clients requested the alias and we
		// must rewrite the body back to the upstream's real model name.
		mappedBody := req.Body
		if mappingJSON := strings.TrimSpace(decision.RouteMappingJSON); mappingJSON != "" {
			mappedBody = rewriteModelName(req.Body, req.Model, mappingJSON)
		}

		effectivePath := req.OpenAIPath
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
		// Registered N×M translation path: the (anthropic → upstream family)
		// pair exists in the matrix, so translate directly instead of going
		// through the composed adapter. The translation returns the upstream
		// body; the response/stream conversion happens via the pair's
		// Response/Stream modes inside the relay conversion block below.
		if registryTranslation != nil {
			toPath, out, tr, ok, trErr := s.registry.Translations.Translate("anthropic", adapters.CanonicalFamily(adapter.Name()), effectivePath, requestSource)
			if trErr != nil || !ok || tr.Body == nil {
				result = &relay.Result{
					StatusCode: http.StatusBadRequest,
					Err:        fmt.Errorf("proxy: anthropic translation: %w", trErr),
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
			if s.breaker != nil {
				s.breaker.RecordError(candidate.Channel.ID, req.Model, false)
			}
		s.recordAttempt(req, candidate, attempt+1, result, category, "")
		s.recordMemberFailure(candidate.Member.ID, candidate.Channel.ID, req.Model, cooldown, category)

			excluded[candidate.Channel.ID] = struct{}{}
			last = preserve(result)
			lastMeta = meta
			endProbe()
			releaseAttempt()
			releaseGate()
			continue
		}

		for keyIndex, apiKey := range apiKeys {
			keyRetries := int(s.channelRetryTimes.Load())
			if channelRetryOverride != nil {
				keyRetries = clampInt(*channelRetryOverride, 0, 5)
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
			// Time the upstream round trip for first-byte latency on streams.
			forwardStarted := s.now()
			// Circuit-breaker probe: an open breaker whose window is due lets
			// exactly one request through as a probe; its outcome drives the
			// recovery backoff.
			wasProbe := probeReserved
			// Non-streaming requests get an overall cap: an upstream that answers
			// headers and then stalls mid-body would otherwise pin this goroutine
			// until the client disconnects. Streaming requests are exempt — long
			// SSE sessions legitimately exceed any fixed budget, and their idle
			// detection is the stream path's responsibility.
			fwdCtx := ctx
			if !req.Stream {
				timeout := s.nonStreamTimeout
				if timeout <= 0 {
					timeout = nonStreamRequestTimeout
				}
				var cancel context.CancelFunc
				fwdCtx, cancel = context.WithTimeout(ctx, timeout)
				defer cancel()
			}
			// Per-channel outbound proxy: an override on the channel routes this
			// request through it (the transport's proxy hook reads the context).
			if proxyURL := strings.TrimSpace(candidate.Channel.ProxyURL); proxyURL != "" {
				fwdCtx = outbound.WithChannelProxy(fwdCtx, proxyURL)
			}
			// (gate slot is held at the channel-attempt level, above the retry
			// loops — see the Acquire/releaseGate pair near the meta setup)
			result = s.relay.ForwardWithHeaders(fwdCtx, req.Method, upstreamURL, headers, requestBody)
			// Convert upstream 2xx bodies back to the OpenAI contract.
			if result != nil && result.Err == nil && result.StatusCode >= 200 && result.StatusCode < 300 && result.Body != nil {
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
							result.Header.Set("Content-Type", "text/event-stream")
						}
					} else if !req.Stream && registryTranslation.Response != nil {
						raw, readErr := io.ReadAll(io.LimitReader(result.Body, 8<<20))
						_ = result.Body.Close()
						if readErr != nil {
							result = &relay.Result{Header: result.Header, LatencyMs: result.LatencyMs, Err: readErr}
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
							result.Header.Set("Content-Type", "application/json")
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
						result.Header.Set("Content-Type", "text/event-stream")
					}
				} else {
					raw, readErr := io.ReadAll(io.LimitReader(result.Body, 8<<20))
					_ = result.Body.Close()
					if readErr != nil {
						result = &relay.Result{Header: result.Header, LatencyMs: result.LatencyMs, Err: readErr}
					} else if converted, convErr := adapter.TransformResponse(effectivePath, raw); convErr != nil {
						result = &relay.Result{
							StatusCode: adapterErrorStatus(convErr, http.StatusBadGateway),
							Header:     result.Header,
							LatencyMs:  result.LatencyMs,
							Err:        fmt.Errorf("proxy: %s response: %w", adapter.Name(), convErr),
						}
					} else {
						result.Body = io.NopCloser(bytes.NewReader(converted))
						if result.Header == nil {
							result.Header = make(http.Header)
						}
						result.Header.Set("Content-Type", "application/json")
					}
				}
			}
			streamInterrupted := false
			if req.Stream && result.Err == nil && result.StatusCode >= 200 && result.StatusCode < 300 {
				first, peekErr := peekFirstChunkWithTimeout(result.Body, streamFirstByteTimeout)
				if peekErr != nil {
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
				} else {
					// Semantic check: a 200 stream that immediately delivers a
					// terminal/empty SSE frame ([DONE] or a delta with neither
					// content nor role) is a silent failure — the client would
					// receive an empty response. Treat it like a first-byte death
					// and fail over.
					if isSilentSSEStart(first) {
						_ = result.Body.Close()
						result = &relay.Result{
							StatusCode: result.StatusCode,
							Header:     result.Header,
							LatencyMs:  result.LatencyMs,
							Err:        fmt.Errorf("proxy: stream ended silently before any content"),
						}
						streamInterrupted = true
					} else {
						// Replay the buffered prefix, then continue streaming the rest.
						result.Body = io.NopCloser(io.MultiReader(bytes.NewReader(first), result.Body))
						// First-byte latency measured from relay start to the first
						// upstream byte (the peek above consumed it).
						result.FirstByteMs = int(s.now().Sub(forwardStarted).Milliseconds())
					}
				}
			}
		category, retryable = classifyForChannel(result, domain.ParseRetryConfig(candidate.Channel.RetryConfig))
			localAdapterFailure := isAdapterError(result.Err)
			// 401 refresh-retry: an expired session/access-token credential is
			// re-established through the check-in machinery exactly once per
			// request, then the request is replayed from the first key in the
			// refreshed pool. A successful replay is logged as refresh_retry.
			if !refreshed && !localAdapterFailure && result.Err == nil && result.StatusCode == http.StatusUnauthorized && s.credentialRefresher != nil {
				if s.refreshCredentialAndReplay(ctx, &candidate, &apiKeys) {
					refreshed = true
					category = "refresh_retry"
					// Replay in place with the refreshed credential: swap the
					// current key for the pool's first key and re-enter the
					// keyAttempt loop without consuming a retry budget.
					if len(apiKeys) > 0 {
						apiKey = apiKeys[0]
					}
					if keyAttempt > 0 {
						keyAttempt--
					}
					continue
				}
			}
			if streamInterrupted && !localAdapterFailure {
				category = "stream_interrupted"
				retryable = true
			}
			// Circuit breaker bookkeeping: a success resets the breaker right away.
			// Failures are NOT counted per attempt here — the same-key re-send
			// loop would otherwise advance the breaker by (retries+1) for a
			// single request. They are counted exactly once per request after
			// the re-sends are exhausted (transport branch / channel tail).
			if s.breaker != nil && !localAdapterFailure && result.Err == nil && result.StatusCode < 400 {
				s.breaker.RecordSuccess(candidate.Channel.ID, req.Model)
			}
			// Per-key auto-disable (AxonHub): a key that fails N times with the
			// same status code is excluded from the pool. A successful re-send
			// clears the key's counters immediately; the failure path is
			// counted once per key after that key's re-sends are exhausted
			// (transport branch / key-loop tail below), so a single request
			// does not advance the counter once per attempt. Adapter-local
			// errors do not implicate the key.
			if !localAdapterFailure && result.Err == nil && result.StatusCode < 400 {
				s.recordKeySuccess(candidate.Channel.ID, apiKey)
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
			// Network errors fail fast: re-send the same key up to
			// channelRetryTimes, then cool the channel and return instead of
			// fanning out across every channel (a dead network link usually
			// affects all channels equally). The failure counters advance
			// exactly once here — after the re-sends are exhausted.
			if category == "transport" && keyAttempt >= keyRetries {
				if result.Body != nil {
					_ = result.Body.Close()
				}
				if s.breaker != nil && !localAdapterFailure {
					s.breaker.RecordError(candidate.Channel.ID, req.Model, wasProbe)
				}
				if !localAdapterFailure {
					if s.recordKeyFailure(candidate.Channel.ID, apiKey, result.StatusCode) {
						log.Printf("proxy: auto-disabled api key %s on channel %d after repeated status %d (downstream_key_id=%d request_id=%s)", keyFingerprint(apiKey), candidate.Channel.ID, result.StatusCode, req.DownstreamKeyID, req.RequestID)
						// All keys down → cascade to the channel-level disable.
						s.cascadeChannelIfAllKeysDisabled(candidate.Channel)
					}
				}
				penalty := retryAfterCooldown(result.Header, s.now(), cooldown)
				s.recordMemberFailure(candidate.Member.ID, candidate.Channel.ID, req.Model, penalty, category)
				s.recordAttempt(req, candidate, attempt+1, result, category, keyFingerprint(apiKey))
				return result, meta
			}
			// Business retryables (5xx/429): re-send the same key up to
			// channelRetryTimes, then try the next key in the pool before
			// failing over to another channel.
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
		// Per-key failure accounting: this key's re-sends are exhausted (or the
		// error is not retryable) — count the key's failure exactly once per
		// request. Client-cancelled requests are not upstream failures.
		if !isAdapterError(result.Err) && (result.Err != nil || result.StatusCode >= 400) &&
			!errors.Is(result.Err, context.Canceled) && !errors.Is(result.Err, context.DeadlineExceeded) && ctx.Err() == nil {
			if s.recordKeyFailure(candidate.Channel.ID, apiKey, result.StatusCode) {
				log.Printf("proxy: auto-disabled api key %s on channel %d after repeated status %d (downstream_key_id=%d request_id=%s)", keyFingerprint(apiKey), candidate.Channel.ID, result.StatusCode, req.DownstreamKeyID, req.RequestID)
				// All keys down → cascade to the channel-level disable.
				s.cascadeChannelIfAllKeysDisabled(candidate.Channel)
			}
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
		endProbe()
		releaseAttempt()
	}
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
			if err := s.db.RouteMember.RecordSuccess(candidate.Member.ID, s.now()); err != nil {
				log.Printf("proxy: record success member_id=%d: %v", candidate.Member.ID, err)
			}
			s.decayError(candidate.Channel.ID, req.Model)
			s.recordMemberSuccess(candidate.Channel.ID)
			if s.latencyAware.Load() && result.LatencyMs > 0 {
				s.observeLatency(candidate.Channel.ID, req.Model, result.LatencyMs)
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
		if errors.Is(result.Err, context.Canceled) || errors.Is(result.Err, context.DeadlineExceeded) || ctx.Err() != nil {
			return result, meta
		}
		// Configurable error passthrough rules (error_passthrough_rules)
		// override the default 4xx behavior for ALL 4xx responses — including
		// retryable ones (429 rate limits are the classic case). Rules are
		// read live on every request so edits apply instantly.
		//   passthrough     → return the upstream error, no failover/cooldown
		//   rewrite         → passthrough with a rewritten status code
		//   ignore_monitor  → keep failover, skip breaker/cooldown/counters
		if result.Err == nil && result.StatusCode >= 400 && result.StatusCode < 500 {		errorText := modelNotFoundText(result)
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
			// Channel-level breaker: all keys on this channel failed after
			// their re-sends — one failure per request, not per attempt.
			// ignore_monitor rules skip this bookkeeping entirely.
			if !monitorSkipped {
				if s.breaker != nil && !isAdapterError(result.Err) {
					s.breaker.RecordError(candidate.Channel.ID, req.Model, probeReserved)
				}
				penalty := retryAfterCooldown(result.Header, s.now(), cooldown)
				s.recordMemberFailure(candidate.Member.ID, candidate.Channel.ID, req.Model, penalty, category)
			}
			if req.PreferChannelID > 0 {
				return result, meta
			}
	} else if result.Err == nil && result.StatusCode >= 400 && result.StatusCode < 500 {
		// 4xx client error: fail over to the next channel — a different
		// upstream may accept the same request (channel capabilities are
		// heterogeneous: one gateway may reject reasoning_effort=max while
		// another accepts it). Cool the member down so this channel is
		// skipped for a while, but do NOT count toward the channel's
		// consecutive-failure tally or auto-disable: the request itself may
		// be at fault, and counting would let a bad client request take
		// down every channel via repeated 4xx failover.
		if !monitorSkipped {
			// A model_not_found / unknown-model response is permanent for this
			// channel × model: blacklist the combination so routing skips it
			// outright instead of failing over on every request.
			if isModelNotFoundError(result.StatusCode, modelNotFoundText(result)) {
				if err := s.db.BlockModel(candidate.Channel.ID, req.Model, category); err != nil {
					log.Printf("proxy: block model channel=%d model=%s: %v", candidate.Channel.ID, req.Model, err)
				}
				log.Printf("proxy: model %s not found on channel %d — blacklisted (request_id=%s)", req.Model, candidate.Channel.ID, req.RequestID)
			}
			if s.breaker != nil && !isAdapterError(result.Err) {
				s.breaker.RecordError(candidate.Channel.ID, req.Model, probeReserved)
			}
			if err := s.db.RouteMember.RecordFailure(candidate.Member.ID, s.now(), cooldown, category); err != nil {
				log.Printf("proxy: record 4xx member failure member_id=%d: %v", candidate.Member.ID, err)
			}
			s.observeError(candidate.Channel.ID, req.Model)
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
		excluded[candidate.Channel.ID] = struct{}{}
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
func (s *Service) refreshCredentialAndReplay(ctx context.Context, candidate *domain.RoutingCandidate, apiKeys *[]string) bool {
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
	freshKeys, err := s.resolveAPIKeyPool(candidate.Channel, "")
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

func clampInt(value, minValue, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

// resolveAPIKeyPool builds the ordered list of plaintext API keys for a channel.
// Prefer the bound credential first, then every other enabled api_key on the same site.
// Keys that hit the per-key auto-disable threshold are excluded until they heal.
// Keys whose models_csv allowlist does not cover the requested model are skipped
// (empty model = no filtering). With key-pool rotation disabled, only the bound
// key (or the first pool key) is used.
func (s *Service) resolveAPIKeyPool(channel domain.Channel, model string) ([]string, error) {
	seen := make(map[int64]struct{})
	var keys []string

	appendCredential := func(credential *domain.Credential) {
		if credential == nil {
			return
		}
		if _, exists := seen[credential.ID]; exists {
			return
		}
		if credential.Status != domain.StatusEnabled || len(credential.SecretEnc) == 0 {
			return
		}
		// Bearer-style kinds only: api_key plus session/access_token (the
		// latter can 401 → refresh-retry through the check-in machinery).
		kind := strings.ToLower(strings.TrimSpace(credential.Kind))
		if kind != "api_key" && kind != "session" && kind != "access_token" {
			return
		}
		if !modelAllowedByKey(model, credential.ModelsCSV) {
			return
		}
		plaintext, err := s.enc.Decrypt(string(credential.SecretEnc))
		if err != nil || len(plaintext) == 0 {
			return
		}
		// Per-key auto-disable: keys on the disabled list are skipped until
		// their penalty expires (AxonHub-style, avoids nuking the whole
		// channel for one bad key). Scoped to this channel.
		if s.keyDisabled(channel.ID, string(plaintext)) {
			return
		}
		seen[credential.ID] = struct{}{}
		keys = append(keys, string(plaintext))
	}

	if !s.keyPoolRotation.Load() {
		// Rotation off: never rotate through the pool — bound key first, or
		// the first enabled pool key as a fallback.
		if channel.CredentialID != nil {
			bound, err := s.db.Credential.GetByID(*channel.CredentialID)
			if err == nil {
				appendCredential(bound)
			}
		} else if channel.SiteID != nil {
			pool, err := s.db.Credential.ListEnabledAPIKeysBySite(*channel.SiteID)
			if err == nil && len(pool) > 0 {
				appendCredential(&pool[0])
			}
		}
		if len(keys) == 0 {
			return nil, ErrCredential
		}
		return keys, nil
	}

	if channel.CredentialID != nil {
		bound, err := s.db.Credential.GetByID(*channel.CredentialID)
		if err == nil {
			appendCredential(bound)
		}
	}
	if channel.SiteID != nil {
		pool, err := s.db.Credential.ListEnabledAPIKeysBySite(*channel.SiteID)
		if err != nil {
			if len(keys) == 0 {
				return nil, ErrCredential
			}
			return keys, nil
		}
		for index := range pool {
			appendCredential(&pool[index])
		}
	}
	if len(keys) == 0 {
		return nil, ErrCredential
	}
	return keys, nil
}

// modelAllowedByKey reports whether a key's model allowlist (models_csv)
// permits serving the model. Empty allowlist = all models; entries support
// "*" suffix wildcards ("gpt-4*" matches "gpt-4o"). An empty model skips
// filtering entirely.
func modelAllowedByKey(model, modelsCSV string) bool {
	if strings.TrimSpace(model) == "" || strings.TrimSpace(modelsCSV) == "" {
		return true
	}
	for _, part := range strings.Split(modelsCSV, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if strings.HasSuffix(part, "*") {
			if strings.HasPrefix(model, strings.TrimSuffix(part, "*")) {
				return true
			}
		} else if part == model {
			return true
		}
	}
	return false
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
		return "", fmt.Errorf("proxy: invalid base url")
	}
	return upstreamURL, nil
}

// recordMemberFailure records a member failure (member cooldown + channel
// consecutive counter) and auto-disables the channel once the channel-level
// consecutive failures reach the configured threshold.
func (s *Service) recordMemberFailure(memberID, channelID int64, model string, cooldown time.Duration, category string) {
	if err := s.db.RouteMember.RecordFailure(memberID, s.now(), cooldown, category); err != nil {
		log.Printf("proxy: record failure member_id=%d: %v", memberID, err)
	}
	s.recordChannelFailure(channelID)
	s.observeError(channelID, model)
}

// recordChannelFailure increments the channel consecutive-failure counter and
// auto-disables the channel once the threshold is reached.
func (s *Service) recordChannelFailure(channelID int64) {
	threshold := int(s.autoDisableThreshold.Load())
	if threshold <= 0 || channelID <= 0 {
		return
	}
	count, err := s.db.Channel.RecordRelayFailure(channelID)
	if err != nil {
		log.Printf("proxy: channel relay failure channel_id=%d: %v", channelID, err)
		return
	}
	if count >= threshold {
		if err := s.db.Channel.AutoDisable(channelID); err != nil {
			log.Printf("proxy: auto disable channel_id=%d: %v", channelID, err)
		} else if s.notifier != nil {
			name := ""
			if ch, err := s.db.Channel.GetByID(channelID); err == nil && ch != nil {
				name = ch.Name
			}
			s.notifier.Notify(context.Background(), webhook.ChannelDisabled, channelID, name,
				fmt.Sprintf("%d consecutive failures", count))
			// Request-failure alert through the full matrix (bark/serverchan/
			// telegram/smtp too, not just the legacy webhook URL).
			s.notifier.SendAlert(context.Background(), webhook.AlertWarning, "请求失败告警",
				fmt.Sprintf("渠道 #%d (%s) 连续 %d 次失败，已自动禁用。", channelID, name, count))
		}
	}
}

// recordMemberSuccess resets the channel consecutive-failure counter.
func (s *Service) recordMemberSuccess(channelID int64) {
	if channelID <= 0 {
		return
	}
	if err := s.db.Channel.RecordRelaySuccess(channelID); err != nil {
		log.Printf("proxy: channel relay success channel_id=%d: %v", channelID, err)
	}
}

// rewriteModelName rewrites the JSON "model" field of a request body from the
// client-facing alias back to the upstream's real model name. mappingJSON is
// the route's mapping_json value, expected to be {"real":"upstream-model"}.
// It is a no-op when the body is not JSON, the field is absent, or it does not
// match the requested alias.
func rewriteModelName(body []byte, requestedModel, mappingJSON string) []byte {
	if len(body) == 0 || requestedModel == "" || mappingJSON == "" {
		return body
	}
	var mapping struct {
		Real string `json:"real"`
	}
	if err := json.Unmarshal([]byte(mappingJSON), &mapping); err != nil || mapping.Real == "" {
		return body
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		// Not JSON (multipart etc.): leave untouched.
		return body
	}
	current, ok := payload["model"].(string)
	if !ok || current != requestedModel {
		return body
	}
	payload["model"] = mapping.Real
	rewritten, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return rewritten
}

// reasoningEffortLevels is the ordered set of OpenAI-style reasoning effort
// values understood by the gateway. A client-requested effort beyond a
// channel's declared max is downgraded to the max at forward time.
var reasoningEffortLevels = []string{
	"none", "minimal", "low", "medium", "high", "xhigh", "max",
}

// downgradeReasoningEffort rewrites a request body's reasoning_effort when it
// exceeds the channel's declared maximum. Returns the rewritten body and a
// "from→to" note, or (nil, "") when no downgrade applies (missing field,
// unknown values, or already at/below the max).
func downgradeReasoningEffort(body []byte, maxEffort string) ([]byte, string) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, ""
	}
	raw, ok := payload["reasoning_effort"]
	if !ok {
		return nil, ""
	}
	var effort string
	if err := json.Unmarshal(raw, &effort); err != nil {
		return nil, ""
	}
	effort = strings.ToLower(strings.TrimSpace(effort))
	maxEffort = strings.ToLower(strings.TrimSpace(maxEffort))
	if effort == "" || maxEffort == "" || effort == maxEffort {
		return nil, ""
	}
	effortIndex, maxIndex := -1, -1
	for i, level := range reasoningEffortLevels {
		if level == effort {
			effortIndex = i
		}
		if level == maxEffort {
			maxIndex = i
		}
	}
	// Unknown values pass through untouched (never guess); already-at/below max
	// needs no rewrite.
	if effortIndex < 0 || maxIndex < 0 || effortIndex <= maxIndex {
		return nil, ""
	}
	payload["reasoning_effort"] = json.RawMessage(fmt.Sprintf("%q", maxEffort))
	rewritten, err := json.Marshal(payload)
	if err != nil {
		return nil, ""
	}
	return rewritten, effort + "→" + maxEffort
}

func (s *Service) recordAttempt(req Request, candidate domain.RoutingCandidate, attempt int, result *relay.Result, category string, keyFP string) {	status := result.StatusCode
	if status == 0 && result.Err != nil {
		status = http.StatusBadGateway
	} else if status == 0 {
		status = http.StatusOK
	}
	errorBrief := ""
	if result.Err != nil || isRetryableStatus(result.StatusCode) || category == "refresh_retry" {
		errorBrief = category
	}
	_, err := s.db.ProxyLog.Insert(&domain.ProxyLog{
		RequestID:        req.RequestID,
		ChannelID:        candidate.Channel.ID,
		RouteID:          candidate.Member.RouteID,
		Model:            req.Model,
		Status:           status,
		LatencyMs:        result.LatencyMs,
		Attempt:          attempt,
		ErrorBrief:       errorBrief,
		DownstreamKeyID:  req.DownstreamKeyID,
		PromptTokens:     req.PromptTokens,
		CompletionTokens: req.CompletionTokens,
		TotalTokens:      req.TotalTokens,
		Stream:           req.Stream,
		Path:             req.OpenAIPath,
		SessionKey:       req.SessionKey,
	ReasoningEffort:       req.ReasoningEffort,
	MappedReasoningEffort: req.MappedReasoningEffort,
		KeyFingerprint:   keyFP,
		UpstreamRequestID: upstreamRequestID(result),
	})
	if err != nil {
		log.Printf("proxy: record attempt request_id=%s channel_id=%d attempt=%d: %v", req.RequestID, candidate.Channel.ID, attempt, err)
	}
}

// upstreamRequestID reads the upstream x-request-id header for the attempt.
// A nil result header (adapter-local failures) yields an empty string.
func upstreamRequestID(result *relay.Result) string {
	if result == nil || result.Header == nil {
		return ""
	}
	return strings.TrimSpace(result.Header.Get("X-Request-Id"))
}

// RecordUsage persists metered tokens for a completed relay response.
func (s *Service) RecordUsage(req Request, channelID int64, status int, tokens usage.Tokens) {
	// Stable-first promotion: successful traffic on a grayscale channel counts
	// toward graduation; the store clears the mark when the threshold is met
	// with no consecutive failures.
	if status >= 200 && status < 300 {
		if threshold := int(s.grayPromoteRequests.Load()); threshold > 0 && s.db != nil && s.db.Channel != nil {
			if promoted, err := s.db.Channel.RecordGraySuccess(channelID, threshold); err != nil {
				log.Printf("proxy: gray success channel_id=%d: %v", channelID, err)
			} else if promoted {
				log.Printf("proxy: channel %d promoted from stable-first grayscale", channelID)
			}
		}
	}
	total := tokens.TotalTokens
	if total <= 0 {
		total = tokens.PromptTokens + tokens.CompletionTokens
	}
	if total <= 0 || s.db == nil {
		return
	}
	// Billing: cost = key unit prices × model ratio, computed and persisted at
	// record time so bills are stable even if prices are edited later.
	record := &domain.UsageRecord{
		RequestID:           req.RequestID,
		DownstreamKeyID:     req.DownstreamKeyID,
		ChannelID:           channelID,
		Model:               req.Model,
		Path:                req.OpenAIPath,
		Stream:              req.Stream,
		PromptTokens:        tokens.PromptTokens,
		CompletionTokens:    tokens.CompletionTokens,
		TotalTokens:         total,
		CacheReadTokens:     tokens.CacheReadTokens,
		CacheCreationTokens: tokens.CacheCreationTokens,
		Status:              status,
	}
	// Tenant group for group-quota accrual (same transaction).
	if req.DownstreamKeyID > 0 && s.db.DownstreamKey != nil {
		if key, err := s.db.DownstreamKey.GetByID(req.DownstreamKeyID); err == nil && key != nil {
			record.GroupName = key.GroupName
		}
	}
	record.Cost = s.billingCost(req, tokens)
	// Usage row, downstream-key quota increment, and proxy-log token backfill
	// commit in one transaction (store.RecordRelayUsage), so a partial failure
	// can never leave metered usage without its quota charge.
	if err := s.db.RecordRelayUsage(record, req.DownstreamKeyID); err != nil {
		log.Printf("proxy: record usage request_id=%s: %v", req.RequestID, err)
	}
}

// billingCost computes the persisted cost for a usage record: per-1k unit
// prices of the downstream key, multiplied by the model's billing ratio.
// Cache-read tokens are billed at the prompt rate. Failures are never fatal;
// a price lookup error degrades to 0 cost rather than dropping the record.
func (s *Service) billingCost(req Request, tokens usage.Tokens) float64 {
	if s.db == nil {
		return 0
	}
	ratio := 1.0
	if s.db.ModelRatio != nil {
		if r, err := s.db.ModelRatio.GetRatio(req.Model); err == nil {
			ratio = r
		} else {
			log.Printf("proxy: billing ratio model=%s: %v", req.Model, err)
		}
	}
	pricePrompt, priceCompletion := 0.0, 0.0
	if req.DownstreamKeyID > 0 && s.db.DownstreamKey != nil {
		if key, err := s.db.DownstreamKey.GetByID(req.DownstreamKeyID); err == nil && key != nil {
			pricePrompt, priceCompletion = key.PricePromptPer1k, key.PriceCompletionPer1k
		}
	}
	prompt := float64(tokens.PromptTokens + tokens.CacheReadTokens + tokens.CacheCreationTokens)
	completion := float64(tokens.CompletionTokens)
	return (prompt/1000.0*pricePrompt + completion/1000.0*priceCompletion) * ratio
}

// keyFingerprint hashes an upstream api key so the in-memory failure tables
// never hold (or log) the plaintext secret.
func keyFingerprint(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:8])
}

// recordKeyFailure increments the per channel × key × status counter and
// returns true when the auto-disable threshold was crossed (the key is then
// excluded from the pool until a success or the penalty TTL expires).
func (s *Service) recordKeyFailure(channelID int64, key string, status int) bool {
	threshold := int(s.keyFailThreshold.Load())
	if threshold <= 0 {
		return false // key auto-exclusion disabled
	}
	fp := keyFingerprint(key)
	now := s.now()
	s.keyErrMu.Lock()
	defer s.keyErrMu.Unlock()
	// Expire stale penalties (30 minutes) so a fixed key heals automatically.
	// Scoped per channel: one channel's exclusion must not be lifted by another.
	for dk, until := range s.disabledKeys {
		if dk.channelID == channelID && now.After(until) {
			delete(s.disabledKeys, dk)
		}
	}
	if s.keyErrCounts[channelID] == nil {
		s.keyErrCounts[channelID] = make(map[string]map[int]int)
	}
	if s.keyErrCounts[channelID][fp] == nil {
		s.keyErrCounts[channelID][fp] = make(map[int]int)
	}
	s.keyErrCounts[channelID][fp][status]++
	if s.keyErrCounts[channelID][fp][status] >= threshold {
		delete(s.keyErrCounts[channelID], fp)
		s.disabledKeys[disabledKey{channelID: channelID, fp: fp}] = now.Add(30 * time.Minute)
		return true
	}
	return false
}

// recordKeySuccess clears a key's failure counters and lifts its disable on
// the same channel only (another channel using the same key must not heal it).
func (s *Service) recordKeySuccess(channelID int64, key string) {
	fp := keyFingerprint(key)
	s.keyErrMu.Lock()
	defer s.keyErrMu.Unlock()
	delete(s.disabledKeys, disabledKey{channelID: channelID, fp: fp})
	if s.keyErrCounts[channelID] != nil {
		delete(s.keyErrCounts[channelID], fp)
	}
}

// keyDisabled reports whether the key is currently excluded from the pool on
// the given channel.
func (s *Service) keyDisabled(channelID int64, key string) bool {
	fp := keyFingerprint(key)
	s.keyErrMu.Lock()
	defer s.keyErrMu.Unlock()
	until, ok := s.disabledKeys[disabledKey{channelID: channelID, fp: fp}]
	if !ok {
		return false
	}
	if s.now().After(until) {
		delete(s.disabledKeys, disabledKey{channelID: channelID, fp: fp})
		return false
	}
	return true
}

// cascadeChannelIfAllKeysDisabled implements the AxonHub all-keys-down rule:
// when the per-key disabled set leaves the channel with no usable key (the
// pool is empty because every key is disabled), the channel itself is
// auto-disabled — a bad-key storm must not leave a half-broken channel in the
// routing pool with zero credentials.
func (s *Service) cascadeChannelIfAllKeysDisabled(channel domain.Channel) {
	if s.autoDisableThreshold.Load() <= 0 || channel.ID <= 0 {
		return
	}
	// Pool still resolves keys → other keys remain usable, no cascade.
	if keys, err := s.resolveAPIKeyPool(channel, ""); err == nil && len(keys) > 0 {
		return
	}
	// Pool empty: distinguish "channel has no keys at all" (nothing to
	// cascade) from "every enabled key is now disabled" (cascade).
	if channel.SiteID == nil {
		return
	}
	all, err := s.db.Credential.ListEnabledAPIKeysBySite(*channel.SiteID)
	if err != nil || len(all) == 0 {
		return
	}
	if err := s.db.Channel.AutoDisable(channel.ID); err != nil {
		log.Printf("proxy: cascade disable channel_id=%d (all keys disabled): %v", channel.ID, err)
		return
	}
	log.Printf("proxy: auto-disabled channel %d: all api keys disabled", channel.ID)
	if s.notifier != nil {
		go s.notifier.Notify(context.Background(), webhook.ChannelDisabled, channel.ID, channel.Name, "all api keys disabled")
		s.notifier.SendAlert(context.Background(), webhook.AlertWarning, "请求失败告警", fmt.Sprintf("渠道 #%d (%s) 所有 API Key 均被禁用，渠道已级联禁用。", channel.ID, channel.Name))
	}
}

// classifyForChannel is classify plus the channel's retry_config: custom
// status codes and error-text patterns can make a non-default failure
// retryable (or keep a default one retryable).
func adapterErrorStatus(err error, fallback int) int {
	switch {
	case errors.Is(err, adapters.ErrUnsupportedPath), errors.Is(err, adapters.ErrUnsupportedFeature):
		return http.StatusNotImplemented
	case errors.Is(err, adapters.ErrContentBlocked):
		return http.StatusBadRequest
	default:
		return fallback
	}
}

func adapterErrorCategory(err error) string {
	switch {
	case errors.Is(err, adapters.ErrInvalidURL):
		return "invalid_url"
	case errors.Is(err, adapters.ErrUnsupportedPath):
		return "unsupported_path"
	case errors.Is(err, adapters.ErrUnsupportedFeature):
		return "unsupported_feature"
	case errors.Is(err, adapters.ErrContentBlocked):
		return "content_blocked"
	default:
		return "adapter_request"
	}
}

func isAdapterError(err error) bool {
	return errors.Is(err, adapters.ErrInvalidURL) ||
		errors.Is(err, adapters.ErrUnsupportedPath) ||
		errors.Is(err, adapters.ErrUnsupportedFeature) ||
		errors.Is(err, adapters.ErrContentBlocked)
}

func classifyForChannel(result *relay.Result, cfg domain.RetryConfig) (string, bool) {
	if result.Err != nil {
		if errors.Is(result.Err, context.Canceled) || errors.Is(result.Err, context.DeadlineExceeded) {
			return "cancelled", false
		}
		switch {
		case errors.Is(result.Err, adapters.ErrInvalidURL):
			return "invalid_url", false
		case errors.Is(result.Err, adapters.ErrUnsupportedPath):
			return "unsupported_path", false
		case errors.Is(result.Err, adapters.ErrUnsupportedFeature):
			return "unsupported_feature", false
		case errors.Is(result.Err, adapters.ErrContentBlocked):
			return "content_blocked", false
		}
		// Transport errors stay retryable; patterns may add text-matched cases.
		if isRetryableForChannel(0, result.Err.Error(), cfg) {
			return "transport", true
		}
		return "transport", true
	}
	category := fmt.Sprintf("upstream_status_%d", result.StatusCode)
	if isRetryableForChannel(result.StatusCode, upstreamErrorText(result), cfg) {
		return category, true
	}
	return category, false
}

// modelNotFoundText returns the error text used for model-not-found
// detection: the OpenAI-style error.code (e.g. model_not_found) joined with
// the message, so both structured codes and free-text messages match.
func modelNotFoundText(result *relay.Result) string {
	if result == nil || result.Body == nil {
		return ""
	}
	raw, err := io.ReadAll(io.LimitReader(result.Body, 64<<10))
	_ = result.Body.Close()
	result.Body = io.NopCloser(bytes.NewReader(raw))
	if err != nil {
		return ""
	}
	var payload struct {
		Error struct {
			Message string `json:"message"`
			Code    string `json:"code"`
			Type    string `json:"type"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return string(raw)
	}
	if payload.Error.Code != "" {
		return payload.Error.Code + " " + payload.Error.Message
	}
	if payload.Error.Message != "" {
		return payload.Error.Message
	}
	return payload.Message
}

// isModelNotFoundError reports whether the upstream said this model does not
// exist (OpenAI code/type identifiers plus common message shapes). Mirror of
// the identifiers used by CLIProxyAPI's isModelNotFoundIdentifier: codes like
// model_not_found / unknown_model / model_does_not_exist and messages like
// "no such model X" / "unknown model X".
func isModelNotFoundError(status int, text string) bool {
	if status < 400 || status > 499 {
		return false
	}
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	normalized := strings.NewReplacer("-", "_", " ", "_").Replace(lower)
	for _, marker := range []string{
		"model_not_found", "model_not_found_error", "unknown_model",
		"model_does_not_exist", "model_not_exist",
	} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	for _, prefix := range []string{"no such model", "unknown model"} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

// upstreamErrorText extracts the upstream error message from a non-2xx relay
// result body (OpenAI-style {error:{message}}), for error-pattern matching.
// The body is restored after reading so the client still receives it.
func upstreamErrorText(result *relay.Result) string {
	if result == nil || result.Body == nil || result.StatusCode >= 200 && result.StatusCode < 300 {
		return ""
	}
	raw, err := io.ReadAll(io.LimitReader(result.Body, 64<<10))
	// Close the live upstream body before swapping in the replay buffer so the
	// connection returns to the pool.
	_ = result.Body.Close()
	// Restore the body for downstream consumers.
	result.Body = io.NopCloser(bytes.NewReader(raw))
	if err != nil {
		return ""
	}
	var payload struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return string(raw)
	}
	if payload.Error.Message != "" {
		return payload.Error.Message
	}
	return payload.Message
}

func isRetryableStatus(status int) bool {
	// AxonHub default set: 429 (rate limit) and 5xx (transient upstream
	// failures) are retryable. Other 4xx are NOT retried by default — a bad
	// request (auth, missing model, malformed payload) will not heal by
	// failing over. Channels can opt into additional codes/patterns via
	// retry_config.
	switch status {
	case http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusInternalServerError,
		http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout,
		// Cloudflare origin-down / overload codes.
		528, 529, 530:
		return true
	default:
		return status >= 500 && status < 600
	}
}

// isRetryableForChannel adds the channel's retry_config on top of the
// global default set (mirrors AxonHub retry.go): custom status codes and
// error-message patterns (substring or regex). Regex patterns arrive
// pre-compiled from ParseRetryConfig; nil entries are skipped.
func isRetryableForChannel(status int, errMsg string, cfg domain.RetryConfig) bool {
	if isRetryableStatus(status) {
		return true
	}
	for _, code := range cfg.StatusCodes {
		if code == status {
			return true
		}
	}
	if errMsg == "" {
		return false
	}
	for i, pattern := range cfg.ErrorPatterns {
		if pattern.Pattern == "" {
			continue
		}
		if pattern.Regex {
			compiled := cfg.CompiledPatterns()
			if i < len(compiled) && compiled[i] != nil && compiled[i].MatchString(errMsg) {
				return true
			}
			continue
		}
		if strings.Contains(errMsg, pattern.Pattern) {
			return true
		}
	}
	return false
}

// preserveReadLimit caps how many bytes preserve() buffers from an upstream
// response before handing the body back. Successful responses may legitimately
// be large (non-stream completions), so only error responses are capped to the
// error-text bound; the failure body is never surfaced whole to the client and
// only its leading text matters for retry classification.
const preserveErrorReadLimit = 64 * 1024

// preserveErrorReadLimitUpper is the cap for non-error (2xx) bodies, matching
// the historical relay bound.
const preserveBodyReadLimit = 10 * 1024 * 1024

func preserve(result *relay.Result) *relay.Result {
	if result == nil || result.Body == nil {
		return result
	}
	limit := int64(preserveBodyReadLimit)
	if result.StatusCode >= 400 {
		limit = preserveErrorReadLimit
	}
	body, err := io.ReadAll(io.LimitReader(result.Body, limit))
	// The original (possibly live) body is fully consumed; close it before
	// handing the replay buffer to the caller so the connection is released.
	_ = result.Body.Close()
	if err != nil {
		return &relay.Result{StatusCode: result.StatusCode, Header: result.Header, LatencyMs: result.LatencyMs, Err: fmt.Errorf("proxy: preserve upstream response: %w", err)}
	}
	return &relay.Result{StatusCode: result.StatusCode, Header: result.Header.Clone(), LatencyMs: result.LatencyMs, Body: io.NopCloser(bytes.NewReader(body))}
}

// streamFirstByteTimeout bounds how long a 200 stream may stay silent before
// its first byte. An upstream that answers 200 and then hangs (half-open
// connection) would otherwise pin the client forever with no data — this
// converts that into a retryable first-byte failure so the request can fail
// over to the next channel.
const streamFirstByteTimeout = 30 * time.Second

// maxStreamFirstChunkBytes bounds how much of a stream prefix we buffer before
// committing the response to the client.
const maxStreamFirstChunkBytes = 256 * 1024

// nonStreamRequestTimeout caps the total duration of a non-streaming upstream
// attempt (request + full body read). Streaming requests are exempt.
const nonStreamRequestTimeout = 5 * time.Minute

// peekFirstChunk reads the leading bytes of an upstream stream response. SSE
// frames end with a blank line, so reading until "\n\n" (or a bounded amount of
// data) lets the gateway detect a 200 that immediately died and fail over to
// the next channel instead of surfacing a silent truncated response.
func peekFirstChunk(body io.Reader) ([]byte, error) {
	var buffered bytes.Buffer
	buffer := make([]byte, 4096)
	for {
		readN, readErr := body.Read(buffer)
		if readN > 0 {
			buffered.Write(buffer[:readN])
			if buffered.Len() >= maxStreamFirstChunkBytes || bytes.Contains(buffered.Bytes(), []byte("\n\n")) {
				return buffered.Bytes(), nil
			}
		}
		if readErr != nil {
			if readErr == io.EOF && buffered.Len() > 0 {
				return buffered.Bytes(), nil
			}
			return nil, readErr
		}
	}
}

// peekFirstChunkWithTimeout bounds peekFirstChunk: a silent 200 stream that
// never emits a byte is closed and reported as a first-byte timeout so the
// candidate loop can fail over (the client has not received anything yet).
func peekFirstChunkWithTimeout(body io.ReadCloser, timeout time.Duration) ([]byte, error) {
	type chunk struct {
		data []byte
		err  error
	}
	ch := make(chan chunk, 1)
	go func() {
		data, err := peekFirstChunk(body)
		ch <- chunk{data, err}
	}()
	select {
	case res := <-ch:
		return res.data, res.err
	case <-time.After(timeout):
		// Closing the body unblocks the pending Read (http guarantees this),
		// which releases the goroutine; the result is discarded.
		_ = body.Close()
		return nil, fmt.Errorf("first byte timeout after %s", timeout)
	}
}

// isSilentSSEStart reports whether a buffered SSE prefix contains only
// terminal or empty frames — i.e. the stream is 200 but will deliver no
// content. A standard OpenAI first chunk carries {"role":"assistant"} with
// no content, which is NOT silent; only frames with neither content nor role,
// or an immediate [DONE], are treated as silent failure.
func isSilentSSEStart(prefix []byte) bool {
	if len(prefix) == 0 {
		return false // empty prefix is handled by peekErr (death before first byte)
	}
	seenAnyData := false
	for _, line := range bytes.Split(prefix, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		payload := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
		if len(payload) == 0 {
			continue
		}
		seenAnyData = true
		if bytes.Equal(payload, []byte("[DONE]")) {
			continue
		}
		var frame struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
					Role    string `json:"role"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(payload, &frame); err != nil {
			// Non-JSON SSE (e.g. raw keep-alive comments) — not silent.
			return false
		}
		if frame.Choices == nil {
			// A JSON frame without a choices array is not a standard OpenAI
			// shape (e.g. nonstandard upstreams, proxies); never classify it
			// as silent — fail open rather than retry valid streams.
			return false
		}
		if len(frame.Choices) == 0 {
			continue // usage-only frame; not content, but also not fatal yet
		}
		for _, choice := range frame.Choices {
			if choice.Delta.Content != "" || choice.Delta.Role != "" {
				return false // real content or a proper role header frame
			}
		}
	}
	// Silent only when we saw data frames and none carried content/role.
	return seenAnyData
}

// retryAfterCooldown extends the base cool-down with the upstream's Retry-After
// header when present (whole seconds or an HTTP-date). It never shrinks the base.
func retryAfterCooldown(header http.Header, now time.Time, base time.Duration) time.Duration {
	if header == nil {
		return base
	}
	value := strings.TrimSpace(header.Get("Retry-After"))
	if value == "" {
		return base
	}
	var penalty time.Duration
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		penalty = time.Duration(seconds) * time.Second
	} else if when, err := http.ParseTime(value); err == nil {
		penalty = when.Sub(now)
	}
	if penalty > base {
		return penalty
	}
	return base
}

// RecordStreamFailure marks the member that served a stream as failed after the
// upstream connection broke mid-stream. The partial response is already on the
// wire, so the current request cannot be retried, but cooling the member down
// makes the next request fail over to a healthier channel.
func (s *Service) RecordStreamFailure(memberID int64) {
	cooldown := time.Duration(s.cooldownNs.Load())
	if err := s.db.RouteMember.RecordFailure(memberID, s.now(), cooldown, "stream_interrupted"); err != nil {
		log.Printf("proxy: record stream failure member_id=%d: %v", memberID, err)
	}
}

// injectSystemPrompt prepends a system message to an OpenAI chat/completions
// body. Non-JSON or non-chat bodies are returned unchanged.
func injectSystemPrompt(body []byte, prompt string) []byte {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return body
	}
	messages, ok := payload["messages"].([]any)
	if !ok {
		return body
	}
	system := map[string]any{"role": "system", "content": prompt}
	// Skip if an identical system message is already first.
	if len(messages) > 0 {
		if first, ok := messages[0].(map[string]any); ok {
			if role, _ := first["role"].(string); role == "system" {
				if existing, _ := first["content"].(string); existing == prompt {
					return body
				}
			}
		}
	}
	updated := make([]any, 0, len(messages)+1)
	updated = append(updated, system)
	updated = append(updated, messages...)
	payload["messages"] = updated
	rewritten, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return rewritten
}

// forbiddenOverrideHeaders cannot be overridden by channel config (transport
// level or authentication-critical).
var forbiddenOverrideHeaders = map[string]struct{}{
	"host":                {},
	"content-length":      {},
	"transfer-encoding":   {},
	"connection":          {},
	"proxy-authorization": {},
	"proxy-connection":    {},
	"te":                  {},
	"trailer":             {},
	"upgrade":             {},
}

// mergeHeaderOverrides applies a channel's header_override JSON onto headers.
// Values replace existing ones; hop-by-hop and auth-critical names are ignored.
func mergeHeaderOverrides(headers http.Header, raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var overrides map[string]string
	if err := json.Unmarshal([]byte(raw), &overrides); err != nil {
		return fmt.Errorf("invalid header_override JSON: %w", err)
	}
	for name, value := range overrides {
		key := http.CanonicalHeaderKey(strings.TrimSpace(name))
		if key == "" {
			continue
		}
		if _, blocked := forbiddenOverrideHeaders[strings.ToLower(key)]; blocked {
			continue
		}
		headers.Set(key, value)
	}
	return nil
}

// Package proxy orchestrates routing, retries, upstream relay, and attempt logs.
package proxy

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lan/meta-gateway/internal/adapters"
	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/relay"
	"github.com/lan/meta-gateway/internal/routing"
	"github.com/lan/meta-gateway/internal/store"
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
	ErrModelTooLong  = errors.New("proxy: model name too long")
	// ErrResponseTooLarge marks an upstream body that exceeds the bounded
	// transformation buffer. It is a local, non-retryable failure: replaying a
	// charged generation would be worse than returning a gateway error.
	ErrResponseTooLarge = errors.New("proxy: upstream response too large")
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
	// autoDisableThreshold: consecutive member failures before a channel is
	// auto-disabled (0 = feature off).
	autoDisableThreshold   atomic.Int64
	faultProtectionEnabled atomic.Bool
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
	notifier atomic.Pointer[webhook.Notifier]
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
	// RouteID is filled after selection so usage accounting can update a
	// model-level stable-first route without changing the public relay API.
	RouteID int64
	// GrayAttempt indicates that the selected candidate came from the gray pool.
	GrayAttempt bool
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
	RouteID     int64  `json:"route_id,omitempty"`
	GrayAttempt bool   `json:"gray_attempt,omitempty"`
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
	service.retryTimes.Store(int64(retryTimes))
	service.channelRetryTimes.Store(1)
	service.crossChannelFailoverEnabled.Store(true)
	service.keyPoolRotation.Store(true)
	service.faultProtectionEnabled.Store(true)
	service.cooldownNs.Store(int64(cooldown))
	service.gate = NewChannelGate()
	return service
}

// SetAutoDisableThreshold enables channel auto-disable after N consecutive
// member failures (0 disables).
func (s *Service) SetAutoDisableThreshold(n int) {
	s.autoDisableThreshold.Store(int64(n))
}

// SetFaultProtection controls fixed cooldown and channel auto-disable. Retry,
// cross-channel failover, and health reporting continue when it is disabled.
func (s *Service) SetFaultProtection(enabled bool) {
	s.faultProtectionEnabled.Store(enabled)
}

// SetStableFirstPromote configures the grayscale promotion threshold: after
// that many successful relay attempts on a stable-first channel (with no
// consecutive failures), the channel is promoted. 0 disables promotion.
func (s *Service) SetStableFirstPromote(n int) {
	s.grayPromoteRequests.Store(int64(n))
}

// SetWebhookNotifier installs the operational webhook notifier (nil disables).
func (s *Service) SetWebhookNotifier(notifier *webhook.Notifier) {
	s.notifier.Store(notifier)
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

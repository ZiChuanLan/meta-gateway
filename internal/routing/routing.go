// Package routing evaluates and selects channels for exact model routes.
package routing

import (
	"context"
	"errors"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
)

var (
	ErrRouteNotFound = errors.New("routing: route not found")
	ErrNoEligible    = errors.New("routing: no eligible channel")
)

type Reason string

const (
	ReasonMemberDisabled   Reason = "member_disabled"
	ReasonChannelDisabled  Reason = "channel_disabled"
	ReasonCredentialAbsent Reason = "credential_unavailable"
	ReasonCoolingDown      Reason = "cooling_down"
	ReasonExcluded         Reason = "already_attempted"
	ReasonInvalidWeight    Reason = "invalid_weight"
	ReasonCircuitOpen      Reason = "circuit_open"
)

type Evaluation struct {
	Candidate domain.RoutingCandidate `json:"candidate"`
	Eligible  bool                    `json:"eligible"`
	Reasons   []Reason                `json:"reasons"`
	// Score is the effective weight for this candidate under the route's
	// current policy (base weight × latency factor × error factor). It
	// mirrors the live selector scoring so the admin UI can show how much
	// adaptive policy changes each channel's actual share. Concurrency is
	// intentionally excluded — it is transient, not a stable policy signal.
	Score float64 `json:"score,omitempty"`
}

type Explanation struct {
	Model            string       `json:"model"`
	RouteID          int64        `json:"route_id"`
	RouteMappingJSON string       `json:"route_mapping_json,omitempty"`
	RoutingMode      string       `json:"routing_mode,omitempty"`
	EvaluatedAt      time.Time    `json:"evaluated_at"`
	SelectedPriority *int         `json:"selected_priority,omitempty"`
	Candidates       []Evaluation `json:"candidates"`
	// Sticky-session fields: present only when a session key was supplied.
	// StickyHit is true when the bound channel was selected again; otherwise
	// StickyReason explains why the binding could not be honored.
	SessionKey      string `json:"session_key,omitempty"`
	StickyChannelID *int64 `json:"sticky_channel_id,omitempty"`
	StickyHit       bool   `json:"sticky_hit,omitempty"`
	StickyReason    string `json:"sticky_reason,omitempty"`
	// Stable-first grayscale fields: present when the pool is active.
	// StableFirstHit is true when the grayscale pool won the 1/N draw.
	StableFirstHit bool `json:"stable_first_hit,omitempty"`
	// StableFirstDenominator is the active 1/N gray ratio (0 = disabled).
	StableFirstDenominator int `json:"stable_first_denominator,omitempty"`
	// RetryTimesOverride / ChannelRetryTimesOverride carry the route-level
	// retry policy (nil = follow the global runtime setting). The proxy reads
	// them from the selection decision.
	RetryTimesOverride         *int `json:"retry_times_override,omitempty"`
	ChannelRetryTimesOverride  *int `json:"channel_retry_times_override,omitempty"`
}

type Decision struct {
	Explanation
	Selected domain.RoutingCandidate `json:"selected"`
}

type Repository interface {
	RoutingCandidates(model string) (*domain.Route, []domain.RoutingCandidate, error)
}

type Clock interface {
	Now() time.Time
}

type Random interface {
	Intn(n int) int
	// Float64 returns a random float in [0,1); used by latency-aware picking.
	Float64() float64
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

type lockedRandom struct {
	mu sync.Mutex
	r  *rand.Rand
}

func (r *lockedRandom) Intn(n int) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.r.Intn(n)
}

func (r *lockedRandom) Float64() float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.r.Float64()
}

// LatencyProvider returns the smoothed latency (ms) for a channel on a given
// model (per channel × model EWMA), with false when no sample exists yet.
type LatencyProvider func(channelID int64, model string) (float64, bool)

// ErrorProvider returns the EWMA failure propensity (0..1) for a channel on a
// given model, with false when no failure has been observed (fresh keep full
// weight).
type ErrorProvider func(channelID int64, model string) (float64, bool)

// CircuitWeightProvider returns the live model-circuit multiplier for a
// channel. A non-positive value means the channel is currently open and must
// not be selected; a value between zero and one represents a half-open probe
// share.
type CircuitWeightProvider func(channelID int64, model string) float64

// ConcurrencyProvider returns the number of in-flight relay attempts currently
// occupying a channel (0 when none). It is the input to the burst guard that
// keeps a sudden spike from overwhelming the healthiest channel.
type ConcurrencyProvider func(channelID int64) int

type Selector struct {
	repo   Repository
	clock  Clock
	random Random
	// latencyAware enables latency-weighted selection within a priority tier.
	latencyAware bool
	latency      LatencyProvider
	// errorAware penalizes channels with a high EWMA failure propensity.
	errorAware bool
	errorRate  ErrorProvider
	// circuitWeight is optional so the selector remains usable in unit tests
	// and lightweight callers that do not own a proxy circuit breaker.
	circuitWeight CircuitWeightProvider
	// concurrencyAware applies an in-flight burst guard: channels at or above
	// the limit are nearly skipped so spikes spread across the fleet.
	concurrencyAware bool
	concurrencyLimit int
	inflight         ConcurrencyProvider
	// sticky is the optional affinity store; nil disables sticky routing.
	sticky *StickyStore
	// stableFirstEnabled gates the 1/N grayscale draw; stableFirstDenominator
	// is the draw base (25 = a grayscale channel gets 1/25 of traffic).
	stableFirstEnabled     bool
	stableFirstDenominator int
}

// SetSticky installs the sticky-session store. Nil (or never called) disables
// sticky routing; session keys then never influence selection.
func (s *Selector) SetSticky(store *StickyStore) {
	s.sticky = store
}

// SetLatencyAware turns latency-weighted picking on/off. provider may be nil,
// in which case channels without latency data keep their plain weight.
func (s *Selector) SetLatencyAware(enabled bool, provider LatencyProvider) {
	s.latencyAware = enabled
	s.latency = provider
}

// SetStableFirst turns the 1/N grayscale pool on/off. denominator must be > 1
// for the pool to have any effect; values <= 1 disable the draw.
func (s *Selector) SetStableFirst(enabled bool, denominator int) {
	s.stableFirstEnabled = enabled && denominator > 1
	s.stableFirstDenominator = denominator
}

// SetErrorAware turns error-propensity penalization on/off. provider may be
// nil, in which case channels without failure samples keep their full weight.
func (s *Selector) SetErrorAware(enabled bool, provider ErrorProvider) {
	s.errorAware = enabled
	s.errorRate = provider
}

// SetCircuitAware wires the proxy's per-channel×model circuit state into
// selection and route explanations. A nil provider disables this extra gate.
func (s *Selector) SetCircuitAware(provider CircuitWeightProvider) {
	s.circuitWeight = provider
}

// SetConcurrencyAware turns the in-flight burst guard on/off. limit is the
// per-channel concurrency ceiling; channels at or above it are nearly skipped.
// provider may be nil, in which case the guard is inert.
func (s *Selector) SetConcurrencyAware(enabled bool, limit int, provider ConcurrencyProvider) {
	s.concurrencyAware = enabled && limit > 0 && provider != nil
	s.concurrencyLimit = limit
	s.inflight = provider
}

func New(repo Repository) *Selector {
	return NewWithDependencies(repo, systemClock{}, &lockedRandom{r: rand.New(rand.NewSource(time.Now().UnixNano()))})
}

func NewWithDependencies(repo Repository, clock Clock, random Random) *Selector {
	return &Selector{repo: repo, clock: clock, random: random}
}

func (s *Selector) Explain(ctx context.Context, model string) (Explanation, error) {
	return s.ExplainWithSession(ctx, model, "")
}

// ExplainWithSession is Explain with an optional session key: the response
// carries the sticky binding for that session when one exists.
func (s *Selector) ExplainWithSession(ctx context.Context, model, sessionKey string) (Explanation, error) {
	return s.evaluateWithSession(ctx, model, nil, sessionKey)
}

func (s *Selector) Select(ctx context.Context, model string, excluded map[int64]struct{}) (Decision, error) {
	return s.SelectSticky(ctx, model, excluded, "")
}

// SelectSticky selects a channel for a request, preferring the channel bound
// to the session key when it is still eligible. A bound channel that is
// cooling down, disabled, or already attempted in this request is escaped
// (StickyReason is set) and a normal weighted/latency pick happens instead.
func (s *Selector) SelectSticky(ctx context.Context, model string, excluded map[int64]struct{}, sessionKey string) (Decision, error) {
	explanation, err := s.evaluateWithSession(ctx, model, excluded, sessionKey)
	if err != nil {
		return Decision{}, err
	}
	eligible := make([]domain.RoutingCandidate, 0, len(explanation.Candidates))
	var priority int
	prioritySet := false
	for _, evaluation := range explanation.Candidates {
		if !evaluation.Eligible {
			continue
		}
		if !prioritySet {
			priority = evaluation.Candidate.Member.Priority
			prioritySet = true
		}
		if evaluation.Candidate.Member.Priority == priority {
			eligible = append(eligible, evaluation.Candidate)
		}
	}
	if len(eligible) == 0 {
		return Decision{Explanation: explanation}, ErrNoEligible
	}
	explanation.SelectedPriority = &priority
	// A sticky hit is deterministic: the bound channel is the answer whenever
	// it is still eligible in the selected priority tier, so no random pick
	// happens for it.
	var selected domain.RoutingCandidate
	selectedSet := false
	if explanation.StickyHit && explanation.StickyChannelID != nil {
		// Binding outranks the priority tier: the bound channel is chosen even
		// when a higher-priority member appeared since the binding was made.
		// Channel continuity (prompt cache, multi-turn coherence) wins over
		// tier order; the binding still yields when the channel is excluded,
		// cooled, or otherwise ineligible (no hard pinning).
		for _, candidate := range explanation.Candidates {
			if !candidate.Eligible {
				continue
			}
			if candidate.Candidate.Channel.ID == *explanation.StickyChannelID {
				selected = candidate.Candidate
				selectedSet = true
				break
			}
		}
	}
	if !selectedSet {
		if s.stableFirstEnabled && s.stableFirstDenominator > 1 {
			selected = s.pickWithGray(eligible, explanation.RoutingMode)
			explanation.StableFirstDenominator = s.stableFirstDenominator
			explanation.StableFirstHit = selected.Channel.StableFirst
		} else {
			selected = s.pick(eligible, explanation.RoutingMode)
		}
	}
	if sessionKey != "" && explanation.StickyChannelID != nil && s.sticky != nil {
		if selected.Channel.ID == *explanation.StickyChannelID {
			s.sticky.RecordHit()
		} else if explanation.StickyReason != "" {
			s.sticky.RecordEscape()
		}
	}
	return Decision{Explanation: explanation, Selected: selected}, nil
}

func (s *Selector) evaluate(ctx context.Context, model string, excluded map[int64]struct{}) (Explanation, error) {
	if err := ctx.Err(); err != nil {
		return Explanation{}, err
	}
	route, candidates, err := s.repo.RoutingCandidates(model)
	if err != nil {
		return Explanation{}, err
	}
	if route == nil {
		return Explanation{}, ErrRouteNotFound
	}
	mappingJSON := route.MappingJSON
	now := s.clock.Now().UTC()
	evaluations := make([]Evaluation, 0, len(candidates))
	for _, candidate := range candidates {
		reasons := make([]Reason, 0, 2)
		circuitWeight := 1.0
		if !candidate.Member.Enabled {
			reasons = append(reasons, ReasonMemberDisabled)
		}
		if candidate.Channel.Status != domain.StatusEnabled {
			reasons = append(reasons, ReasonChannelDisabled)
		}
		if !candidate.CredentialUsable {
			reasons = append(reasons, ReasonCredentialAbsent)
		}
		if candidate.Member.CooldownUntil != nil && candidate.Member.CooldownUntil.After(now) {
			reasons = append(reasons, ReasonCoolingDown)
		}
		if _, ok := excluded[candidate.Channel.ID]; ok {
			reasons = append(reasons, ReasonExcluded)
		}
		if candidate.Member.Weight < 0 {
			reasons = append(reasons, ReasonInvalidWeight)
		}
		if s.circuitWeight != nil {
			circuitWeight = s.circuitWeight(candidate.Channel.ID, model)
			if circuitWeight <= 0 {
				reasons = append(reasons, ReasonCircuitOpen)
			}
		}
		score := s.scoreFor(candidate, route.RoutingMode)
		if circuitWeight <= 0 {
			score = 0
		} else if circuitWeight < 1 {
			score *= circuitWeight
		}
		evaluations = append(evaluations, Evaluation{
			Candidate: candidate,
			Eligible:  len(reasons) == 0,
			Reasons:   reasons,
			Score:     score,
		})
	}
	sort.SliceStable(evaluations, func(i, j int) bool {
		left, right := evaluations[i].Candidate.Member, evaluations[j].Candidate.Member
		if left.Priority != right.Priority {
			return left.Priority > right.Priority
		}
		return left.ID < right.ID
	})
	return Explanation{
		Model:            model,
		RouteID:          route.ID,
		RouteMappingJSON: mappingJSON,
		RoutingMode:      domain.NormalizeRoutingMode(route.RoutingMode),
		EvaluatedAt:      now,
		Candidates:       evaluations,
		RetryTimesOverride:        route.RetryTimes,
		ChannelRetryTimesOverride: route.ChannelRetryTimes,
	}, nil
}

// evaluateWithSession runs the plain evaluation and annotates the sticky
// binding for the session key when one exists.
func (s *Selector) evaluateWithSession(ctx context.Context, model string, excluded map[int64]struct{}, sessionKey string) (Explanation, error) {
	explanation, err := s.evaluate(ctx, model, excluded)
	if err != nil {
		return explanation, err
	}
	explanation.SessionKey = sessionKey
	if s.sticky == nil || sessionKey == "" {
		return explanation, nil
	}
	channelID, ok := s.sticky.Lookup(sessionKey, explanation.EvaluatedAt)
	if !ok {
		return explanation, nil
	}
	explanation.StickyChannelID = &channelID
	for _, evaluation := range explanation.Candidates {
		if evaluation.Candidate.Channel.ID != channelID {
			continue
		}
		if !evaluation.Eligible {
			reasons := make([]string, 0, len(evaluation.Reasons))
			for _, reason := range evaluation.Reasons {
				reasons = append(reasons, string(reason))
			}
			explanation.StickyReason = strings.Join(reasons, ",")
		} else {
			explanation.StickyHit = true
		}
		break
	}
	return explanation, nil
}

// pickWithGray applies the stable-first draw: with probability 1/N the pick is
// made among grayscale channels only; otherwise among stable channels only.
// If every eligible candidate is grayscale (e.g. a brand-new fleet being
// validated), the draw is bypassed and a normal pick happens so traffic is
// never dropped. The pick inside each pool still honors latency/error/weight.
func (s *Selector) pickWithGray(candidates []domain.RoutingCandidate, mode string) domain.RoutingCandidate {
	var gray, stable []domain.RoutingCandidate
	for _, candidate := range candidates {
		if candidate.Channel.StableFirst {
			gray = append(gray, candidate)
		} else {
			stable = append(stable, candidate)
		}
	}
	if len(gray) == 0 {
		return s.pick(stable, mode)
	}
	if len(stable) == 0 || s.random.Intn(s.stableFirstDenominator) == 0 {
		return s.pick(gray, mode)
	}
	return s.pick(stable, mode)
}

// scoreFor computes the stable policy score for one candidate under the given
// route mode: base weight × latency factor × error factor. It deliberately
// excludes the concurrency guard so the admin UI shows a stable, explainable
// effective weight instead of a number that flaps with in-flight requests.
func (s *Selector) scoreFor(candidate domain.RoutingCandidate, mode string) float64 {
	latencyAware := s.latencyAware
	errorAware := s.errorAware
	switch domain.NormalizeRoutingMode(mode) {
	case domain.RoutingModeLatency:
		latencyAware = true
	case domain.RoutingModeWeighted:
		latencyAware = false
		errorAware = false
	case domain.RoutingModeAdaptive:
		latencyAware = true
		errorAware = true
	}
	weight := float64(candidate.Member.Weight)
	if weight <= 0 {
		weight = 1
	}
	score := weight
	if latencyAware && s.latency != nil {
		if latency, ok := s.latency(candidate.Channel.ID, candidate.ModelPattern); ok && latency > 0 {
			score = weight * (baseLatencyMs / (baseLatencyMs + latency))
		}
	}
	if errorAware && s.errorRate != nil {
		if propensity, ok := s.errorRate(candidate.Channel.ID, candidate.ModelPattern); ok && propensity > 0 {
			factor := 1 - propensity
			if factor < 0.05 {
				factor = 0.05
			}
			score *= factor
		}
	}
	return score
}

// pick resolves the effective picking strategy. Auto follows global policy;
// latency forces latency scoring; weighted uses only member priority/weight;
// adaptive enables both latency and error scoring for this model.
func (s *Selector) pick(candidates []domain.RoutingCandidate, mode string) domain.RoutingCandidate {
	latencyAware := s.latencyAware
	errorAware := s.errorAware
	switch domain.NormalizeRoutingMode(mode) {
	case domain.RoutingModeLatency:
		latencyAware = true
	case domain.RoutingModeWeighted:
		latencyAware = false
		errorAware = false
	case domain.RoutingModeAdaptive:
		latencyAware = true
		errorAware = true
	}
	if latencyAware && s.latency != nil {
		return s.pickLatencyAware(candidates, errorAware)
	}
	if errorAware && s.errorRate != nil {
		return s.pickErrorAware(candidates)
	}
	return s.pickWeighted(candidates)
}

// concurrencyFactor returns the burst-guard share multiplier for a channel:
// 1 when the guard is off or the channel is idle, (limit-inflight)/limit while
// it is busy, and a small floor (0.01) at or above the limit so a fully
// saturated fleet still spreads traffic instead of failing the pick.
func (s *Selector) concurrencyFactor(channelID int64) float64 {
	if !s.concurrencyAware || s.inflight == nil || s.concurrencyLimit <= 0 {
		return 1
	}
	inflight := s.inflight(channelID)
	if inflight >= s.concurrencyLimit {
		return 0.01
	}
	return float64(s.concurrencyLimit-inflight) / float64(s.concurrencyLimit)
}

// baseLatencyMs normalizes latency scoring so a 3000 ms channel keeps half its
// base weight and a 300 ms channel keeps ~91%. Raised from 1000 so a merely
// slow channel (2-4 s) keeps a meaningful share — failures are punished far
// harder than slowness.
const baseLatencyMs = 3000.0

// pickLatencyAware weights each candidate by weight / (1 + latency/base) so
// slower channels lose share within the same priority tier. Channels without
// latency samples keep their full weight (cold start). When error-aware is
// also enabled, the failure propensity (0..1) additionally scales the score by
// (1 - error), so a channel with a 0.5 error EMA keeps half its share and
// recovers as successes decay the EMA.
func (s *Selector) pickLatencyAware(candidates []domain.RoutingCandidate, errorAware bool) domain.RoutingCandidate {
	type scored struct {
		candidate domain.RoutingCandidate
		score     float64
	}
	scoredList := make([]scored, 0, len(candidates))
	total := 0.0
	for _, candidate := range candidates {
		weight := float64(candidate.Member.Weight)
		if weight <= 0 {
			weight = 1
		}
		score := weight
		if latency, ok := s.latency(candidate.Channel.ID, candidate.ModelPattern); ok && latency > 0 {
			score = weight * (baseLatencyMs / (baseLatencyMs + latency))
		}
		if errorAware && s.errorRate != nil {
			if propensity, ok := s.errorRate(candidate.Channel.ID, candidate.ModelPattern); ok && propensity > 0 {
				factor := 1 - propensity
				if factor < 0.05 {
					factor = 0.05 // floor: an unhealthy channel keeps a small chance
				}
				score *= factor
			}
		}
		score *= s.concurrencyFactor(candidate.Channel.ID)
		scoredList = append(scoredList, scored{candidate: candidate, score: score})
		total += score
	}
	if total <= 0 || len(scoredList) == 0 {
		return candidates[s.random.Intn(len(candidates))]
	}
	value := s.random.Float64() * total
	for _, entry := range scoredList {
		if value < entry.score {
			return entry.candidate
		}
		value -= entry.score
	}
	return scoredList[len(scoredList)-1].candidate
}

// pickErrorAware weights candidates by weight × (1 - error propensity) when
// no latency data is available (pure error-aware mode).
func (s *Selector) pickErrorAware(candidates []domain.RoutingCandidate) domain.RoutingCandidate {
	type scored struct {
		candidate domain.RoutingCandidate
		score     float64
	}
	scoredList := make([]scored, 0, len(candidates))
	total := 0.0
	for _, candidate := range candidates {
		weight := float64(candidate.Member.Weight)
		if weight <= 0 {
			weight = 1
		}
		score := weight
		if propensity, ok := s.errorRate(candidate.Channel.ID, candidate.ModelPattern); ok && propensity > 0 {
			factor := 1 - propensity
			if factor < 0.05 {
				factor = 0.05
			}
			score *= factor
		}
		score *= s.concurrencyFactor(candidate.Channel.ID)
		scoredList = append(scoredList, scored{candidate: candidate, score: score})
		total += score
	}
	if total <= 0 || len(scoredList) == 0 {
		return candidates[s.random.Intn(len(candidates))]
	}
	value := s.random.Float64() * total
	for _, entry := range scoredList {
		if value < entry.score {
			return entry.candidate
		}
		value -= entry.score
	}
	return scoredList[len(scoredList)-1].candidate
}

func (s *Selector) pickWeighted(candidates []domain.RoutingCandidate) domain.RoutingCandidate {
	type scored struct {
		candidate domain.RoutingCandidate
		score     float64
	}
	scoredList := make([]scored, 0, len(candidates))
	total := 0.0
	for _, candidate := range candidates {
		weight := float64(candidate.Member.Weight)
		if weight <= 0 {
			continue // zero-weight channels never win
		}
		score := weight * s.concurrencyFactor(candidate.Channel.ID)
		scoredList = append(scoredList, scored{candidate: candidate, score: score})
		total += score
	}
	if total <= 0 || len(scoredList) == 0 {
		// All weights zero (or every channel fully saturated): fall back to a
		// uniform pick among positive-weight channels so traffic is spread.
		var positive []domain.RoutingCandidate
		for _, candidate := range candidates {
			if candidate.Member.Weight > 0 {
				positive = append(positive, candidate)
			}
		}
		if len(positive) == 0 {
			return candidates[s.random.Intn(len(candidates))]
		}
		return positive[s.random.Intn(len(positive))]
	}
	value := s.random.Float64() * total
	for _, entry := range scoredList {
		if value < entry.score {
			return entry.candidate
		}
		value -= entry.score
	}
	return scoredList[len(scoredList)-1].candidate
}

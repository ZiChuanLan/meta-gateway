// Package routing evaluates and selects channels for exact model routes.
package routing

import (
	"context"
	"errors"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
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
	// ReasonSingleMode marks members skipped because the route is pinned to
	// another member via routing_mode=single.
	ReasonSingleMode Reason = "single_mode_other_member"
)

// SelectionConstraint narrows the candidate pool of ONE selection attempt. The
// proxy builds one per relay request and mutates it between failover rounds;
// each attribute is optional and they compose:
//
//   - ExcludedMembers skips individual route_members rows. This is the
//     per-variant exclusion used by alias groups ([A]/[B]/[次] of one logical
//     model live on the same channel as separate rows): failing one variant
//     must not blacklist the channel's healthy siblings.
//   - PreferChannel implements the two-layer fallback order. When nonzero and
//     that channel still has eligible members, the pick is restricted to the
//     channel's top priority tier and becomes deterministic (lowest member ID),
//     so retries exhaust [A]→[B]→[次] before moving to the next channel. When
//     the channel has no eligible members left, selection falls back to the
//     normal weighted/sticky behavior across the remaining fleet.
//   - RouteGroup narrows the pool to one route group (see RoutingCandidates);
//     empty means the route's 'default' group.
type SelectionConstraint struct {
	ExcludedMembers map[int64]struct{}
	PreferChannel   int64
	RouteGroup      string
}

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
	// PreferredChannelID / PreferredApplied record the intra-channel variant
	// walk: a retry preferred channel C and the restriction actually applied
	// (C still had eligible members). Absent on fresh attempts.
	PreferredChannelID *int64 `json:"preferred_channel_id,omitempty"`
	PreferredApplied   bool   `json:"preferred_applied,omitempty"`
	// RetryTimesOverride / ChannelRetryTimesOverride carry the route-level
	// retry policy (nil = follow the global runtime setting). The proxy reads
	// them from the selection decision.
	RetryTimesOverride             *int  `json:"retry_times_override,omitempty"`
	ChannelRetryTimesOverride      *int  `json:"channel_retry_times_override,omitempty"`
	StableFirstOverride            *bool `json:"stable_first_override,omitempty"`
	StableFirstDenominatorOverride *int  `json:"stable_first_denominator_override,omitempty"`
}

type Decision struct {
	Explanation
	Selected domain.RoutingCandidate `json:"selected"`
}

type Repository interface {
	RoutingCandidates(model, group string) (*domain.Route, []domain.RoutingCandidate, error)
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

// ConcurrencyProvider returns the number of in-flight relay attempts currently
// occupying a channel (0 when none). It is the input to the burst guard that
// keeps a sudden spike from overwhelming the healthiest channel.
type ConcurrencyProvider func(channelID int64) int

type Selector struct {
	repo   Repository
	clock  Clock
	random Random
	// settings is an immutable snapshot. Runtime settings are replaced as a
	// whole so request goroutines never observe a partially updated policy.
	settings atomic.Pointer[selectorSettings]
}

type selectorSettings struct {
	latencyAware           bool
	latency                LatencyProvider
	errorAware             bool
	errorRate              ErrorProvider
	concurrencyAware       bool
	concurrencyLimit       int
	inflight               ConcurrencyProvider
	sticky                 *StickyStore
	stableFirstEnabled     bool
	stableFirstDenominator int
}

func (s *Selector) loadSettings() *selectorSettings {
	if cfg := s.settings.Load(); cfg != nil {
		return cfg
	}
	return &selectorSettings{}
}

func (s *Selector) updateSettings(update func(*selectorSettings)) {
	for {
		old := s.settings.Load()
		next := &selectorSettings{}
		if old != nil {
			*next = *old
		}
		update(next)
		if s.settings.CompareAndSwap(old, next) {
			return
		}
	}
}

// SetSticky installs the sticky-session store. Nil (or never called) disables
// sticky routing; session keys then never influence selection.
func (s *Selector) SetSticky(store *StickyStore) {
	s.updateSettings(func(cfg *selectorSettings) { cfg.sticky = store })
}

// SetLatencyAware turns latency-weighted picking on/off. provider may be nil,
// in which case channels without latency data keep their plain weight.
func (s *Selector) SetLatencyAware(enabled bool, provider LatencyProvider) {
	s.updateSettings(func(cfg *selectorSettings) {
		cfg.latencyAware = enabled
		cfg.latency = provider
	})
}

// SetStableFirst turns the 1/N grayscale pool on/off. denominator must be > 1
// for the pool to have any effect; values <= 1 disable the draw.
func (s *Selector) SetStableFirst(enabled bool, denominator int) {
	s.updateSettings(func(cfg *selectorSettings) {
		cfg.stableFirstEnabled = enabled && denominator > 1
		cfg.stableFirstDenominator = denominator
	})
}

// SetErrorAware turns error-propensity penalization on/off. provider may be
// nil, in which case channels without failure samples keep their full weight.
func (s *Selector) SetErrorAware(enabled bool, provider ErrorProvider) {
	s.updateSettings(func(cfg *selectorSettings) {
		cfg.errorAware = enabled
		cfg.errorRate = provider
	})
}

// SetConcurrencyAware turns the in-flight burst guard on/off. limit is the
// per-channel concurrency ceiling; channels at or above it are nearly skipped.
// provider may be nil, in which case the guard is inert.
func (s *Selector) SetConcurrencyAware(enabled bool, limit int, provider ConcurrencyProvider) {
	s.updateSettings(func(cfg *selectorSettings) {
		cfg.concurrencyAware = enabled && limit > 0 && provider != nil
		cfg.concurrencyLimit = limit
		cfg.inflight = provider
	})
}

func New(repo Repository) *Selector {
	return NewWithDependencies(repo, systemClock{}, &lockedRandom{r: rand.New(rand.NewSource(time.Now().UnixNano()))})
}

func NewWithDependencies(repo Repository, clock Clock, random Random) *Selector {
	s := &Selector{repo: repo, clock: clock, random: random}
	s.settings.Store(&selectorSettings{})
	return s
}

func (s *Selector) Explain(ctx context.Context, model string) (Explanation, error) {
	return s.ExplainWithSession(ctx, model, "")
}

// ExplainWithSession is Explain with an optional session key: the response
// carries the sticky binding for that session when one exists.
func (s *Selector) ExplainWithSession(ctx context.Context, model, sessionKey string) (Explanation, error) {
	return s.evaluateWithSession(ctx, model, nil, sessionKey, nil)
}

func (s *Selector) Select(ctx context.Context, model string, excluded map[int64]struct{}, constraints ...*SelectionConstraint) (Decision, error) {
	return s.SelectSticky(ctx, model, excluded, "", constraints...)
}

// SelectSticky selects a channel for a request, preferring the channel bound
// to the session key when it is still eligible. A bound channel that is
// cooling down, disabled, or already attempted in this request is escaped
// (StickyReason is set) and a normal weighted/latency pick happens instead.
//
// An optional SelectionConstraint narrows this attempt: ExcludedMembers skips
// individual variants and PreferChannel restricts a retry to the previous
// channel's remaining members before any cross-channel move. The constraint
// outranks the sticky binding — a failing request finishes walking its
// channel's variants before session affinity is honored again.
func (s *Selector) SelectSticky(ctx context.Context, model string, excluded map[int64]struct{}, sessionKey string, constraints ...*SelectionConstraint) (Decision, error) {
	var constraint *SelectionConstraint
	for _, c := range constraints {
		if c != nil {
			constraint = c
			break
		}
	}
	explanation, err := s.evaluateWithSession(ctx, model, excluded, sessionKey, constraint)
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
		// Last resort: when every member is disqualified ONLY by an active
		// cooldown, try the least-bad one instead of hard-failing. A cooldown
		// is a health hint, not proof the channel is down — without this a
		// sole-member route would self-inflict an outage for the whole
		// cooldown window even after the upstream recovered. Members failed
		// earlier in THIS request (already_attempted) stay out, as do
		// disabled / absent-credential / non-pinned members.
		best := -1
		for i, evaluation := range explanation.Candidates {
			if len(evaluation.Reasons) != 1 || evaluation.Reasons[0] != ReasonCoolingDown {
				continue
			}
			if best == -1 || betterCoolingFallback(evaluation.Candidate, explanation.Candidates[best].Candidate) {
				best = i
			}
		}
		if best >= 0 {
			return Decision{Selected: explanation.Candidates[best].Candidate, Explanation: explanation}, nil
		}
		return Decision{Explanation: explanation}, ErrNoEligible
	}
	explanation.SelectedPriority = &priority
	var selected domain.RoutingCandidate
	selectedSet := false
	// Intra-channel variant walk: a retry that prefers channel C exhausts C's
	// remaining members (deterministically, lowest member ID first, top tier of
	// the channel) before any other channel is considered. Session affinity is
	// bypassed here — a request already failing over finishes its fallback walk.
	if constraint != nil && constraint.PreferChannel > 0 {
		preferredChannelID := constraint.PreferChannel
		var tier []domain.RoutingCandidate
		for _, candidate := range eligible {
			if candidate.Channel.ID == preferredChannelID {
				tier = append(tier, candidate)
			}
		}
		if len(tier) > 0 {
			topPriority := tier[0].Member.Priority
			for _, candidate := range tier[1:] {
				if candidate.Member.Priority > topPriority {
					topPriority = candidate.Member.Priority
				}
			}
			for _, candidate := range tier {
				if candidate.Member.Priority == topPriority {
					selected = candidate
					break // eligible is sorted by (priority desc, member id asc)
				}
			}
			selectedSet = true
			explanation.PreferredChannelID = &preferredChannelID
			explanation.PreferredApplied = true
			priority = topPriority
			explanation.SelectedPriority = &priority
		}
	}
	// A sticky hit is deterministic: the bound channel is the answer whenever
	// it is still eligible in the selected priority tier, so no random pick
	// happens for it.
	if !selectedSet && explanation.StickyHit && explanation.StickyChannelID != nil {
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
	cfg := s.loadSettings()
	if !selectedSet {
		grayEnabled := cfg.stableFirstEnabled
		grayDenominator := cfg.stableFirstDenominator
		if explanation.StableFirstOverride != nil {
			grayEnabled = *explanation.StableFirstOverride
		}
		if explanation.StableFirstDenominatorOverride != nil && *explanation.StableFirstDenominatorOverride > 1 {
			grayDenominator = *explanation.StableFirstDenominatorOverride
		}
		if grayEnabled && grayDenominator > 1 {
			selected = s.pickWithGray(eligible, explanation.RoutingMode, grayDenominator)
			explanation.StableFirstDenominator = grayDenominator
			explanation.StableFirstHit = selected.Channel.StableFirst
		} else {
			selected = s.pick(eligible, explanation.RoutingMode)
		}
	}
	if sessionKey != "" && explanation.StickyChannelID != nil && cfg.sticky != nil {
		if selected.Channel.ID == *explanation.StickyChannelID {
			cfg.sticky.RecordHit()
		} else if explanation.StickyReason != "" {
			cfg.sticky.RecordEscape()
		}
	}
	return Decision{Explanation: explanation, Selected: selected}, nil
}

func (s *Selector) evaluate(ctx context.Context, model string, excluded map[int64]struct{}, constraint *SelectionConstraint) (Explanation, error) {
	if err := ctx.Err(); err != nil {
		return Explanation{}, err
	}
	var group string
	if constraint != nil {
		group = constraint.RouteGroup
	}
	route, candidates, err := s.repo.RoutingCandidates(model, group)
	if err != nil {
		return Explanation{}, err
	}
	if route == nil {
		return Explanation{}, ErrRouteNotFound
	}
	mappingJSON := route.MappingJSON
	now := s.clock.Now().UTC()
	// Single mode: routing_mode=single pins the route to one member. When the
	// pinned member no longer exists (deleted), the pin is inert and the route
	// behaves as auto so traffic is never stranded.
	mode := domain.NormalizeRoutingMode(route.RoutingMode)
	var singlePin *int64
	if mode == domain.RoutingModeSingle && route.SingleMemberID != nil {
		for _, candidate := range candidates {
			if candidate.Member.ID == *route.SingleMemberID {
				singlePin = route.SingleMemberID
				break
			}
		}
	}
	evaluations := make([]Evaluation, 0, len(candidates))
	for _, candidate := range candidates {
		reasons := make([]Reason, 0, 2)
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
		if constraint != nil && len(constraint.ExcludedMembers) > 0 {
			if _, ok := constraint.ExcludedMembers[candidate.Member.ID]; ok {
				reasons = append(reasons, ReasonExcluded)
			}
		}
		if candidate.Member.Weight < 0 {
			reasons = append(reasons, ReasonInvalidWeight)
		}
		if singlePin != nil && candidate.Member.ID != *singlePin {
			reasons = append(reasons, ReasonSingleMode)
		}
		score := s.scoreFor(candidate, route.RoutingMode)
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
	// With exactly one usable channel there is nothing to fail over to:
	// cross-channel retry rounds count as 0 regardless of the stored override.
	retryTimesOverride := route.RetryTimes
	if singlePin != nil {
		zero := 0
		retryTimesOverride = &zero
	}
	return Explanation{
		Model:                          model,
		RouteID:                        route.ID,
		RouteMappingJSON:               mappingJSON,
		RoutingMode:                    mode,
		EvaluatedAt:                    now,
		Candidates:                     evaluations,
		RetryTimesOverride:             retryTimesOverride,
		ChannelRetryTimesOverride:      route.ChannelRetryTimes,
		StableFirstOverride:            route.StableFirst,
		StableFirstDenominatorOverride: route.StableFirstDenominator,
	}, nil
}

// evaluateWithSession runs the plain evaluation and annotates the sticky
// binding for the session key when one exists.
func (s *Selector) evaluateWithSession(ctx context.Context, model string, excluded map[int64]struct{}, sessionKey string, constraint *SelectionConstraint) (Explanation, error) {
	explanation, err := s.evaluate(ctx, model, excluded, constraint)
	if err != nil {
		return explanation, err
	}
	explanation.SessionKey = sessionKey
	sticky := s.loadSettings().sticky
	if sticky == nil || sessionKey == "" {
		return explanation, nil
	}
	channelID, ok := sticky.Lookup(sessionKey, explanation.EvaluatedAt)
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
func (s *Selector) pickWithGray(candidates []domain.RoutingCandidate, mode string, denominator int) domain.RoutingCandidate {
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
	if len(stable) == 0 || s.random.Intn(denominator) == 0 {
		return s.pick(gray, mode)
	}
	return s.pick(stable, mode)
}

// scoreFor computes the stable policy score for one candidate under the given
// route mode: base weight × latency factor × error factor. It deliberately
// excludes the concurrency guard so the admin UI shows a stable, explainable
// effective weight instead of a number that flaps with in-flight requests.
func (s *Selector) scoreFor(candidate domain.RoutingCandidate, mode string) float64 {
	cfg := s.loadSettings()
	latencyAware := cfg.latencyAware
	errorAware := cfg.errorAware
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
	if latencyAware && cfg.latency != nil {
		if latency, ok := cfg.latency(candidate.Channel.ID, candidate.ModelPattern); ok && latency > 0 {
			score = weight * (baseLatencyMs / (baseLatencyMs + latency))
		}
	}
	if errorAware && cfg.errorRate != nil {
		if propensity, ok := cfg.errorRate(candidate.Channel.ID, candidate.ModelPattern); ok && propensity > 0 {
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
	cfg := s.loadSettings()
	latencyAware := cfg.latencyAware
	errorAware := cfg.errorAware
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
	if latencyAware && cfg.latency != nil {
		return s.pickLatencyAware(candidates, errorAware)
	}
	if errorAware && cfg.errorRate != nil {
		return s.pickErrorAware(candidates)
	}
	return s.pickWeighted(candidates)
}

// betterCoolingFallback orders last-resort picks among cooling members:
// higher priority wins, then the cooldown expiring soonest, then the lower
// member id for determinism.
func betterCoolingFallback(a, b domain.RoutingCandidate) bool {
	if a.Member.Priority != b.Member.Priority {
		return a.Member.Priority > b.Member.Priority
	}
	if a.Member.CooldownUntil != nil && b.Member.CooldownUntil != nil &&
		!a.Member.CooldownUntil.Equal(*b.Member.CooldownUntil) {
		return a.Member.CooldownUntil.Before(*b.Member.CooldownUntil)
	}
	return a.Member.ID < b.Member.ID
}

// concurrencyFactor returns the burst-guard share multiplier for a channel:
// 1 when the guard is off or the channel is idle, (limit-inflight)/limit while
// it is busy, and a small floor (0.01) at or above the limit so a fully
// saturated fleet still spreads traffic instead of failing the pick.
func (s *Selector) concurrencyFactor(channelID int64) float64 {
	cfg := s.loadSettings()
	if !cfg.concurrencyAware || cfg.inflight == nil || cfg.concurrencyLimit <= 0 {
		return 1
	}
	inflight := cfg.inflight(channelID)
	if inflight >= cfg.concurrencyLimit {
		return 0.01
	}
	return float64(cfg.concurrencyLimit-inflight) / float64(cfg.concurrencyLimit)
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
	cfg := s.loadSettings()
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
		if cfg.latency != nil {
			if latency, ok := cfg.latency(candidate.Channel.ID, candidate.ModelPattern); ok && latency > 0 {
				score = weight * (baseLatencyMs / (baseLatencyMs + latency))
			}
		}
		if errorAware && cfg.errorRate != nil {
			if propensity, ok := cfg.errorRate(candidate.Channel.ID, candidate.ModelPattern); ok && propensity > 0 {
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
	cfg := s.loadSettings()
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
		if cfg.errorRate != nil {
			if propensity, ok := cfg.errorRate(candidate.Channel.ID, candidate.ModelPattern); ok && propensity > 0 {
				factor := 1 - propensity
				if factor < 0.05 {
					factor = 0.05
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

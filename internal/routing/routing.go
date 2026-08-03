// Package routing evaluates and selects channels for exact model routes.
package routing

import (
	"context"
	"errors"
	"math/rand"
	"sort"
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
)

type Evaluation struct {
	Candidate domain.RoutingCandidate `json:"candidate"`
	Eligible  bool                    `json:"eligible"`
	Reasons   []Reason                `json:"reasons"`
}

type Explanation struct {
	Model            string       `json:"model"`
	RouteID          int64        `json:"route_id"`
	RouteMappingJSON string       `json:"route_mapping_json,omitempty"`
	EvaluatedAt      time.Time    `json:"evaluated_at"`
	SelectedPriority *int         `json:"selected_priority,omitempty"`
	Candidates       []Evaluation `json:"candidates"`
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

// LatencyProvider returns the smoothed latency (ms) for a channel, with false
// when no sample exists yet.
type LatencyProvider func(channelID int64) (float64, bool)

type Selector struct {
	repo   Repository
	clock  Clock
	random Random
	// latencyAware enables latency-weighted selection within a priority tier.
	latencyAware bool
	latency      LatencyProvider
}

// SetLatencyAware turns on latency-weighted picking. provider may be nil,
// in which case channels without latency data keep their plain weight.
func (s *Selector) SetLatencyAware(provider LatencyProvider) {
	s.latencyAware = provider != nil
	s.latency = provider
}

func New(repo Repository) *Selector {
	return NewWithDependencies(repo, systemClock{}, &lockedRandom{r: rand.New(rand.NewSource(time.Now().UnixNano()))})
}

func NewWithDependencies(repo Repository, clock Clock, random Random) *Selector {
	return &Selector{repo: repo, clock: clock, random: random}
}

func (s *Selector) Explain(ctx context.Context, model string) (Explanation, error) {
	return s.evaluate(ctx, model, nil)
}

func (s *Selector) Select(ctx context.Context, model string, excluded map[int64]struct{}) (Decision, error) {
	explanation, err := s.evaluate(ctx, model, excluded)
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
	return Decision{Explanation: explanation, Selected: s.pick(eligible)}, nil
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
		evaluations = append(evaluations, Evaluation{Candidate: candidate, Eligible: len(reasons) == 0, Reasons: reasons})
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
		EvaluatedAt:      now,
		Candidates:       evaluations,
	}, nil
}

func (s *Selector) pick(candidates []domain.RoutingCandidate) domain.RoutingCandidate {
	if s.latencyAware && s.latency != nil {
		return s.pickLatencyAware(candidates)
	}
	return s.pickWeighted(candidates)
}

// pickLatencyAware weights each candidate by weight / (1 + latency/base) so
// slower channels lose share within the same priority tier. Channels without
// latency samples keep their full weight (cold start).
func (s *Selector) pickLatencyAware(candidates []domain.RoutingCandidate) domain.RoutingCandidate {
	const baseLatencyMs = 1000.0
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
		if latency, ok := s.latency(candidate.Channel.ID); ok && latency > 0 {
			score = weight * (baseLatencyMs / (baseLatencyMs + latency))
		}
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
	total := 0
	for _, candidate := range candidates {
		if candidate.Member.Weight > 0 {
			total += candidate.Member.Weight
		}
	}
	if total == 0 {
		return candidates[s.random.Intn(len(candidates))]
	}
	value := s.random.Intn(total)
	for _, candidate := range candidates {
		if candidate.Member.Weight <= 0 {
			continue
		}
		if value < candidate.Member.Weight {
			return candidate
		}
		value -= candidate.Member.Weight
	}
	return candidates[len(candidates)-1]
}

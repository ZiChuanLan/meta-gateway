package routing

import "github.com/lan/meta-gateway/internal/domain"

// scoreFor computes the stable policy score for one candidate under the given
// route mode: base weight × latency factor × error factor. Latency and error
// lookups are keyed by the candidate's UPSTREAM model name (see
// upstreamModelName), not the route pattern — one route may reach several
// differently named models and they must be scored separately. It deliberately
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
	return adaptiveScore(cfg, candidate, latencyAware, errorAware)
}

func adaptiveScore(cfg *selectorSettings, candidate domain.RoutingCandidate, latencyAware, errorAware bool) float64 {
	weight := float64(candidate.Member.Weight)
	if weight <= 0 {
		weight = 1
	}
	score := weight
	if latencyAware && cfg.latency != nil {
		if latency, ok := cfg.latency(candidate.Channel.ID, upstreamModelName(candidate)); ok && latency > 0 {
			score = weight * (baseLatencyMs / (baseLatencyMs + latency))
		}
	}
	if errorAware && cfg.errorRate != nil {
		if propensity, ok := cfg.errorRate(candidate.Channel.ID, upstreamModelName(candidate)); ok && propensity > 0 {
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

// Adaptive paths share one formula with the console explanation. Only the
// transient concurrency factor is omitted from the explanatory score.
func (s *Selector) pickLatencyAware(candidates []domain.RoutingCandidate, errorAware bool) domain.RoutingCandidate {
	return s.pickAdaptive(candidates, true, errorAware)
}

func (s *Selector) pickErrorAware(candidates []domain.RoutingCandidate) domain.RoutingCandidate {
	return s.pickAdaptive(candidates, false, true)
}

func (s *Selector) pickAdaptive(candidates []domain.RoutingCandidate, latencyAware, errorAware bool) domain.RoutingCandidate {
	if len(candidates) == 0 {
		return domain.RoutingCandidate{}
	}
	cfg := s.loadSettings()
	scores := make([]float64, len(candidates))
	total := 0.0
	for index, candidate := range candidates {
		scores[index] = adaptiveScore(cfg, candidate, latencyAware, errorAware) * s.concurrencyFactor(candidate.Channel.ID)
		total += scores[index]
	}
	if total <= 0 {
		return candidates[s.random.Intn(len(candidates))]
	}
	value := s.random.Float64() * total
	for index, score := range scores {
		if value < score {
			return candidates[index]
		}
		value -= score
	}
	return candidates[len(candidates)-1]
}

// upstreamModelName is the health-record key for a candidate: the name it
// actually sends upstream. An alias member ("cn:x") and a plain member of the
// same route therefore keep separate latency and error records, instead of
// sharing one sample that made them always score identically.
func upstreamModelName(candidate domain.RoutingCandidate) string {
	return domain.UpstreamModelName(candidate)
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

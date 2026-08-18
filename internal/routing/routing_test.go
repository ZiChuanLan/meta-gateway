package routing

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
)

type fakeRepo struct {
	route      *domain.Route
	candidates []domain.RoutingCandidate
}

func (r fakeRepo) RoutingCandidates(string) (*domain.Route, []domain.RoutingCandidate, error) {
	return r.route, r.candidates, nil
}

type fakeClock struct{ now time.Time }

func (c fakeClock) Now() time.Time { return c.now }

type fakeRandom struct{ values []int }

func (r *fakeRandom) Intn(n int) int {
	value := r.values[0]
	r.values = r.values[1:]
	return value % n
}

func (r *fakeRandom) Float64() float64 {
	value := float64(r.values[0])
	r.values = r.values[1:]
	if value < 0 {
		return 0
	}
	return value
}

func candidate(id, priority, weight int64) domain.RoutingCandidate {
	return domain.RoutingCandidate{
		Member:           domain.RouteMember{ID: id, RouteID: 1, ChannelID: id, Priority: int(priority), Weight: int(weight), Enabled: true},
		Channel:          domain.Channel{ID: id, Status: domain.StatusEnabled},
		CredentialUsable: true,
	}
}

func TestSelectUsesHighestPriorityAndExclusions(t *testing.T) {
	now := time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC)
	repo := fakeRepo{route: &domain.Route{ID: 1}, candidates: []domain.RoutingCandidate{
		candidate(1, 10, 100), candidate(2, 20, 100), candidate(3, 20, 100),
	}}
	selector := NewWithDependencies(repo, fakeClock{now}, &fakeRandom{values: []int{0}})
	decision, err := selector.Select(context.Background(), "gpt-test", map[int64]struct{}{2: {}})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Selected.Channel.ID != 3 || *decision.SelectedPriority != 20 {
		t.Fatalf("unexpected decision: %+v", decision)
	}
}

func TestSelectWeightedAndAllZeroFallback(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name       string
		candidates []domain.RoutingCandidate
		random     int
		want       int64
	}{
		{"weighted first", []domain.RoutingCandidate{candidate(1, 5, 1), candidate(2, 5, 3)}, 0, 1},
		{"weighted second", []domain.RoutingCandidate{candidate(1, 5, 1), candidate(2, 5, 3)}, 1, 2},
		{"zero excluded when positive exists", []domain.RoutingCandidate{candidate(1, 5, 0), candidate(2, 5, 2)}, 0, 2},
		{"all zero uniform", []domain.RoutingCandidate{candidate(1, 5, 0), candidate(2, 5, 0)}, 1, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			selector := NewWithDependencies(fakeRepo{route: &domain.Route{ID: 1}, candidates: tt.candidates}, fakeClock{now}, &fakeRandom{values: []int{tt.random}})
			decision, err := selector.Select(context.Background(), "model", nil)
			if err != nil {
				t.Fatal(err)
			}
			if decision.Selected.Channel.ID != tt.want {
				t.Fatalf("got channel %d, want %d", decision.Selected.Channel.ID, tt.want)
			}
		})
	}
}

func TestExplainSharesEligibilityAndStableOrder(t *testing.T) {
	now := time.Date(2026, 7, 14, 1, 2, 3, 0, time.UTC)
	cooling := candidate(3, 20, 100)
	until := now.Add(time.Minute)
	cooling.Member.CooldownUntil = &until
	disabled := candidate(1, 30, 100)
	disabled.Member.Enabled = false
	selector := NewWithDependencies(fakeRepo{route: &domain.Route{ID: 9}, candidates: []domain.RoutingCandidate{candidate(2, 10, 100), cooling, disabled}}, fakeClock{now}, &fakeRandom{values: []int{0}})
	explanation, err := selector.Explain(context.Background(), "model")
	if err != nil {
		t.Fatal(err)
	}
	if explanation.Candidates[0].Candidate.Member.ID != 1 || explanation.Candidates[1].Reasons[0] != ReasonCoolingDown {
		t.Fatalf("unexpected explanation: %+v", explanation)
	}
}

func TestSelectNoRouteAndNoEligible(t *testing.T) {
	selector := NewWithDependencies(fakeRepo{}, fakeClock{}, &fakeRandom{})
	if _, err := selector.Select(context.Background(), "missing", nil); !errors.Is(err, ErrRouteNotFound) {
		t.Fatalf("expected route error, got %v", err)
	}
	disabled := candidate(1, 1, 1)
	disabled.Channel.Status = domain.StatusDisabled
	selector = NewWithDependencies(fakeRepo{route: &domain.Route{ID: 1}, candidates: []domain.RoutingCandidate{disabled}}, fakeClock{}, &fakeRandom{})
	if _, err := selector.Select(context.Background(), "model", nil); !errors.Is(err, ErrNoEligible) {
		t.Fatalf("expected eligibility error, got %v", err)
	}
}

func TestPickLatencyAwarePrefersFastChannel(t *testing.T) {
	// Two candidates, equal weight; channel 1 fast, channel 2 slow.
	fast := candidate(1, 0, 100)
	slow := candidate(2, 0, 100)
	latency := func(channelID int64, model string) (float64, bool) {
		switch channelID {
		case 1:
			return 200, true
		case 2:
			return 5000, true
		}
		return 0, false
	}
	selector := NewWithDependencies(fakeRepo{}, systemClock{}, &fakeRandom{values: []int{0, 0, 0, 0, 0, 0, 0, 0, 0, 0}})
	selector.SetLatencyAware(true, latency)

	fastCount, slowCount := 0, 0
	for i := 0; i < 10; i++ {
		picked := selector.pick([]domain.RoutingCandidate{fast, slow}, domain.RoutingModeAuto)
		if picked.Channel.ID == 1 {
			fastCount++
		} else {
			slowCount++
		}
	}
	// Fast channel should dominate (score 100*1000/1200 vs 100*1000/6000).
	if fastCount < 7 {
		t.Fatalf("fast channel picked %d/10, want majority", fastCount)
	}
	_ = slowCount
}

func TestPickLatencyAwareColdStartKeepsWeight(t *testing.T) {
	// No latency data: plain weighted behavior.
	a := candidate(1, 0, 100)
	b := candidate(2, 0, 100)
	selector := NewWithDependencies(fakeRepo{}, systemClock{}, &fakeRandom{values: []int{0}})
	selector.SetLatencyAware(true, func(int64, string) (float64, bool) { return 0, false })
	picked := selector.pick([]domain.RoutingCandidate{a, b}, domain.RoutingModeAuto)
	if picked.Channel.ID != 1 {
		t.Fatalf("cold start picked %d, want 1 (random=0)", picked.Channel.ID)
	}
}

func TestPickRouteModeResolution(t *testing.T) {
	fast := candidate(1, 0, 100)
	slow := candidate(2, 0, 100)
	latency := func(channelID int64, model string) (float64, bool) {
		switch channelID {
		case 1:
			return 200, true
		case 2:
			return 5000, true
		}
		return 0, false
	}

	tests := []struct {
		name     string
		global   bool
		mode     string
		wantFast bool // true = expect latency-aware fast majority
	}{
		{"latency overrides global off", false, domain.RoutingModeLatency, true},
		{"auto follows global off", false, domain.RoutingModeAuto, false},
		{"auto follows global on", true, domain.RoutingModeAuto, true},
		{"weighted overrides global on", true, domain.RoutingModeWeighted, false},
		{"empty treated as auto global on", true, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Latency picking reads Float64 (0 picks the fast channel); weighted
			// picking reads Intn (150 % 200 >= 100 picks the slow channel).
			values := make([]int, 10)
			for i := range values {
				if !tt.wantFast {
					values[i] = 150
				}
			}
			selector := NewWithDependencies(fakeRepo{}, systemClock{}, &fakeRandom{values: values})
			selector.SetLatencyAware(tt.global, latency)

			fastCount := 0
			for i := 0; i < 10; i++ {
				picked := selector.pick([]domain.RoutingCandidate{fast, slow}, tt.mode)
				if picked.Channel.ID == 1 {
					fastCount++
				}
			}
			if tt.wantFast && fastCount < 7 {
				t.Fatalf("expected fast majority, got fast=%d/10", fastCount)
			}
			if !tt.wantFast && fastCount > 3 {
				t.Fatalf("expected weighted distribution, got fast=%d/10", fastCount)
			}
		})
	}
}

func TestAdaptiveModeCombinesLatencyAndErrorAwareScoring(t *testing.T) {
	fast := candidate(1, 0, 100)
	healthy := candidate(2, 0, 100)
	latency := func(channelID int64, model string) (float64, bool) {
		if channelID == 1 {
			return 100, true
		}
		return 900, true
	}
	errorRate := func(channelID int64, model string) (float64, bool) {
		if channelID == 1 {
			return 0.9, true
		}
		return 0, false
	}
	selector := NewWithDependencies(fakeRepo{}, systemClock{}, &fakeRandom{values: []int{0, 0, 0, 0, 0, 0, 0, 0, 0, 0}})
	selector.SetLatencyAware(false, latency)
	selector.SetErrorAware(false, errorRate)

	healthyCount := 0
	for i := 0; i < 10; i++ {
		picked := selector.pick([]domain.RoutingCandidate{healthy, fast}, domain.RoutingModeAdaptive)
		if picked.Channel.ID == 2 {
			healthyCount++
		}
	}
	if healthyCount < 7 {
		t.Fatalf("adaptive mode ignored error score: healthy=%d/10", healthyCount)
	}
}

func TestWeightedModeDisablesAdaptiveScoring(t *testing.T) {
	fast := candidate(1, 0, 100)
	slow := candidate(2, 0, 100)
	latency := func(channelID int64, model string) (float64, bool) {
		if channelID == 1 {
			return 100, true
		}
		return 5000, true
	}
	errorRate := func(channelID int64, model string) (float64, bool) {
		if channelID == 1 {
			return 0.9, true
		}
		return 0, false
	}
	selector := NewWithDependencies(fakeRepo{}, systemClock{}, &fakeRandom{values: []int{150, 150, 150}})
	selector.SetLatencyAware(true, latency)
	selector.SetErrorAware(true, errorRate)
	for i := 0; i < 3; i++ {
		picked := selector.pick([]domain.RoutingCandidate{fast, slow}, domain.RoutingModeWeighted)
		if picked.Channel.ID != 2 {
			t.Fatalf("weighted mode did not preserve weighted selection: picked=%d", picked.Channel.ID)
		}
	}
}

func TestExplainReportsRoutingMode(t *testing.T) {
	now := time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC)
	repo := fakeRepo{
		route:      &domain.Route{ID: 1, RoutingMode: domain.RoutingModeWeighted},
		candidates: []domain.RoutingCandidate{candidate(1, 10, 100)},
	}
	selector := NewWithDependencies(repo, fakeClock{now}, &fakeRandom{values: []int{0}})
	explanation, err := selector.Explain(context.Background(), "gpt-test")
	if err != nil {
		t.Fatal(err)
	}
	if explanation.RoutingMode != domain.RoutingModeWeighted {
		t.Fatalf("routing mode = %q, want %q", explanation.RoutingMode, domain.RoutingModeWeighted)
	}
}

func TestPickErrorAwarePenalizesFailingChannel(t *testing.T) {
	// Two candidates, equal weight; channel 1 has a high failure propensity,
	// channel 2 is clean.
	flaky := candidate(1, 0, 100)
	healthy := candidate(2, 0, 100)
	errorRate := func(channelID int64, model string) (float64, bool) {
		switch channelID {
		case 1:
			return 0.8, true
		case 2:
			return 0, false
		}
		return 0, false
	}
	selector := NewWithDependencies(fakeRepo{}, systemClock{}, &fakeRandom{values: []int{0, 0, 0, 0, 0, 0, 0, 0, 0, 0}})
	selector.SetErrorAware(true, errorRate)

	flakyCount, healthyCount := 0, 0
	for i := 0; i < 10; i++ {
		picked := selector.pick([]domain.RoutingCandidate{healthy, flaky}, domain.RoutingModeAuto)
		if picked.Channel.ID == 1 {
			flakyCount++
		} else {
			healthyCount++
		}
	}
	// Healthy channel score 100 vs flaky 100×0.2 = 20 — healthy must dominate.
	if healthyCount < 7 {
		t.Fatalf("healthy channel picked %d/10, want majority (flaky=%d)", healthyCount, flakyCount)
	}
}

func TestPickLatencyAndErrorAwareCombine(t *testing.T) {
	// Error propensity scales the latency-weighted score: a fast-but-flaky
	// channel must lose share to a slower-but-healthy one.
	fast := candidate(1, 0, 100)
	healthy := candidate(2, 0, 100)
	latency := func(channelID int64, model string) (float64, bool) {
		switch channelID {
		case 1:
			return 100, true
		case 2:
			return 900, true
		}
		return 0, false
	}
	errorRate := func(channelID int64, model string) (float64, bool) {
		if channelID == 1 {
			return 0.9, true
		}
		return 0, false
	}
	selector := NewWithDependencies(fakeRepo{}, systemClock{}, &fakeRandom{values: []int{0, 0, 0, 0, 0, 0, 0, 0, 0, 0}})
	selector.SetLatencyAware(true, latency)
	selector.SetErrorAware(true, errorRate)

	fastCount, healthyCount := 0, 0
	for i := 0; i < 10; i++ {
		picked := selector.pick([]domain.RoutingCandidate{healthy, fast}, domain.RoutingModeAuto)
		if picked.Channel.ID == 1 {
			fastCount++
		} else {
			healthyCount++
		}
	}
	// fast: 100×(1000/1100)×0.1 ≈ 9.1; healthy: 100×(1000/1900) ≈ 52.6.
	if healthyCount < 7 {
		t.Fatalf("healthy channel picked %d/10, want majority (fast=%d)", healthyCount, fastCount)
	}
}

func grayCandidate(id, priority, weight int64, gray bool) domain.RoutingCandidate {
	c := candidate(id, priority, weight)
	c.Channel.StableFirst = gray
	return c
}

func TestStableFirstDrawDefaultDenominator25(t *testing.T) {
	now := time.Now()
	gray := grayCandidate(2, 10, 100, true)
	// Draw value 0 → gray wins (1/25 hit).
	hit := NewWithDependencies(fakeRepo{route: &domain.Route{ID: 1}, candidates: []domain.RoutingCandidate{
		grayCandidate(1, 10, 100, false), gray,
	}}, fakeClock{now}, &fakeRandom{values: []int{0, 0}})
	hit.SetStableFirst(true, 25)
	if d, err := hit.Select(context.Background(), "gpt-test", nil); err != nil || d.Selected.Channel.ID != 2 {
		t.Fatalf("draw 0 must hit gray: id=%d err=%v", d.Selected.Channel.ID, err)
	}
	// Draws 1..24 → stable channel, gray never selected, metadata reports denominator.
	for _, draw := range []int{1, 12, 24} {
		t.Run(fmt.Sprintf("draw%d", draw), func(t *testing.T) {
			stable := NewWithDependencies(fakeRepo{route: &domain.Route{ID: 1}, candidates: []domain.RoutingCandidate{
				grayCandidate(1, 10, 100, false), gray,
			}}, fakeClock{now}, &fakeRandom{values: []int{draw, 0}})
			stable.SetStableFirst(true, 25)
			d, err := stable.Select(context.Background(), "gpt-test", nil)
			if err != nil {
				t.Fatal(err)
			}
			if d.Selected.Channel.ID == 2 {
				t.Fatalf("draw %d must not select gray under 24:1", draw)
			}
			if d.StableFirstDenominator != 25 {
				t.Fatalf("denominator metadata = %d want 25", d.StableFirstDenominator)
			}
		})
	}
}

func TestStableFirstDrawPicksGrayOnHit(t *testing.T) {
	now := time.Now()
	repo := fakeRepo{route: &domain.Route{ID: 1}, candidates: []domain.RoutingCandidate{
		grayCandidate(1, 10, 100, false), grayCandidate(2, 10, 100, true),
	}}
	// First Intn(2) == 0 -> grayscale draw; then pick among gray (weighted Intn).
	selector := NewWithDependencies(repo, fakeClock{now}, &fakeRandom{values: []int{0, 0}})
	selector.SetStableFirst(true, 2)
	decision, err := selector.Select(context.Background(), "gpt-test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Selected.Channel.ID != 2 {
		t.Fatalf("gray draw should select gray channel, got %d", decision.Selected.Channel.ID)
	}
	if !decision.StableFirstHit || decision.StableFirstDenominator != 2 {
		t.Fatalf("explanation missing gray metadata: hit=%v denom=%d", decision.StableFirstHit, decision.StableFirstDenominator)
	}
}

func TestStableFirstDrawSkipsGrayOnMiss(t *testing.T) {
	now := time.Now()
	repo := fakeRepo{route: &domain.Route{ID: 1}, candidates: []domain.RoutingCandidate{
		grayCandidate(1, 10, 100, false), grayCandidate(2, 10, 100, true),
	}}
	// First Intn(2) == 1 -> stable draw; gray channel must never win.
	// 20 values: 10 selections x (draw + weighted pick).
	values := make([]int, 20)
	for i := range values {
		values[i] = 1
	}
	selector := NewWithDependencies(repo, fakeClock{now}, &fakeRandom{values: values})
	selector.SetStableFirst(true, 2)
	for i := 0; i < 10; i++ {
		decision, err := selector.Select(context.Background(), "gpt-test", nil)
		if err != nil {
			t.Fatal(err)
		}
		if decision.Selected.Channel.ID == 2 {
			t.Fatalf("stable draw selected gray channel on miss (i=%d)", i)
		}
		if decision.StableFirstHit {
			t.Fatalf("stable draw reported gray hit")
		}
	}
}

func TestStableFirstAllGrayFallsBack(t *testing.T) {
	now := time.Now()
	repo := fakeRepo{route: &domain.Route{ID: 1}, candidates: []domain.RoutingCandidate{
		grayCandidate(1, 10, 100, true), grayCandidate(2, 10, 100, true),
	}}
	selector := NewWithDependencies(repo, fakeClock{now}, &fakeRandom{values: []int{5}})
	selector.SetStableFirst(true, 2)
	decision, err := selector.Select(context.Background(), "gpt-test", nil)
	if err != nil {
		t.Fatalf("all-gray fleet must not drop traffic: %v", err)
	}
	if decision.Selected.Channel.ID == 0 {
		t.Fatal("no channel selected")
	}
}

func TestStableFirstDisabledOrDenominatorOne(t *testing.T) {
	now := time.Now()
	repo := fakeRepo{route: &domain.Route{ID: 1}, candidates: []domain.RoutingCandidate{
		grayCandidate(1, 10, 1, false), grayCandidate(2, 10, 1, true),
	}}
	for _, tc := range []struct {
		name    string
		enabled bool
		denom   int
	}{
		{"disabled", false, 25},
		{"denominator one", true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selector := NewWithDependencies(repo, fakeClock{now}, &fakeRandom{values: []int{0, 0}})
			selector.SetStableFirst(tc.enabled, tc.denom)
			decision, err := selector.Select(context.Background(), "gpt-test", nil)
			if err != nil {
				t.Fatal(err)
			}
			// With the draw off, weighted picking can legitimately select either;
			// just ensure no panic and gray metadata absent.
			if decision.StableFirstDenominator != 0 {
				t.Fatalf("gray metadata present when draw disabled: %d", decision.StableFirstDenominator)
			}
		})
	}
}

// seqRandom draws 0..1 values in order, then cycles; used for deterministic
// weighted-pick assertions without integer-only fakeRandom.
type seqRandom struct{ draws []float64 }

func (r *seqRandom) Intn(n int) int {
	v := r.Float64()
	if v >= 1 {
		v = 0.999999
	}
	return int(v * float64(n))
}

func (r *seqRandom) Float64() float64 {
	if len(r.draws) == 0 {
		return 0.5
	}
	v := r.draws[0]
	r.draws = r.draws[1:]
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func TestPickConcurrencyAwarePenalizesBusyChannel(t *testing.T) {
	now := time.Now()
	c1 := candidate(1, 10, 100)
	c2 := candidate(2, 10, 100)
	inflight := func(channelID int64) int {
		if channelID == 1 {
			return 10 // saturated (limit)
		}
		return 0
	}
	// 40 selections with uniform draws: the saturated channel (score 1 vs 100)
	// must win at most a couple of times; the idle channel takes ~99%.
	draws := make([]float64, 40)
	for i := range draws {
		draws[i] = float64(i%40+1) / 41.0
	}
	selector := NewWithDependencies(fakeRepo{route: &domain.Route{ID: 1}, candidates: []domain.RoutingCandidate{c1, c2}}, fakeClock{now}, &seqRandom{draws: draws})
	selector.SetConcurrencyAware(true, 10, inflight)
	busyWins := 0
	for i := 0; i < 40; i++ {
		decision, err := selector.Select(context.Background(), "gpt-test", nil)
		if err != nil {
			t.Fatal(err)
		}
		if decision.Selected.Channel.ID == 1 {
			busyWins++
		}
	}
	if busyWins > 4 {
		t.Fatalf("saturated channel won %d/40 picks, want ≈0 (burst guard not applied)", busyWins)
	}

	// After the channel drains (inflight 0), it recovers its full share: with
	// uniform draws both channels are selected roughly equally.
	idle := func(int64) int { return 0 }
	selector.SetConcurrencyAware(true, 10, idle)
	fresh := make([]float64, 40)
	for i := range fresh {
		fresh[i] = float64(i%40+1) / 41.0
	}
	selector.random = &seqRandom{draws: fresh}
	idleWins := 0
	for i := 0; i < 40; i++ {
		decision, err := selector.Select(context.Background(), "gpt-test", nil)
		if err != nil {
			t.Fatal(err)
		}
		if decision.Selected.Channel.ID == 1 {
			idleWins++
		}
	}
	if idleWins < 10 {
		t.Fatalf("recovered channel won only %d/40 picks, want ≈20 (share not restored)", idleWins)
	}
}

func TestConcurrencyGuardDisabledIsNoop(t *testing.T) {
	now := time.Now()
	c1 := candidate(1, 10, 100)
	c2 := candidate(2, 10, 100)
	draws := make([]float64, 40)
	for i := range draws {
		draws[i] = float64(i%40+1) / 41.0
	}
	selector := NewWithDependencies(fakeRepo{route: &domain.Route{ID: 1}, candidates: []domain.RoutingCandidate{c1, c2}}, fakeClock{now}, &seqRandom{draws: draws})
	// Guard off: scoring ignores occupancy entirely.
	selector.SetConcurrencyAware(false, 0, nil)
	firstWins := 0
	for i := 0; i < 40; i++ {
		decision, err := selector.Select(context.Background(), "gpt-test", nil)
		if err != nil {
			t.Fatal(err)
		}
		if decision.Selected.Channel.ID == 1 {
			firstWins++
		}
	}
	if firstWins < 10 {
		t.Fatalf("guard disabled still penalized channel 1: %d/40", firstWins)
	}
}

func TestSingleModePinsMemberAndZeroesRetry(t *testing.T) {
	pin := int64(2)
	route := &domain.Route{
		ID:             1,
		RoutingMode:    domain.RoutingModeSingle,
		SingleMemberID: &pin,
		RetryTimes:     intPtr(3),
	}
	candidates := []domain.RoutingCandidate{
		candidate(1, 20, 100),
		candidate(2, 10, 100),
		candidate(3, 10, 100),
	}
	selector := NewWithDependencies(fakeRepo{route: route, candidates: candidates}, fakeClock{time.Now()}, &fakeRandom{values: []int{0}})

	decision, err := selector.Select(context.Background(), "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	// The pin outranks priority tiers: member 2 wins even though member 1
	// sits in a higher tier.
	if decision.Selected.Channel.ID != 2 {
		t.Fatalf("got channel %d, want pinned channel 2", decision.Selected.Channel.ID)
	}
	if decision.RetryTimesOverride == nil || *decision.RetryTimesOverride != 0 {
		t.Fatalf("single mode must force retry override 0, got %v", decision.RetryTimesOverride)
	}
	for _, evaluation := range decision.Candidates {
		if evaluation.Candidate.Member.ID == 2 {
			continue
		}
		if evaluation.Eligible {
			t.Fatalf("member %d should be ineligible under single mode", evaluation.Candidate.Member.ID)
		}
		found := false
		for _, reason := range evaluation.Reasons {
			if reason == ReasonSingleMode {
				found = true
			}
		}
		if !found {
			t.Fatalf("member %d missing single_mode reason: %v", evaluation.Candidate.Member.ID, evaluation.Reasons)
		}
	}
}

func TestSingleModeMissingPinFallsBackToAuto(t *testing.T) {
	pin := int64(99)
	route := &domain.Route{
		ID:             1,
		RoutingMode:    domain.RoutingModeSingle,
		SingleMemberID: &pin,
		RetryTimes:     intPtr(2),
	}
	candidates := []domain.RoutingCandidate{
		candidate(1, 20, 100),
		candidate(2, 10, 100),
	}
	selector := NewWithDependencies(fakeRepo{route: route, candidates: candidates}, fakeClock{time.Now()}, &fakeRandom{values: []int{0}})

	decision, err := selector.Select(context.Background(), "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	// Dangling pin behaves as auto: the top tier picks normally and the
	// stored retry override stays untouched.
	if decision.Selected.Channel.ID != 1 {
		t.Fatalf("got channel %d, want tier winner 1", decision.Selected.Channel.ID)
	}
	if decision.RetryTimesOverride == nil || *decision.RetryTimesOverride != 2 {
		t.Fatalf("dangling pin must keep stored retry override, got %v", decision.RetryTimesOverride)
	}
}

func intPtr(v int) *int { return &v }

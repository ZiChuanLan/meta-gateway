package routing

import (
	"context"
	"errors"
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
	latency := func(channelID int64) (float64, bool) {
		switch channelID {
		case 1:
			return 200, true
		case 2:
			return 5000, true
		}
		return 0, false
	}
	selector := NewWithDependencies(fakeRepo{}, systemClock{}, &fakeRandom{values: []int{0, 0, 0, 0, 0, 0, 0, 0, 0, 0}})
	selector.SetLatencyAware(latency)

	fastCount, slowCount := 0, 0
	for i := 0; i < 10; i++ {
		picked := selector.pick([]domain.RoutingCandidate{fast, slow})
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
	selector.SetLatencyAware(func(int64) (float64, bool) { return 0, false })
	picked := selector.pick([]domain.RoutingCandidate{a, b})
	if picked.Channel.ID != 1 {
		t.Fatalf("cold start picked %d, want 1 (random=0)", picked.Channel.ID)
	}
}

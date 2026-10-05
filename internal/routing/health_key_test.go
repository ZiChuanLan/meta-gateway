package routing

import (
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
)

// Adaptive health is keyed by the name a member actually sends upstream, not by
// the route's pattern.
//
// The case this pins: one route (an alias such as "deepseek-v4.1-flash") whose
// members reach differently named models — "cn:…" and "global:…" — through the
// same channel. While both shared the route-pattern key, one latency sample
// covered both, so whichever variant served the traffic both scored
// identically: a fast cn variant kept the slow global one looking healthy, and
// the operator saw "这两个的权重始终一样" no matter what they used.
func TestAdaptiveHealthIsKeyedPerUpstreamModel(t *testing.T) {
	const pattern = "deepseek-v4.1-flash"
	member := func(id int64, real string) domain.RoutingCandidate {
		return domain.RoutingCandidate{
			Member: domain.RouteMember{
				ID: id, RouteID: 1, ChannelID: 7, Priority: 10, Weight: 100,
				Enabled: true, MappingJSON: `{"real":"` + real + `"}`,
			},
			Channel:          domain.Channel{ID: 7, Status: domain.StatusEnabled},
			CredentialUsable: true,
			ModelPattern:     pattern,
		}
	}
	repo := fakeRepo{route: &domain.Route{ID: 1, ModelPattern: pattern}}
	selector := NewWithDependencies(repo, fakeClock{time.Now()}, &fakeRandom{values: []int{0}})
	// Only the cn variant is slow.
	selector.SetLatencyAware(true, func(_ int64, model string) (float64, bool) {
		if model == "cn:deepseek-v4.1-flash" {
			return 12000, true
		}
		return 0, false
	})

	slow := selector.scoreFor(member(1, "cn:deepseek-v4.1-flash"), "")
	fast := selector.scoreFor(member(2, "global:deepseek-v4.1-flash"), "")

	// The slow variant keeps roughly a fifth of its weight (3000/(3000+12000));
	// the fast one, with no sample of its own, keeps all of it.
	if slow >= fast {
		t.Fatalf("slow variant scored %v, fast variant %v — they must not share a sample", slow, fast)
	}
	if fast != 100 {
		t.Fatalf("a variant with no latency sample of its own scored %v, want the full weight 100", fast)
	}
}

// The same resolution must apply to the error record, or a failing variant
// would halve the share of its healthy sibling.
func TestErrorPropensityIsKeyedPerUpstreamModel(t *testing.T) {
	const pattern = "deepseek-v4.1-flash"
	member := func(id int64, real string) domain.RoutingCandidate {
		return domain.RoutingCandidate{
			Member: domain.RouteMember{
				ID: id, RouteID: 1, ChannelID: 7, Priority: 10, Weight: 100,
				Enabled: true, MappingJSON: `{"real":"` + real + `"}`,
			},
			Channel:          domain.Channel{ID: 7, Status: domain.StatusEnabled},
			CredentialUsable: true,
			ModelPattern:     pattern,
		}
	}
	repo := fakeRepo{route: &domain.Route{ID: 1, ModelPattern: pattern}}
	selector := NewWithDependencies(repo, fakeClock{time.Now()}, &fakeRandom{values: []int{0}})
	selector.SetErrorAware(true, func(_ int64, model string) (float64, bool) {
		if model == "global:deepseek-v4.1-flash" {
			return 0.5, true
		}
		return 0, false
	})

	healthy := selector.scoreFor(member(1, "cn:deepseek-v4.1-flash"), "")
	failing := selector.scoreFor(member(2, "global:deepseek-v4.1-flash"), "")

	if healthy != 100 {
		t.Fatalf("the healthy variant scored %v, want 100 — its sibling's failures must not reach it", healthy)
	}
	if failing != 50 {
		t.Fatalf("the failing variant scored %v, want 50 (half share)", failing)
	}
}

// Two members that genuinely reach the same upstream name DO share a record:
// they are the same model behind one channel, so one latency sample is the
// honest answer, and the operator's "same alias" case behaves as before.
func TestMembersReachingTheSameUpstreamShareARecord(t *testing.T) {
	const pattern = "deepseek-v4.1-flash"
	shared := func(id int64) domain.RoutingCandidate {
		return domain.RoutingCandidate{
			Member: domain.RouteMember{
				ID: id, RouteID: 1, ChannelID: 7, Priority: 10, Weight: 100,
				Enabled: true, MappingJSON: `{"real":"upstream-x"}`,
			},
			Channel:          domain.Channel{ID: 7, Status: domain.StatusEnabled},
			CredentialUsable: true,
			ModelPattern:     pattern,
		}
	}
	repo := fakeRepo{route: &domain.Route{ID: 1, ModelPattern: pattern}}
	selector := NewWithDependencies(repo, fakeClock{time.Now()}, &fakeRandom{values: []int{0}})
	selector.SetLatencyAware(true, func(_ int64, model string) (float64, bool) {
		if model == "upstream-x" {
			return 3000, true
		}
		return 0, false
	})

	if a, b := selector.scoreFor(shared(1), ""), selector.scoreFor(shared(2), ""); a != b {
		t.Fatalf("members reaching one upstream scored %v and %v, want equal", a, b)
	}
}

// A member with no alias is keyed by the route's pattern, so a plain route
// keeps behaving exactly as it did before.
func TestPlainMemberIsKeyedByRoutePattern(t *testing.T) {
	plain := domain.RoutingCandidate{
		Member:           domain.RouteMember{ID: 1, RouteID: 1, ChannelID: 7, Priority: 10, Weight: 100, Enabled: true},
		Channel:          domain.Channel{ID: 7, Status: domain.StatusEnabled},
		CredentialUsable: true,
		ModelPattern:     "gpt-4o",
	}
	if got := domain.UpstreamModelName(plain); got != "gpt-4o" {
		t.Fatalf("plain member keyed by %q, want the route pattern", got)
	}
}

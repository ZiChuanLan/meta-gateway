package routing

import (
	"context"
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
)

func TestErrorOnlyScoringUsesEachUpstreamModel(t *testing.T) {
	first := candidate(1, 0, 100)
	second := candidate(2, 0, 100)
	first.ModelPattern, second.ModelPattern = "public", "public"
	first.Member.MappingJSON = `{"real":"upstream-a"}`
	second.Member.MappingJSON = `{"real":"upstream-b"}`
	var seen []string
	s := NewWithDependencies(fakeRepo{}, systemClock{}, &fakeRandom{values: []int{0}})
	s.SetErrorAware(true, func(_ int64, model string) (float64, bool) { seen = append(seen, model); return 0.8, true })
	s.pick([]domain.RoutingCandidate{first, second}, domain.RoutingModeAuto)
	if len(seen) != 2 || seen[0] != "upstream-a" || seen[1] != "upstream-b" {
		t.Fatalf("health keys: %v", seen)
	}
}

func TestScoringResolvesLegacyAliasesAndConcreteWildcardRequests(t *testing.T) {
	for _, test := range []struct{ requested, pattern, mapping, want string }{
		{"public", "public", `{"real":"upstream"}`, "upstream"},
		{"gpt-test", "gpt-*", "", "gpt-test"},
	} {
		c := candidate(1, 0, 100)
		c.ModelPattern = test.pattern
		s := NewWithDependencies(fakeRepo{route: &domain.Route{ID: 1, ModelPattern: test.pattern, MappingJSON: test.mapping}, candidates: []domain.RoutingCandidate{c}}, systemClock{}, &fakeRandom{values: []int{0}})
		var seen []string
		s.SetErrorAware(true, func(_ int64, model string) (float64, bool) { seen = append(seen, model); return 0.5, true })
		decision, err := s.Select(context.Background(), test.requested, nil)
		if err != nil {
			t.Fatal(err)
		}
		if domain.UpstreamModelName(decision.Selected) != test.want {
			t.Fatalf("selected name: %+v", decision.Selected)
		}
		for _, model := range seen {
			if model != test.want {
				t.Fatalf("wrong scoring key: %v", seen)
			}
		}
	}
}

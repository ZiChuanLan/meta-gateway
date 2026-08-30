package routing

import (
	"context"
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
)

// variant builds a candidate whose channel id differs from the member id, so
// one channel can carry several alias variants ([A]/[B]/[次] of one logical
// model) as separate route_members rows.
func variant(memberID, channelID int64, priority, weight int) domain.RoutingCandidate {
	return domain.RoutingCandidate{
		Member: domain.RouteMember{
			ID: memberID, RouteID: 1, ChannelID: channelID,
			Priority: priority, Weight: weight, Enabled: true,
		},
		Channel:          domain.Channel{ID: channelID, Status: domain.StatusEnabled},
		CredentialUsable: true,
	}
}

func TestExcludedMembersSkipFailedVariant(t *testing.T) {
	repo := fakeRepo{route: &domain.Route{ID: 1}, candidates: []domain.RoutingCandidate{
		variant(1, 10, 5, 100), variant(2, 20, 5, 100),
	}}
	selector := NewWithDependencies(repo, fakeClock{}, &fakeRandom{values: []int{0}})
	constraint := &SelectionConstraint{ExcludedMembers: map[int64]struct{}{1: {}}}
	decision, err := selector.SelectSticky(context.Background(), "model", nil, "", constraint)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Selected.Member.ID != 2 {
		t.Fatalf("expected member 2 after excluding member 1, got %d", decision.Selected.Member.ID)
	}
}

func TestMemberExclusionMarksReasonInExplanation(t *testing.T) {
	repo := fakeRepo{route: &domain.Route{ID: 1}, candidates: []domain.RoutingCandidate{
		variant(1, 10, 5, 100),
	}}
	selector := NewWithDependencies(repo, fakeClock{}, &fakeRandom{values: []int{0}})
	explanation, err := selector.evaluate(context.Background(), "model", nil, &SelectionConstraint{ExcludedMembers: map[int64]struct{}{1: {}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(explanation.Candidates) != 1 || explanation.Candidates[0].Eligible {
		t.Fatalf("expected the excluded member to be ineligible: %+v", explanation.Candidates)
	}
	found := false
	for _, reason := range explanation.Candidates[0].Reasons {
		if reason == ReasonExcluded {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected ReasonExcluded, got %v", explanation.Candidates[0].Reasons)
	}
}

func TestPreferChannelWalksVariantsBeforeOtherChannels(t *testing.T) {
	// Channel 10 hosts two variants of the alias; channel 20 serves one.
	candidates := []domain.RoutingCandidate{
		variant(11, 10, 5, 100), variant(12, 10, 5, 100), variant(21, 20, 5, 100),
	}
	repo := fakeRepo{route: &domain.Route{ID: 1}, candidates: candidates}
	selector := NewWithDependencies(repo, fakeClock{}, &fakeRandom{values: []int{0, 0}})

	// Failed [11] on channel 10: the retry must pick sibling [12], not channel 20.
	constraint := &SelectionConstraint{ExcludedMembers: map[int64]struct{}{11: {}}, PreferChannel: 10}
	decision, err := selector.SelectSticky(context.Background(), "model", nil, "", constraint)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Selected.Member.ID != 12 {
		t.Fatalf("expected intra-channel variant 12, got member %d", decision.Selected.Member.ID)
	}
	if !decision.PreferredApplied || decision.PreferredChannelID == nil || *decision.PreferredChannelID != 10 {
		t.Fatalf("expected preferred-channel metadata, got applied=%v id=%v", decision.PreferredApplied, decision.PreferredChannelID)
	}

	// Channel 10 fully exhausted: selection falls back to channel 20.
	constraint.ExcludedMembers[12] = struct{}{}
	decision, err = selector.SelectSticky(context.Background(), "model", nil, "", constraint)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Selected.Channel.ID != 20 {
		t.Fatalf("expected cross-channel fallback to channel 20, got channel %d", decision.Selected.Channel.ID)
	}
	if decision.PreferredApplied {
		t.Fatal("expected the preference to lapse once the channel is exhausted")
	}
}

func TestPreferChannelUsesChannelTopTier(t *testing.T) {
	// Variant 13 sits in a lower priority tier than 11 on channel 10; the walk
	// stays in the channel's top tier (10) even though 13 is eligible.
	candidates := []domain.RoutingCandidate{
		variant(11, 10, 10, 100), variant(13, 10, 5, 100), variant(21, 20, 10, 100),
	}
	repo := fakeRepo{route: &domain.Route{ID: 1}, candidates: candidates}
	selector := NewWithDependencies(repo, fakeClock{}, &fakeRandom{values: []int{0, 0}})
	constraint := &SelectionConstraint{PreferChannel: 10}
	decision, err := selector.SelectSticky(context.Background(), "model", nil, "", constraint)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Selected.Member.ID != 11 || *decision.SelectedPriority != 10 {
		t.Fatalf("expected top-tier member 11 of channel 10, got member %d priority %v", decision.Selected.Member.ID, decision.SelectedPriority)
	}
}

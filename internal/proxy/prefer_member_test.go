package proxy

import (
	"errors"
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/routing"
)

// candidate builds an eligible evaluation for one member on a channel.
func candidate(memberID, channelID int64, eligible bool) routing.Evaluation {
	return routing.Evaluation{
		Eligible: eligible,
		Candidate: domain.RoutingCandidate{
			Member:  domain.RouteMember{ID: memberID, ChannelID: channelID},
			Channel: domain.Channel{ID: channelID, Name: "ch"},
		},
	}
}

// A unified alias puts several members on ONE channel (one per upstream
// 原模型 name), so a channel pin can only ever reach the first of them. The
// member pin is what makes a listed row addressable — and it must not quietly
// fall back to a sibling member when the pinned one is ineligible, because a
// probe that reports on a different upstream than the one that was asked for
// is worse than a probe that reports nothing.
func TestPickPreferredPrefersTheMemberOverTheChannel(t *testing.T) {
	decision := routing.Decision{Explanation: routing.Explanation{
		Candidates: []routing.Evaluation{
			candidate(11, 7, true),
			candidate(12, 7, true),
			candidate(13, 8, true),
		},
	}}

	picked, ok := pickPreferred(decision, 0, 12)
	if !ok || picked.Member.ID != 12 {
		t.Fatalf("member pin = %+v ok=%v, want member 12", picked.Member, ok)
	}

	// A channel pin still resolves to the first eligible member on it.
	picked, ok = pickPreferred(decision, 7, 0)
	if !ok || picked.Member.ID != 11 {
		t.Fatalf("channel pin = %+v ok=%v, want the first member of channel 7", picked.Member, ok)
	}

	// The member pin wins when both are supplied.
	picked, ok = pickPreferred(decision, 8, 12)
	if !ok || picked.Member.ID != 12 {
		t.Fatalf("combined pin = %+v ok=%v, want member 12", picked.Member, ok)
	}
}

func TestPickPreferredRefusesIneligiblePins(t *testing.T) {
	decision := routing.Decision{Explanation: routing.Explanation{
		Candidates: []routing.Evaluation{
			candidate(11, 7, false),
			candidate(12, 7, true),
		},
	}}

	// The pinned member is cooling down: refuse rather than silently test 12,
	// even though 12 shares its channel and is eligible.
	if picked, ok := pickPreferred(decision, 0, 11); ok {
		t.Fatalf("ineligible member pin resolved to %+v", picked.Member)
	}
	// Nothing on the pinned channel is eligible either.
	if picked, ok := pickPreferred(decision, 9, 0); ok {
		t.Fatalf("unknown channel pin resolved to %+v", picked.Member)
	}
}

// Every pin kind must be registered here: the guards that stop cross-channel
// failover and sticky binding read this one predicate, so a new pin field that
// is forgotten would let a diagnostic probe leave affinity behind.
func TestPinnedUpstreamCoversEveryPinKind(t *testing.T) {
	if pinnedUpstream(Request{}) {
		t.Error("an unpinned request is reported as pinned")
	}
	if !pinnedUpstream(Request{PreferChannelID: 7}) {
		t.Error("a channel pin is not reported as pinned")
	}
	if !pinnedUpstream(Request{PreferMemberID: 9}) {
		t.Error("a member pin is not reported as pinned")
	}
}

// An unusable pinned upstream has to say WHICH way it is unusable: a member
// switched off, a parked channel, an empty key pool, a cooling row and a row
// that is not part of the route at all are four different fixes.
func TestPinFailureNamesTheReason(t *testing.T) {
	decision := routing.Decision{Explanation: routing.Explanation{
		Candidates: []routing.Evaluation{
			{
				Candidate: domain.RoutingCandidate{
					Member:  domain.RouteMember{ID: 11, ChannelID: 7},
					Channel: domain.Channel{ID: 7, Name: "ch"},
				},
				Reasons: []routing.Reason{routing.ReasonMemberDisabled},
			},
			{
				Candidate: domain.RoutingCandidate{
					Member:  domain.RouteMember{ID: 13, ChannelID: 8},
					Channel: domain.Channel{ID: 8, Name: "ch"},
				},
				Reasons: []routing.Reason{routing.ReasonCoolingDown},
			},
		},
	}}
	cases := []struct {
		name string
		req  Request
		want string
	}{
		{"member switched off", Request{PreferMemberID: 11}, "pinned_upstream_member_disabled"},
		{"cooling down", Request{PreferMemberID: 13}, "pinned_upstream_cooling_down"},
		{"not part of the route", Request{PreferMemberID: 99}, "pinned_upstream_not_member"},
		{"channel not part of the route", Request{PreferChannelID: 42}, "pinned_upstream_not_member"},
	}
	for _, tc := range cases {
		err := pinFailure(decision, tc.req)
		var pin *PinFailure
		if !errors.As(err, &pin) || pin.Code != tc.want {
			t.Fatalf("%s: got %v, want %s", tc.name, err, tc.want)
		}
		// The console and the probes both key off this sentinel.
		if !errors.Is(err, ErrPreferredChannel) {
			t.Fatalf("%s: pin failure no longer wraps ErrPreferredChannel", tc.name)
		}
	}
}

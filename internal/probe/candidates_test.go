package probe

import (
	"context"
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

// memberByChannel fetches the route member bound to a channel.
func memberByChannel(t *testing.T, db *store.DB, channelID int64) *domain.RouteMember {
	t.Helper()
	overviews, err := db.RouteMember.ListRouteOverviews()
	if err != nil {
		t.Fatalf("list overviews: %v", err)
	}
	for _, overview := range overviews {
		for _, candidate := range overview.Members {
			if candidate.Member.ChannelID == channelID {
				member := candidate.Member
				return &member
			}
		}
	}
	t.Fatalf("member for channel %d not found", channelID)
	return nil
}

// The dead-end regression: once a probe disables a member, the pair must stay
// in CandidatePairs' scope. The probe is the only mechanism that re-enables its
// own disables, so excluding enabled=0 pairs here would make every
// auto-disable permanent.
func TestCandidatePairsKeepsProbeDisabledPairsReachable(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	chID := newProbeFixture(t, db, "m1")

	// Probe the member down (threshold 1).
	service := NewService(db, &fakeRelay{ok: map[int64]bool{}}, nil)
	task, err := service.Start(context.Background(), []Pair{{ChannelID: chID, Model: "m1"}}, Options{AutoDisableAfter: 1})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForTask(t, db, task.ID)
	if memberEnabled(t, db, chID, "m1") {
		t.Fatal("precondition: member should be disabled by the failing probe")
	}

	// The scheduled run builds its scope from CandidatePairs with no filters.
	pairs, err := CandidatePairs(db, nil, nil)
	if err != nil {
		t.Fatalf("candidate pairs: %v", err)
	}
	if len(pairs) != 1 || pairs[0] != (Pair{ChannelID: chID, Model: "m1"}) {
		t.Fatalf("pairs = %+v, want the auto-disabled pair to stay probeable", pairs)
	}

	// A green run over those pairs restores the member.
	service = NewService(db, &fakeRelay{ok: map[int64]bool{chID: true}}, nil)
	task, err = service.Start(context.Background(), pairs, Options{AutoDisableAfter: 1})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForTask(t, db, task.ID)
	if !memberEnabled(t, db, chID, "m1") {
		t.Error("probe could not recover the member it disabled: auto-disable was a dead end")
	}
}

// A member an operator disabled by hand stays out of the probe's scope: probes
// answer to the operator, they do not second-guess them.
func TestCandidatePairsExcludesManuallyDisabledMembers(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	chID := newProbeFixture(t, db, "m1")

	// Hand-disable through the real UI path: whole-object PUT, then the
	// handler records the manual intent.
	member := memberByChannel(t, db, chID)
	member.Enabled = false
	if err := db.RouteMember.Update(member); err != nil {
		t.Fatalf("manual disable: %v", err)
	}
	if err := db.RouteMember.ApplyManualIntent(member.ID, false); err != nil {
		t.Fatalf("manual intent: %v", err)
	}

	pairs, err := CandidatePairs(db, nil, nil)
	if err != nil {
		t.Fatalf("candidate pairs: %v", err)
	}
	if len(pairs) != 0 {
		t.Fatalf("pairs = %+v, want none: a hand-disabled member is not the probe's business", pairs)
	}
}

package store_test

import (
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

// memberStateFixture builds one enabled channel serving one route, the minimum
// shape the member-state regression tests need.
func memberStateFixture(t *testing.T, db *store.DB, model string) (channelID, memberID int64) {
	t.Helper()
	channelID, err := db.Channel.Create(&domain.Channel{
		Name: "state-target", BaseURL: "https://api.example.com", Weight: 100, Status: domain.StatusEnabled,
		ModelSyncMode: domain.ModelSyncModeAuto,
	})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	routeID, err := db.Route.Create(&domain.Route{ModelPattern: model, Enabled: true})
	if err != nil {
		t.Fatalf("create route: %v", err)
	}
	memberID, err = db.RouteMember.Create(&domain.RouteMember{
		RouteID: routeID, ChannelID: channelID, Weight: 100, Enabled: true,
	})
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
	return channelID, memberID
}

// getMember is GetByID with fatal error handling.
func getMember(t *testing.T, db *store.DB, id int64) *domain.RouteMember {
	t.Helper()
	member, err := db.RouteMember.GetByID(id)
	if err != nil {
		t.Fatalf("get member: %v", err)
	}
	if member == nil {
		t.Fatalf("member %d not found", id)
	}
	return member
}

// A manual disable must wipe the health marks real traffic left behind. The UI
// PUTs the whole member object, so the stale fail_count would otherwise survive
// the toggle — and channel recovery (enabled = CASE WHEN fail_count > 0 THEN 1)
// would resurrect a member the operator turned off on purpose.
func TestApplyManualIntentDisableResetsHealth(t *testing.T) {
	db := openTestDB(t)
	channelID, memberID := memberStateFixture(t, db, "m1")

	// Real traffic cools the member and trips the channel breaker.
	if err := db.RouteMember.RecordFailure(memberID, time.Now().UTC(), time.Minute, "upstream_500"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Channel.RecordRelayFailure(channelID); err != nil {
		t.Fatal(err)
	}
	if err := db.Channel.AutoDisable(channelID); err != nil {
		t.Fatal(err)
	}

	// The operator disables the member: the UI PUT round-trips the stale health
	// fields, then the handler records the manual intent.
	member := getMember(t, db, memberID)
	member.Enabled = false
	if err := db.RouteMember.Update(member); err != nil {
		t.Fatal(err)
	}
	if err := db.RouteMember.ApplyManualIntent(memberID, false); err != nil {
		t.Fatal(err)
	}

	got := getMember(t, db, memberID)
	if got.Enabled {
		t.Error("member should be disabled")
	}
	if got.AutoDisabled {
		t.Error("a manual disable must not carry the probe marker")
	}
	if got.FailCount != 0 {
		t.Errorf("fail_count = %d, want 0: a leftover mark lets channel recovery resurrect the member", got.FailCount)
	}
	if got.CooldownUntil != nil {
		t.Errorf("cooldown_until = %v, want nil", got.CooldownUntil)
	}
	if got.LastError != "" {
		t.Errorf("last_error = %q, want empty", got.LastError)
	}

	// Channel recovery must leave the hand-disabled member off.
	recovered, err := db.Channel.RecoverAutoDisabled(channelID)
	if err != nil {
		t.Fatal(err)
	}
	if !recovered {
		t.Fatal("precondition: channel should have been auto-disabled")
	}
	if got = getMember(t, db, memberID); got.Enabled {
		t.Error("channel recovery resurrected a manually disabled member")
	}
}

// Enabling a member by hand supersedes the probe's negative verdict: the
// auto_disabled marker must go, or AutoRecoverByPair would later "recover" a
// member that is already on and CountAutoDisabled would overcount.
func TestApplyManualIntentEnableClearsAutoDisabled(t *testing.T) {
	db := openTestDB(t)
	channelID, memberID := memberStateFixture(t, db, "m1")

	if _, err := db.RouteMember.AutoDisableByPair(channelID, "m1", "probe failed 5 times in a row"); err != nil {
		t.Fatal(err)
	}
	if member := getMember(t, db, memberID); member.Enabled || !member.AutoDisabled {
		t.Fatalf("precondition: member should be probe-disabled, got %+v", member)
	}

	member := getMember(t, db, memberID)
	member.Enabled = true
	if err := db.RouteMember.Update(member); err != nil {
		t.Fatal(err)
	}
	if err := db.RouteMember.ApplyManualIntent(memberID, true); err != nil {
		t.Fatal(err)
	}

	got := getMember(t, db, memberID)
	if !got.Enabled {
		t.Error("member should be enabled")
	}
	if got.AutoDisabled {
		t.Error("a manual enable must clear the probe marker")
	}
}

// A discovery snapshot refresh only creates and removes members. Flipping
// enabled is the operator's business (or the probe's): a member disabled by
// hand or by probing must survive the reconcile untouched.
func TestReconcileDoesNotResurrectDisabledMembers(t *testing.T) {
	db := openTestDB(t)
	channelA, err := db.Channel.Create(&domain.Channel{Name: "reconcile-a", Status: domain.StatusEnabled, ModelSyncMode: domain.ModelSyncModeAuto})
	if err != nil {
		t.Fatal(err)
	}
	channelB, err := db.Channel.Create(&domain.Channel{Name: "reconcile-b", Status: domain.StatusEnabled, ModelSyncMode: domain.ModelSyncModeAuto})
	if err != nil {
		t.Fatal(err)
	}
	inputA := store.ReconcileInput{ChannelID: channelA, Models: []string{"m1"}, Source: "new-api", CheckedAt: time.Now()}
	inputB := store.ReconcileInput{ChannelID: channelB, Models: []string{"m1"}, Source: "new-api", CheckedAt: time.Now()}
	if _, err := db.DiscoveredModel.Reconcile(t.Context(), inputA); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DiscoveredModel.Reconcile(t.Context(), inputB); err != nil {
		t.Fatal(err)
	}

	route, err := db.Route.GetByModel("m1")
	if err != nil {
		t.Fatal(err)
	}
	members, err := db.RouteMember.ListByRoute(route.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 2 {
		t.Fatalf("members = %d, want 2 (one per channel)", len(members))
	}
	var memberA, memberB domain.RouteMember
	for _, member := range members {
		switch member.ChannelID {
		case channelA:
			memberA = member
		case channelB:
			memberB = member
		}
	}

	// Member A: disabled by hand through the UI path (whole-object PUT, then
	// the handler records the intent).
	memberA.Enabled = false
	if err := db.RouteMember.Update(&memberA); err != nil {
		t.Fatal(err)
	}
	if err := db.RouteMember.ApplyManualIntent(memberA.ID, false); err != nil {
		t.Fatal(err)
	}
	// Member B: disabled by probing.
	if _, err := db.RouteMember.AutoDisableByPair(channelB, "m1", "probe failed 5 times in a row"); err != nil {
		t.Fatal(err)
	}

	if _, err := db.DiscoveredModel.Reconcile(t.Context(), inputA); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DiscoveredModel.Reconcile(t.Context(), inputB); err != nil {
		t.Fatal(err)
	}

	if got := getMember(t, db, memberA.ID); got.Enabled {
		t.Error("discovery reconcile resurrected a member the operator disabled")
	}
	if got := getMember(t, db, memberB.ID); got.Enabled {
		t.Error("discovery reconcile resurrected a member the probe disabled")
	}
}

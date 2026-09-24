package store_test

import (
	"errors"
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

// newUnifyChannel creates the minimal channel a route member needs.
func newUnifyChannel(t *testing.T, db *store.DB, name string) int64 {
	t.Helper()
	id, err := db.Channel.Create(&domain.Channel{Name: name, Status: domain.StatusEnabled})
	if err != nil {
		t.Fatalf("create channel %s: %v", name, err)
	}
	return id
}

func routeEnabled(t *testing.T, db *store.DB, id int64) bool {
	t.Helper()
	route, err := db.Route.GetByID(id)
	if err != nil {
		t.Fatalf("get route %d: %v", id, err)
	}
	if route == nil {
		t.Fatalf("route %d vanished", id)
	}
	return route.Enabled
}

// routePresent reports whether a route row still exists at all — after a
// unification pass the superseded originals are gone, not parked.
func routePresent(t *testing.T, db *store.DB, id int64) bool {
	t.Helper()
	route, err := db.Route.GetByID(id)
	if err != nil {
		t.Fatalf("get route %d: %v", id, err)
	}
	return route != nil
}

// Deleting — rather than parking — is what leaves one callable name per model:
// a disabled original still shows as a dead row in the model list. The batch
// snapshot is what keeps the removal reversible.
func TestUnifyApplyDeletesSupersededRoutes(t *testing.T) {
	db := openTestDB(t)
	c1 := newUnifyChannel(t, db, "C1")
	c2 := newUnifyChannel(t, db, "C2")

	originalA, err := db.Route.Create(&domain.Route{ModelPattern: "[A]GEMINI", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	originalB, err := db.Route.Create(&domain.Route{ModelPattern: "GEMINI", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.RouteMember.Create(&domain.RouteMember{RouteID: originalA, ChannelID: c1, Enabled: true, Weight: 100}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.RouteMember.Create(&domain.RouteMember{RouteID: originalB, ChannelID: c2, Enabled: true, Weight: 100}); err != nil {
		t.Fatal(err)
	}

	outcome, err := db.ApplyUnify([]store.UnifyApplyGroup{{
		Canonical: "gemini-flash",
		Variants: []store.UnifyApplyVariant{
			{ChannelID: c1, ModelName: "[A]GEMINI"},
			{ChannelID: c2, ModelName: "GEMINI"},
		},
	}}, true)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.RoutesCreated != 1 || outcome.MembersCreated != 2 {
		t.Fatalf("unexpected outcome: %+v", outcome)
	}
	if outcome.RoutesDeleted != 2 {
		t.Fatalf("expected 2 deleted routes, got %d", outcome.RoutesDeleted)
	}
	if routePresent(t, db, originalA) || routePresent(t, db, originalB) {
		t.Fatal("original routes should be gone after apply")
	}
	// The members went with them: nothing may stay attached to a dead route.
	if members, err := db.RouteMember.ListByRoute(originalA); err != nil || len(members) != 0 {
		t.Fatalf("members of a deleted route must be gone: %+v %v", members, err)
	}

	deleted, err := db.UnifyDeletedRoutes()
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 2 {
		t.Fatalf("expected 2 removal entries, got %+v", deleted)
	}
	// The removed rows are gone, so the op itself has to carry the name the
	// history lists — a join back to routes cannot supply it any more.
	names := map[string]bool{}
	for _, entry := range deleted {
		names[entry.ModelName] = true
	}
	if !names["[A]GEMINI"] || !names["GEMINI"] {
		t.Fatalf("removal entries should name the deleted models: %+v", deleted)
	}
}

// Undo walks the recorded operations backwards: members and routes created by
// the batch are deleted, routes it removed are rebuilt from their snapshots —
// with the members and settings they had when they went away.
func TestUnifyUndoRestoresEverything(t *testing.T) {
	db := openTestDB(t)
	c1 := newUnifyChannel(t, db, "C1")
	c2 := newUnifyChannel(t, db, "C2")

	originalA, err := db.Route.Create(&domain.Route{ModelPattern: "[A]GEMINI", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.RouteMember.Create(&domain.RouteMember{RouteID: originalA, ChannelID: c1, Enabled: true, Weight: 100}); err != nil {
		t.Fatal(err)
	}

	outcome, err := db.ApplyUnify([]store.UnifyApplyGroup{{
		Canonical: "gemini-flash",
		Variants: []store.UnifyApplyVariant{
			{ChannelID: c1, ModelName: "[A]GEMINI"},
			{ChannelID: c2, ModelName: "GEMINI"},
		},
	}}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(outcome.Batches) != 1 {
		t.Fatalf("expected 1 batch, got %d", len(outcome.Batches))
	}
	batchID := outcome.Batches[0].ID

	aliasRoute, err := db.Route.GetByModel("gemini-flash")
	if err != nil || aliasRoute == nil {
		t.Fatalf("alias route missing: %v %v", aliasRoute, err)
	}
	members, err := db.RouteMember.ListByRoute(aliasRoute.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 2 {
		t.Fatalf("expected 2 alias members, got %d", len(members))
	}

	if err := db.UndoBatch(batchID); err != nil {
		t.Fatal(err)
	}

	if remaining, err := db.Route.GetByModelAny("gemini-flash"); err != nil || remaining != nil {
		t.Fatalf("alias route should be gone after undo: %v %v", remaining, err)
	}
	restored, err := db.Route.GetByModelAny("[A]GEMINI")
	if err != nil || restored == nil {
		t.Fatalf("undo should rebuild the original route: %v %v", restored, err)
	}
	if !restored.Enabled {
		t.Fatal("rebuilt original should be enabled again")
	}
	restoredMembers, err := db.RouteMember.ListByRoute(restored.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(restoredMembers) != 1 || restoredMembers[0].ChannelID != c1 {
		t.Fatalf("rebuilt original should carry its member again, got %+v", restoredMembers)
	}
	deleted, err := db.UnifyDeletedRoutes()
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 0 {
		t.Fatalf("expected no removed routes after undo, got %+v", deleted)
	}

	batches, err := db.ListUnifyBatches(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(batches) != 1 || batches[0].UndoneAt == nil {
		t.Fatalf("batch should be marked undone: %+v", batches)
	}
}

// Undoing twice must not double-apply or fail.
func TestUnifyUndoIsIdempotent(t *testing.T) {
	db := openTestDB(t)
	c1 := newUnifyChannel(t, db, "C1")
	outcome, err := db.ApplyUnify([]store.UnifyApplyGroup{{
		Canonical: "solo-alias",
		Variants:  []store.UnifyApplyVariant{{ChannelID: c1, ModelName: "[A]Real"}},
	}}, true)
	if err != nil {
		t.Fatal(err)
	}
	batchID := outcome.Batches[0].ID
	if err := db.UndoBatch(batchID); err != nil {
		t.Fatal(err)
	}
	if err := db.UndoBatch(batchID); err != nil {
		t.Fatalf("second undo should be a no-op: %v", err)
	}
}

// A route is only retired when the group provably covers every binding on it.
// Wildcards and partially covered routes stay untouched.
func TestUnifyApplyLeavesRiskyRoutesAlone(t *testing.T) {
	db := openTestDB(t)
	c1 := newUnifyChannel(t, db, "C1")
	c2 := newUnifyChannel(t, db, "C2")

	// Wildcard route: retiring it would affect models nobody asked about.
	wildcard, err := db.Route.Create(&domain.Route{ModelPattern: "gpt-4*", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	// Route with an extra member from a channel the group does not cover.
	partly, err := db.Route.Create(&domain.Route{ModelPattern: "PARTLY", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.RouteMember.Create(&domain.RouteMember{RouteID: partly, ChannelID: c1, Enabled: true, Weight: 100}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.RouteMember.Create(&domain.RouteMember{RouteID: partly, ChannelID: c2, Enabled: true, Weight: 100}); err != nil {
		t.Fatal(err)
	}

	outcome, err := db.ApplyUnify([]store.UnifyApplyGroup{{
		Canonical: "covered-alias",
		Variants: []store.UnifyApplyVariant{
			{ChannelID: c1, ModelName: "gpt-4*"},
			{ChannelID: c1, ModelName: "PARTLY"},
		},
	}}, true)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.RoutesDeleted != 0 {
		t.Fatalf("expected nothing to be removed, got %d", outcome.RoutesDeleted)
	}
	if !routeEnabled(t, db, wildcard) {
		t.Error("wildcard route must stay enabled")
	}
	if !routeEnabled(t, db, partly) {
		t.Error("partially covered route must stay enabled")
	}
}

// Rebuilding one original keeps the alias and the rest of the batch intact,
// and brings the row back with the settings it had — member priorities and
// prices included, not just the bare model name.
func TestUnifyRebuildSingleDeletedRoute(t *testing.T) {
	db := openTestDB(t)
	c1 := newUnifyChannel(t, db, "C1")
	c2 := newUnifyChannel(t, db, "C2")

	originalA, err := db.Route.Create(&domain.Route{ModelPattern: "[A]GEMINI", Enabled: true, ModelGroup: "gemini"})
	if err != nil {
		t.Fatal(err)
	}
	originalB, err := db.Route.Create(&domain.Route{ModelPattern: "GEMINI", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	memberA, err := db.RouteMember.Create(&domain.RouteMember{
		RouteID: originalA, ChannelID: c1, Enabled: true, Weight: 42, Priority: 3,
		PricePromptPer1k: 0.51, PriceCompletionPer1k: 1.25,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Pin the route to that member (routing_mode=single): the pin is a foreign
	// key back into route_members, so a rebuild has to write it after the
	// member rows are back, not with the route row.
	pinnedRoute, err := db.Route.GetByID(originalA)
	if err != nil || pinnedRoute == nil {
		t.Fatalf("reload route: %v %v", pinnedRoute, err)
	}
	pinnedRoute.RoutingMode = "single"
	pinnedRoute.SingleMemberID = &memberA
	if err := db.Route.Update(pinnedRoute); err != nil {
		t.Fatal(err)
	}
	if _, err := db.RouteMember.Create(&domain.RouteMember{RouteID: originalB, ChannelID: c2, Enabled: true, Weight: 100}); err != nil {
		t.Fatal(err)
	}

	if _, err := db.ApplyUnify([]store.UnifyApplyGroup{{
		Canonical: "gemini-flash",
		Variants: []store.UnifyApplyVariant{
			{ChannelID: c1, ModelName: "[A]GEMINI"},
			{ChannelID: c2, ModelName: "GEMINI"},
		},
	}}, true); err != nil {
		t.Fatal(err)
	}

	if err := db.RestoreDeletedRoute(originalA); err != nil {
		t.Fatal(err)
	}
	restored, err := db.Route.GetByModelAny("[A]GEMINI")
	if err != nil || restored == nil {
		t.Fatalf("[A]GEMINI should be back: %v %v", restored, err)
	}
	if restored.ModelGroup != "gemini" {
		t.Fatalf("rebuilt route lost its model group: %+v", restored)
	}
	if restored.SingleMemberID == nil || *restored.SingleMemberID != memberA {
		t.Fatalf("rebuilt route lost its pinned member: %+v", restored.SingleMemberID)
	}
	members, err := db.RouteMember.ListByRoute(restored.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 1 {
		t.Fatalf("rebuilt route should carry its own member, got %+v", members)
	}
	if members[0].ID != memberA {
		t.Fatalf("rebuilt member should keep its original id %d, got %d", memberA, members[0].ID)
	}
	if members[0].Weight != 42 || members[0].Priority != 3 || members[0].PricePromptPer1k != 0.51 || members[0].PriceCompletionPer1k != 1.25 {
		t.Fatalf("rebuilt member lost its settings: %+v", members[0])
	}
	if remaining, err := db.Route.GetByModelAny("GEMINI"); err != nil || remaining != nil {
		t.Fatalf("GEMINI should still be removed: %v %v", remaining, err)
	}
	alias, err := db.Route.GetByModel("gemini-flash")
	if err != nil || alias == nil {
		t.Fatalf("alias must survive a single rebuild: %v %v", alias, err)
	}
	aliasMembers, err := db.RouteMember.ListByRoute(alias.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(aliasMembers) != 2 {
		t.Fatalf("alias should keep both members, got %d", len(aliasMembers))
	}

	// Rebuilding the same name twice is refused instead of minting a duplicate.
	if err := db.RestoreDeletedRoute(originalA); err == nil {
		t.Fatal("expected a second rebuild of the same route to be refused")
	}
}

func TestUnifyRejectsInvalidGroups(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.ApplyUnify([]store.UnifyApplyGroup{{Canonical: "  "}}, false); err == nil {
		t.Fatal("expected empty canonical to be rejected")
	}
	c1 := newUnifyChannel(t, db, "C1")
	if _, err := db.ApplyUnify([]store.UnifyApplyGroup{{
		Canonical: "ok",
		Variants:  []store.UnifyApplyVariant{{ChannelID: c1, ModelName: "   "}},
	}}, false); err == nil {
		t.Fatal("expected empty model name to be rejected")
	}
	if _, err := db.ApplyUnify([]store.UnifyApplyGroup{{
		Canonical: "ok",
		Variants:  []store.UnifyApplyVariant{{ChannelID: 0, ModelName: "real"}},
	}}, false); err == nil {
		t.Fatal("expected zero channel to be rejected")
	}
	// Nothing should have been half-written.
	batches, err := db.ListUnifyBatches(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(batches) != 0 {
		t.Fatalf("expected no batches from rejected applies, got %+v", batches)
	}
}

// Rebuilding a route that was never removed is a clean error, not a panic.
func TestUnifyRestoreUnknownRoute(t *testing.T) {
	db := openTestDB(t)
	if err := db.RestoreDeletedRoute(9999); err == nil {
		t.Fatal("expected an error for an unknown route")
	}
}

// Rebuilding an original must not dead-end the assistant: re-applying the same
// group removes the exposed original again, and the history keeps a trace of
// both the rebuild and the second removal.
func TestUnifyRebuildThenReapplyDeletesAgain(t *testing.T) {
	db := openTestDB(t)
	c1 := newUnifyChannel(t, db, "C1")
	c2 := newUnifyChannel(t, db, "C2")

	originalA, err := db.Route.Create(&domain.Route{ModelPattern: "[A]GEMINI", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	originalB, err := db.Route.Create(&domain.Route{ModelPattern: "GEMINI", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.RouteMember.Create(&domain.RouteMember{RouteID: originalA, ChannelID: c1, Enabled: true, Weight: 100}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.RouteMember.Create(&domain.RouteMember{RouteID: originalB, ChannelID: c2, Enabled: true, Weight: 100}); err != nil {
		t.Fatal(err)
	}

	group := []store.UnifyApplyGroup{{
		Canonical: "gemini-flash",
		Variants: []store.UnifyApplyVariant{
			{ChannelID: c1, ModelName: "[A]GEMINI"},
			{ChannelID: c2, ModelName: "GEMINI"},
		},
	}}
	if _, err := db.ApplyUnify(group, true); err != nil {
		t.Fatal(err)
	}
	if err := db.RestoreDeletedRoute(originalA); err != nil {
		t.Fatal(err)
	}
	if !routePresent(t, db, originalA) {
		t.Fatal("[A]GEMINI should exist again after a rebuild")
	}

	// The history must still show the rebuilt entry next to the removed one.
	deleted, err := db.UnifyDeletedRoutes()
	if err != nil {
		t.Fatal(err)
	}
	rebuiltCount, removedCount := 0, 0
	for _, entry := range deleted {
		if entry.RouteID == originalA && entry.Rebuilt {
			rebuiltCount++
		}
		if entry.RouteID == originalB && !entry.Rebuilt {
			removedCount++
		}
	}
	if rebuiltCount != 1 || removedCount != 1 {
		t.Fatalf("expected 1 rebuilt + 1 removed entry, got %+v", deleted)
	}

	// Re-applying the same group must remove the rebuilt original again.
	second, err := db.ApplyUnify(group, true)
	if err != nil {
		t.Fatal(err)
	}
	if second.RoutesDeleted != 1 || second.MembersCreated != 0 || second.MembersSkipped != 2 {
		t.Fatalf("unexpected re-apply outcome: %+v", second)
	}
	if routePresent(t, db, originalA) {
		t.Fatal("[A]GEMINI should be gone again after re-apply")
	}
}

// Undoing a batch whose alias route gained a member nobody recorded must be
// refused, not silently delete the operator's manual work.
func TestUnifyUndoRefusesManualMembers(t *testing.T) {
	db := openTestDB(t)
	c1 := newUnifyChannel(t, db, "C1")
	outcome, err := db.ApplyUnify([]store.UnifyApplyGroup{{
		Canonical: "solo-alias",
		Variants:  []store.UnifyApplyVariant{{ChannelID: c1, ModelName: "[A]Real"}},
	}}, false)
	if err != nil {
		t.Fatal(err)
	}
	batchID := outcome.Batches[0].ID

	alias, err := db.Route.GetByModel("solo-alias")
	if err != nil || alias == nil {
		t.Fatalf("alias missing: %v %v", alias, err)
	}
	if _, err := db.RouteMember.Create(&domain.RouteMember{
		RouteID: alias.ID, ChannelID: c1, Enabled: true, ManualOverride: true,
	}); err != nil {
		t.Fatal(err)
	}

	err = db.UndoBatch(batchID)
	var validation *store.UnifyValidationError
	if err == nil || !errors.As(err, &validation) {
		t.Fatalf("expected a validation error for the manual member, got %v", err)
	}
	if !routeEnabled(t, db, alias.ID) {
		t.Fatal("alias route must survive a refused undo")
	}
	members, err := db.RouteMember.ListByRoute(alias.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 2 {
		t.Fatalf("manual member must survive a refused undo, got %d", len(members))
	}

	// Removing the manual member unblocks the undo.
	if err := db.RouteMember.Delete(members[1].ID); err != nil {
		t.Fatal(err)
	}
	if err := db.UndoBatch(batchID); err != nil {
		t.Fatalf("undo should succeed once the manual member is gone: %v", err)
	}
}

// Two batches sharing one alias route: undoing the older one must not
// cascade-delete the newer batch's members and leave it marked active.
func TestUnifyUndoRefusesSharedRoute(t *testing.T) {
	db := openTestDB(t)
	c1 := newUnifyChannel(t, db, "C1")
	c2 := newUnifyChannel(t, db, "C2")
	first, err := db.ApplyUnify([]store.UnifyApplyGroup{{
		Canonical: "shared-alias",
		Variants:  []store.UnifyApplyVariant{{ChannelID: c1, ModelName: "[A]Real"}},
	}}, false)
	if err != nil {
		t.Fatal(err)
	}
	second, err := db.ApplyUnify([]store.UnifyApplyGroup{{
		Canonical: "shared-alias",
		Variants:  []store.UnifyApplyVariant{{ChannelID: c2, ModelName: "[B]Real"}},
	}}, false)
	if err != nil {
		t.Fatal(err)
	}

	err = db.UndoBatch(first.Batches[0].ID)
	var validation *store.UnifyValidationError
	if err == nil || !errors.As(err, &validation) {
		t.Fatalf("expected undo to be refused while another batch shares the route, got %v", err)
	}
	alias, err := db.Route.GetByModel("shared-alias")
	if err != nil || alias == nil {
		t.Fatalf("alias missing: %v %v", alias, err)
	}
	members, err := db.RouteMember.ListByRoute(alias.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 2 {
		t.Fatalf("both batches' members must survive, got %d", len(members))
	}

	// Undoing the newest batch first unblocks the older one.
	if err := db.UndoBatch(second.Batches[0].ID); err != nil {
		t.Fatalf("newest batch should undo cleanly: %v", err)
	}
	if err := db.UndoBatch(first.Batches[0].ID); err != nil {
		t.Fatalf("older batch should undo once unshared: %v", err)
	}
	if remaining, err := db.Route.GetByModelAny("shared-alias"); err != nil || remaining != nil {
		t.Fatalf("alias should be gone after both undos: %v %v", remaining, err)
	}
}

// Applying onto a pre-existing disabled route must switch it on — otherwise
// the unified model simply never serves — and undo must park it again.
func TestUnifyApplyEnablesDisabledRoute(t *testing.T) {
	db := openTestDB(t)
	c1 := newUnifyChannel(t, db, "C1")
	routeID, err := db.Route.Create(&domain.Route{ModelPattern: "alias-name", Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := db.ApplyUnify([]store.UnifyApplyGroup{{
		Canonical: "alias-name",
		Variants:  []store.UnifyApplyVariant{{ChannelID: c1, ModelName: "[A]Real-Name"}},
	}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !routeEnabled(t, db, routeID) {
		t.Fatal("apply must enable the disabled canonical route")
	}
	if err := db.UndoBatch(outcome.Batches[0].ID); err != nil {
		t.Fatal(err)
	}
	if routeEnabled(t, db, routeID) {
		t.Fatal("undo must restore the parked (disabled) state")
	}
}

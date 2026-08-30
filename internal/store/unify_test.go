package store_test

import (
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

// Archiving — rather than deleting — is what makes an alias reversible: the
// original keeps its members and every model-level override, and comes back
// with a single flag flip.
func TestUnifyApplyArchivesSupersededRoutes(t *testing.T) {
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
	if outcome.RoutesArchived != 2 {
		t.Fatalf("expected 2 archived routes, got %d", outcome.RoutesArchived)
	}
	if routeEnabled(t, db, originalA) || routeEnabled(t, db, originalB) {
		t.Fatal("original routes should be disabled after archiving")
	}

	archived, err := db.UnifyArchivedRoutes()
	if err != nil {
		t.Fatal(err)
	}
	if len(archived) != 2 {
		t.Fatalf("expected 2 archived entries, got %+v", archived)
	}
}

// Undo walks the recorded operations backwards: members and routes created by
// the batch are deleted, routes it archived come back enabled.
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
	if !routeEnabled(t, db, originalA) {
		t.Fatal("original route should be re-enabled after undo")
	}
	archived, err := db.UnifyArchivedRoutes()
	if err != nil {
		t.Fatal(err)
	}
	if len(archived) != 0 {
		t.Fatalf("expected no archived routes after undo, got %+v", archived)
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
	if outcome.RoutesArchived != 0 {
		t.Fatalf("expected nothing to be archived, got %d", outcome.RoutesArchived)
	}
	if !routeEnabled(t, db, wildcard) {
		t.Error("wildcard route must stay enabled")
	}
	if !routeEnabled(t, db, partly) {
		t.Error("partially covered route must stay enabled")
	}
}

// Restoring one original keeps the alias and the rest of the batch intact.
func TestUnifyRestoreSingleArchivedRoute(t *testing.T) {
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

	if _, err := db.ApplyUnify([]store.UnifyApplyGroup{{
		Canonical: "gemini-flash",
		Variants: []store.UnifyApplyVariant{
			{ChannelID: c1, ModelName: "[A]GEMINI"},
			{ChannelID: c2, ModelName: "GEMINI"},
		},
	}}, true); err != nil {
		t.Fatal(err)
	}

	if err := db.RestoreArchivedRoute(originalA); err != nil {
		t.Fatal(err)
	}
	if !routeEnabled(t, db, originalA) {
		t.Fatal("[A]GEMINI should be back")
	}
	if routeEnabled(t, db, originalB) {
		t.Fatal("GEMINI should still be archived")
	}
	alias, err := db.Route.GetByModel("gemini-flash")
	if err != nil || alias == nil {
		t.Fatalf("alias must survive a single restore: %v %v", alias, err)
	}
	members, err := db.RouteMember.ListByRoute(alias.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 2 {
		t.Fatalf("alias should keep both members, got %d", len(members))
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

// Restoring a route that was never archived is a clean error, not a panic.
func TestUnifyRestoreUnknownRoute(t *testing.T) {
	db := openTestDB(t)
	if err := db.RestoreArchivedRoute(9999); err == nil {
		t.Fatal("expected an error for an unknown route")
	}
}

package store_test

import (
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

// The aggregate record is console metadata only; the interesting behaviour is
// the unique name, the route link, and that deleting a route does not delete
// the record (the route may outlive or predate the record).
func TestAggregateRecordLifecycle(t *testing.T) {
	db := openTestDB(t)

	routeID, _, err := db.CreateRouteWithAutoMatch(&domain.Route{ModelPattern: "agg-model", Enabled: true, RoutingMode: domain.RoutingModeAuto}, nil, store.ModelMatchExact)
	if err != nil {
		t.Fatal(err)
	}

	id, err := db.CreateAggregate("agg-model", routeID, "for hermes")
	if err != nil {
		t.Fatal(err)
	}
	agg, err := db.AggregateByID(id)
	if err != nil || agg == nil {
		t.Fatalf("get: %v %v", agg, err)
	}
	if agg.Name != "agg-model" || agg.RouteID != routeID || agg.Note != "for hermes" {
		t.Fatalf("aggregate = %+v", agg)
	}

	// A second aggregate with the same public name is refused by the unique
	// index — the console surfaces it as a conflict, not a silent second row.
	if _, err := db.CreateAggregate("agg-model", 0, ""); err == nil {
		t.Fatal("duplicate aggregate name accepted")
	}

	byRoute, err := db.AggregateByRoute(routeID)
	if err != nil || byRoute == nil || byRoute.ID != id {
		t.Fatalf("by route: %v %v", byRoute, err)
	}

	if err := db.UpdateAggregate(id, "agg-model-2", "renamed"); err != nil {
		t.Fatal(err)
	}
	renamed, _ := db.AggregateByID(id)
	if renamed.Name != "agg-model-2" || renamed.Note != "renamed" {
		t.Fatalf("renamed = %+v", renamed)
	}

	if err := db.SetAggregateRoute(id, 0); err != nil {
		t.Fatal(err)
	}
	cleared, _ := db.AggregateByID(id)
	if cleared.RouteID != 0 {
		t.Fatalf("route link not cleared: %+v", cleared)
	}

	if err := db.DeleteAggregate(id); err != nil {
		t.Fatal(err)
	}
	gone, err := db.AggregateByID(id)
	if err != nil || gone != nil {
		t.Fatalf("deleted: %v %v", gone, err)
	}
}

// Deleting the route must not delete the aggregate record: the record is what
// tells the console "this name WAS an aggregate", so a route deleted through
// the plain route editor leaves a broken-but-listed aggregate.
func TestAggregateSurvivesRouteDelete(t *testing.T) {
	db := openTestDB(t)

	routeID, _, err := db.CreateRouteWithAutoMatch(&domain.Route{ModelPattern: "agg-survivor", Enabled: true, RoutingMode: domain.RoutingModeAuto}, nil, store.ModelMatchExact)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateAggregate("agg-survivor", routeID, ""); err != nil {
		t.Fatal(err)
	}
	if err := db.Route.Delete(routeID); err != nil {
		t.Fatal(err)
	}
	list, err := db.ListAggregates()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("aggregates = %d, want 1", len(list))
	}
}

// Console pins: add is idempotent, remove is idempotent, list is per kind.
func TestConsolePins(t *testing.T) {
	db := openTestDB(t)

	if err := db.AddPin("channel", "3"); err != nil {
		t.Fatal(err)
	}
	if err := db.AddPin("channel", "3"); err != nil {
		t.Fatal(err)
	}
	if err := db.AddPin("channel", "7"); err != nil {
		t.Fatal(err)
	}
	if err := db.AddPin("model", "hermes-model"); err != nil {
		t.Fatal(err)
	}

	refs, err := db.Pins("channel")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 || refs[0] != "3" || refs[1] != "7" {
		t.Fatalf("channel pins = %v", refs)
	}
	modelRefs, _ := db.Pins("model")
	if len(modelRefs) != 1 || modelRefs[0] != "hermes-model" {
		t.Fatalf("model pins = %v", modelRefs)
	}

	if err := db.RemovePin("channel", "3"); err != nil {
		t.Fatal(err)
	}
	if err := db.RemovePin("channel", "3"); err != nil {
		t.Fatal(err)
	}
	refs, _ = db.Pins("channel")
	if len(refs) != 1 || refs[0] != "7" {
		t.Fatalf("channel pins after remove = %v", refs)
	}
}

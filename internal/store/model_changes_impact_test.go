package store_test

import (
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

func TestModelChangesWildcardAndMappingIsolation(t *testing.T) {
	db := openTestDB(t)
	a := syncModeFixture(t, db, "a", domain.ModelSyncModeManual)
	b := syncModeFixture(t, db, "b", domain.ModelSyncModeManual)
	reconcile(t, db, a, "old")
	reconcile(t, db, b, "old", "target")
	wildcard, err := db.Route.Create(&domain.Route{ModelPattern: "*", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	wildMember, err := db.RouteMember.Create(&domain.RouteMember{RouteID: wildcard, ChannelID: a, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	alias, err := db.Route.Create(&domain.Route{ModelPattern: "alias", MappingJSON: `{"real":"old"}`, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	mapped, err := db.RouteMember.Create(&domain.RouteMember{RouteID: alias, ChannelID: a, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.RouteMember.Create(&domain.RouteMember{RouteID: alias, ChannelID: b, MappingJSON: `{"real":"old"}`, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	reconcile(t, db, a, "target")
	changes, err := db.ModelChanges()
	if err != nil {
		t.Fatal(err)
	}
	var removed store.ModelChange
	for _, x := range changes.Items {
		if x.Kind == "removed" {
			removed = x
		}
	}
	if len(removed.Members) != 2 || changes.Summary.AffectedRoutes != 2 {
		t.Fatalf("impact: %+v", changes)
	}
	req := store.ModelChangeRequest{ChangeIDs: []int64{removed.ID}, MemberIDs: []int64{wildMember}, TargetChannelID: b, TargetModel: "target"}
	if _, err := db.PreviewModelChanges(req); err == nil {
		t.Fatal("unsafe wildcard remap accepted")
	}
	req.MemberIDs = []int64{mapped}
	if _, err := db.PreviewModelChanges(req); err != nil {
		t.Fatalf("route mapping not eligible: %v", err)
	}
}

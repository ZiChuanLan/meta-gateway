package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

func TestAliasPreservesMembersAcrossGroupsAndResync(t *testing.T) {
	db, err := store.OpenTest(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	channel := syncModeFixture(t, db, "upstream", domain.ModelSyncModeAuto)
	reconcile(t, db, channel, "real-a", "real-b")
	route, err := db.Route.GetByModel("real-a")
	if err != nil || route == nil {
		t.Fatal(err)
	}
	members, err := db.RouteMember.ListByRoute(route.ID)
	if err != nil || len(members) != 1 {
		t.Fatal(err)
	}
	first := members[0]
	first.Priority, first.Weight, first.ManualOverride, first.PricePromptPer1k = 42, 7, true, 0.3
	if err = db.RouteMember.Update(&first); err != nil {
		t.Fatal(err)
	}
	second := first
	second.ID = 0
	second.GroupName = "vip"
	second.Weight = 13
	second.ID, err = db.RouteMember.Create(&second)
	if err != nil {
		t.Fatal(err)
	}
	until := time.Now().UTC().Add(time.Hour)
	if _, err = db.Exec(`UPDATE route_members SET fail_count=3,cooldown_until=?,last_error='upstream_failed' WHERE id=?`, until.Format(time.RFC3339Nano), first.ID); err != nil {
		t.Fatal(err)
	}
	target, err := db.RouteMember.SetChannelModelAlias(channel, "real-a", "public")
	if err != nil {
		t.Fatal(err)
	}
	again, err := db.RouteMember.SetChannelModelAlias(channel, "real-a", "public")
	if err != nil || again != target {
		t.Fatalf("repeat: %d %v", again, err)
	}
	// A different upstream model may share the public name, even on the same channel.
	if _, err = db.RouteMember.SetChannelModelAlias(channel, "real-b", "public"); err != nil {
		t.Fatal(err)
	}
	reconcile(t, db, channel, "real-a", "real-b")
	if names := routePatterns(t, db); len(names) != 1 || !names["public"] {
		t.Fatalf("resync recreated originals: %v", names)
	}
	got, err := db.RouteMember.GetByID(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Priority != 42 || got.Weight != 7 || got.PricePromptPer1k != 0.3 || !got.ManualOverride || got.FailCount != 3 || got.CooldownUntil == nil || !got.CooldownUntil.Equal(until) {
		t.Fatalf("rename changed member state: %+v", got)
	}
	vip, err := db.RouteMember.GetByID(second.ID)
	if err != nil || vip.GroupName != "vip" || vip.Weight != 13 {
		t.Fatalf("group lost: %+v %v", vip, err)
	}
	// Restoring one real name must not delete another upstream's shared alias.
	if _, err = db.RouteMember.SetChannelModelAlias(channel, "real-a", "real-a"); err != nil {
		t.Fatal(err)
	}
	remaining, err := db.RouteMember.ListByRoute(target)
	if err != nil || len(remaining) != 1 || remaining[0].MappingJSON != `{"real":"real-b"}` {
		t.Fatalf("shared alias damaged: %+v %v", remaining, err)
	}
}

func TestAliasConflictDoesNotDeleteOrRepriceBindings(t *testing.T) {
	db, err := store.OpenTest(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ch := syncModeFixture(t, db, "upstream", domain.ModelSyncModeManual)
	for _, name := range []string{"first", "second"} {
		id, e := db.Route.Create(&domain.Route{ModelPattern: name, Enabled: true})
		if e != nil {
			t.Fatal(e)
		}
		if _, e = db.RouteMember.Create(&domain.RouteMember{RouteID: id, ChannelID: ch, Enabled: true, Weight: 17, MappingJSON: `{"real":"real"}`}); e != nil {
			t.Fatal(e)
		}
	}
	if _, err = db.RouteMember.SetChannelModelAlias(ch, "real", "target"); !errors.Is(err, store.ErrModelAliasConflict) {
		t.Fatalf("want conflict, got %v", err)
	}
	if names := routePatterns(t, db); len(names) != 2 || names["target"] {
		t.Fatalf("partial rename: %v", names)
	}
}

func TestConfigurationUpdateCannotOverwriteConcurrentHealth(t *testing.T) {
	db, err := store.OpenTest(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ch := syncModeFixture(t, db, "upstream", domain.ModelSyncModeAuto)
	reconcile(t, db, ch, "model")
	route, _ := db.Route.GetByModel("model")
	members, _ := db.RouteMember.ListByRoute(route.ID)
	stale := members[0]
	if err = db.RouteMember.RecordFailure(stale.ID, time.Now(), time.Hour, "failed"); err != nil {
		t.Fatal(err)
	}
	stale.Weight = 27
	if err = db.RouteMember.UpdateConfiguration(&stale, false); err != nil {
		t.Fatal(err)
	}
	got, _ := db.RouteMember.GetByID(stale.ID)
	if got.Weight != 27 || got.FailCount != 1 || got.CooldownUntil == nil || got.LastError != "failed" {
		t.Fatalf("stale form overwrote health: %+v", got)
	}
	stale.Enabled = false
	if err = db.RouteMember.UpdateConfiguration(&stale, true); err != nil {
		t.Fatal(err)
	}
	got, _ = db.RouteMember.GetByID(stale.ID)
	if got.Enabled || got.FailCount != 0 || got.CooldownUntil != nil {
		t.Fatalf("manual disable: %+v", got)
	}
}

func TestConfigurationPatchPreservesOtherConcurrentSettings(t *testing.T) {
	db, err := store.OpenTest(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ch := syncModeFixture(t, db, "upstream", domain.ModelSyncModeAuto)
	reconcile(t, db, ch, "model")
	route, _ := db.Route.GetByModel("model")
	members, _ := db.RouteMember.ListByRoute(route.ID)
	stale := members[0]
	if _, err = db.Exec(`UPDATE route_members SET weight=73,enabled=0,auto_disabled=1,price_per_request=.4 WHERE id=?`, stale.ID); err != nil {
		t.Fatal(err)
	}
	stale.Priority = 25
	if err = db.RouteMember.PatchConfiguration(&stale, map[string]bool{"priority": true, "fail_count": true}); err != nil {
		t.Fatal(err)
	}
	got, _ := db.RouteMember.GetByID(stale.ID)
	if got.Priority != 25 || got.Weight != 73 || got.Enabled || !got.AutoDisabled || got.PricePerRequest != .4 {
		t.Fatalf("unrelated settings overwritten: %+v", got)
	}
}

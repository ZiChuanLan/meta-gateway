package store_test

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

func TestModelChangesSnapshotLifecycle(t *testing.T) {
	db := openTestDB(t)
	a := syncModeFixture(t, db, "a", domain.ModelSyncModeAuto)
	b := syncModeFixture(t, db, "b", domain.ModelSyncModeAuto)
	reconcile(t, db, a)
	reconcile(t, db, b, "old")
	got, err := db.ModelChanges()
	if err != nil || len(got.Items) != 0 {
		t.Fatalf("baseline: %+v %v", got, err)
	}
	reconcile(t, db, a, "old")
	got, _ = db.ModelChanges()
	if len(got.Items) != 1 || got.Items[0].Kind != "added" {
		t.Fatalf("empty baseline: %+v", got)
	}
	reconcile(t, db, a, "new")
	got, _ = db.ModelChanges()
	if len(got.Items) != 3 || got.Summary.Removed != 1 || got.Summary.AffectedRoutes != 1 {
		t.Fatalf("diff: %+v", got)
	}
	var removed store.ModelChange
	for _, x := range got.Items {
		if x.Kind == "removed" {
			removed = x
		}
	}
	if !reflect.DeepEqual(removed.Candidates, []string{"new"}) || len(removed.Members) != 1 || removed.Members[0].ChannelID != a {
		t.Fatalf("impact: %+v", removed)
	}
	reconcile(t, db, a, "new")
	repeated, _ := db.ModelChanges()
	// The repeat must not spawn rows, but the pending removal's sustained-
	// absence counter advances once per complete snapshot.
	if len(repeated.Items) != len(got.Items) {
		t.Fatalf("repeat changed row count: %+v", repeated)
	}
	for i := range repeated.Items {
		if repeated.Items[i].ID != got.Items[i].ID || repeated.Items[i].Kind != got.Items[i].Kind || repeated.Items[i].Status != got.Items[i].Status {
			t.Fatalf("repeat changed log: %+v vs %+v", repeated.Items[i], got.Items[i])
		}
		if repeated.Items[i].Kind == "removed" && repeated.Items[i].MissCount != 1 {
			t.Fatalf("miss count not advanced: %+v", repeated.Items[i])
		}
	}
	// Duplicate snapshot rows force the entire reconcile transaction to fail.
	_, err = db.DiscoveredModel.Reconcile(t.Context(), store.ReconcileInput{ChannelID: a, Models: []string{"broken", "broken"}})
	if err == nil {
		t.Fatal("expected snapshot failure")
	}
	failed, _ := db.ModelChanges()
	if !reflect.DeepEqual(repeated, failed) {
		t.Fatal("failure changed log")
	}
	reconcile(t, db, a, "old", "new")
	reconcile(t, db, a, "new")
	got, _ = db.ModelChanges()
	count := 0
	for _, x := range got.Items {
		if x.Kind == "removed" && x.ModelName == "old" {
			count++
			if x.ID == removed.ID && x.Status != "resolved" {
				t.Fatal("outdated entry not resolved")
			}
		}
	}
	if count != 2 {
		t.Fatalf("repeat disappearance count %d", count)
	}
}

func TestModelChangesRemapPartialStaleRollback(t *testing.T) {
	db := openTestDB(t)
	a := syncModeFixture(t, db, "a", domain.ModelSyncModeManual)
	b := syncModeFixture(t, db, "b", domain.ModelSyncModeManual)
	reconcile(t, db, a, "old")
	reconcile(t, db, b, "target")
	var members []int64
	for _, name := range []string{"public-one", "public-two"} {
		route, err := db.Route.Create(&domain.Route{ModelPattern: name, Enabled: true, Notes: "keep"})
		if err != nil {
			t.Fatal(err)
		}
		member, err := db.RouteMember.Create(&domain.RouteMember{RouteID: route, ChannelID: a, MappingJSON: `{"real":"old"}`, Priority: 23, Weight: 7, Enabled: false, Auto: true, ManualOverride: false})
		if err != nil {
			t.Fatal(err)
		}
		members = append(members, member)
	}
	before, err := db.RouteMember.GetByID(members[0])
	if err != nil {
		t.Fatal(err)
	}
	reconcile(t, db, a, "new")
	changes, _ := db.ModelChanges()
	var changeID int64
	for _, x := range changes.Items {
		if x.Kind == "removed" {
			changeID = x.ID
		}
	}
	req := store.ModelChangeRequest{ChangeIDs: []int64{changeID}, MemberIDs: members[:1], TargetChannelID: b, TargetModel: "absent"}
	if _, err := db.PreviewModelChanges(req); err == nil {
		t.Fatal("invalid target accepted")
	}
	req.TargetModel = "target"
	preview, err := db.PreviewModelChanges(req)
	if err != nil {
		t.Fatal(err)
	}
	req.PreviewToken = preview.PreviewToken
	reconcile(t, db, b, "target")
	if _, err := db.ApplyModelChanges(req); !errors.Is(err, store.ErrModelChangeConflict) {
		t.Fatalf("stale accepted: %v", err)
	}
	preview, err = db.PreviewModelChanges(req)
	if err != nil {
		t.Fatal(err)
	}
	req.PreviewToken = preview.PreviewToken
	if n, err := db.ApplyModelChanges(req); err != nil || n != 1 {
		t.Fatalf("apply: %d %v", n, err)
	}
	after, _ := db.RouteMember.GetByID(members[0])
	before.ChannelID = b
	before.MappingJSON = `{"real":"target"}`
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("settings changed: before=%+v after=%+v", before, after)
	}
	changes, _ = db.ModelChanges()
	for _, x := range changes.Items {
		if x.ID == changeID && (x.Status != "pending" || len(x.Members) != 1) {
			t.Fatalf("partial: %+v", x)
		}
	}
	if err := db.IgnoreModelChanges([]int64{changeID, 999999}); !errors.Is(err, store.ErrModelChangeConflict) {
		t.Fatalf("ignore invalid: %v", err)
	}
	req.MemberIDs = members[1:]
	preview, err = db.PreviewModelChanges(req)
	if err != nil {
		t.Fatal(err)
	}
	req.PreviewToken = preview.PreviewToken
	if _, err := db.ApplyModelChanges(req); err != nil {
		t.Fatal(err)
	}
	changes, _ = db.ModelChanges()
	for _, x := range changes.Items {
		if x.ID == changeID && x.Status != "applied" {
			t.Fatalf("not applied: %+v", x)
		}
	}
	reconcile(t, db, a, "new")
	after, _ = db.RouteMember.GetByID(members[0])
	if !reflect.DeepEqual(before, after) {
		t.Fatal("reconcile overwrote remap")
	}
}

func TestModelChangesBulkApplyRollsBack(t *testing.T) {
	db := openTestDB(t)
	a := syncModeFixture(t, db, "a", domain.ModelSyncModeAuto)
	reconcile(t, db, a, "one", "two")
	reconcile(t, db, a, "target")
	changes, _ := db.ModelChanges()
	req := store.ModelChangeRequest{TargetChannelID: a, TargetModel: "target"}
	for _, x := range changes.Items {
		if x.Kind == "removed" {
			req.ChangeIDs = append(req.ChangeIDs, x.ID)
			req.MemberIDs = append(req.MemberIDs, x.Members[0].MemberID)
		}
	}
	preview, err := db.PreviewModelChanges(req)
	if err != nil {
		t.Fatal(err)
	}
	req.PreviewToken = preview.PreviewToken
	// A database failure on the second write must roll back the first as well.
	last := preview.Items[len(preview.Items)-1].MemberID
	_, err = db.Exec(`CREATE TRIGGER fail_remap BEFORE UPDATE OF mapping_json ON route_members WHEN NEW.id=` + strconv.FormatInt(last, 10) + ` BEGIN SELECT RAISE(ABORT,'test rollback'); END`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ApplyModelChanges(req); err == nil {
		t.Fatal("expected trigger failure")
	}
	after, _ := db.ModelChanges()
	if !reflect.DeepEqual(changes, after) {
		t.Fatal("apply did not roll back")
	}
	if _, err := db.Exec(`DROP TRIGGER fail_remap`); err != nil {
		t.Fatal(err)
	}
	if n, err := db.ApplyModelChanges(req); err != nil || n != 2 {
		t.Fatalf("bulk: %d %v", n, err)
	}
}

func findRemoved(t *testing.T, items []store.ModelChange, model string) store.ModelChange {
	t.Helper()
	for _, x := range items {
		if x.Kind == "removed" && x.ModelName == model {
			return x
		}
	}
	t.Fatalf("removed row for %q not found", model)
	return store.ModelChange{}
}

func TestModelChangesConfirmationSignals(t *testing.T) {
	db := openTestDB(t)
	a := syncModeFixture(t, db, "conf", domain.ModelSyncModeManual)
	reconcile(t, db, a, "gone")
	reconcile(t, db, a)
	removed := findRemoved(t, mustChanges(t, db).Items, "gone")
	if removed.Confirmed || removed.MissCount != 0 {
		t.Fatalf("detection round: %+v", removed)
	}

	// Each complete sync without the model advances the counter; the row is
	// confirmed once it reaches the threshold.
	for i := 0; i < store.ModelChangeConfirmSynces; i++ {
		reconcile(t, db, a)
	}
	removed = findRemoved(t, mustChanges(t, db).Items, "gone")
	if !removed.Confirmed || removed.MissCount != store.ModelChangeConfirmSynces {
		t.Fatalf("after %d syncs: %+v", store.ModelChangeConfirmSynces, removed)
	}

	// A partial snapshot (some keys silent) is unreliable evidence: flagged on
	// fresh removals, and it must not advance the confirmation counter.
	if _, err := db.DiscoveredModel.Reconcile(context.Background(), store.ReconcileInput{
		ChannelID: a, Source: "test", CheckedAt: time.Now(), PartialDiscovery: true,
	}); err != nil {
		t.Fatalf("partial reconcile: %v", err)
	}
	removed = findRemoved(t, mustChanges(t, db).Items, "gone")
	if !removed.Confirmed || removed.MissCount != store.ModelChangeConfirmSynces {
		t.Fatalf("partial sync advanced counter: %+v", removed)
	}

	// The relay-reported model-not-found blacklist surfaces on the row.
	if err := db.BlockModel(a, "gone", "model_not_found"); err != nil {
		t.Fatal(err)
	}
	removed = findRemoved(t, mustChanges(t, db).Items, "gone")
	if !removed.RuntimeBlocked || removed.BlockedAt == "" {
		t.Fatalf("runtime block not surfaced: %+v", removed)
	}

	// Return, then vanish again inside the flap window: the fresh removal
	// carries the churn counter instead of reading as a first-time event.
	if _, err := db.Exec(`DELETE FROM channel_model_blocks WHERE channel_id=?`, a); err != nil {
		t.Fatal(err)
	}
	reconcile(t, db, a, "gone")
	reconcile(t, db, a)
	removed = findRemoved(t, mustChanges(t, db).Items, "gone")
	if removed.FlapCount != 1 {
		t.Fatalf("flap count: %+v", removed)
	}
}

func TestModelChangesPruneAndAutoIgnore(t *testing.T) {
	db := openTestDB(t)
	a := syncModeFixture(t, db, "sweep", domain.ModelSyncModeManual)
	reconcile(t, db, a, "gone-impact", "stale-harmless")
	// "gone-impact" has a live member, so its removal is NOT harmless.
	route, err := db.Route.Create(&domain.Route{ModelPattern: "gone-impact", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.RouteMember.Create(&domain.RouteMember{RouteID: route, ChannelID: a, Weight: 100, Enabled: true, Auto: true}); err != nil {
		t.Fatal(err)
	}
	reconcile(t, db, a, "stale-harmless", "brand-new")
	reconcile(t, db, a, "brand-new")
	changes := mustChanges(t, db)
	if changes.Summary.Removed != 2 {
		t.Fatalf("setup: %+v", changes.Summary)
	}

	stale := time.Now().UTC().AddDate(0, 0, -30).Format(time.RFC3339Nano)
	if _, err := db.Exec(`UPDATE model_changes SET detected_at=? WHERE kind='removed'`, stale); err != nil {
		t.Fatal(err)
	}

	// Auto-ignore flips only the member-free removal; "gone-impact" keeps
	// reminding because a route member still binds the model.
	n, err := db.AutoIgnoreHarmlessModelChanges(7)
	if err != nil || n != 1 {
		t.Fatalf("auto ignore: %d %v", n, err)
	}
	changes = mustChanges(t, db)
	if got := findRemoved(t, changes.Items, "stale-harmless"); got.Status != "ignored" {
		t.Fatalf("harmless not ignored: %+v", got)
	}
	if got := findRemoved(t, changes.Items, "gone-impact"); got.Status != "pending" {
		t.Fatalf("impacted removal ignored: %+v", got)
	}

	// Prune deletes finished rows past the window; pending ones survive.
	if _, err := db.Exec(`UPDATE model_changes SET detected_at=?, status='resolved' WHERE model_name='stale-harmless'`, stale); err != nil {
		t.Fatal(err)
	}
	pruned, err := db.PruneModelChanges(7)
	if err != nil || pruned != 1 {
		t.Fatalf("prune: %d %v", pruned, err)
	}
	changes = mustChanges(t, db)
	for _, x := range changes.Items {
		if x.ModelName == "stale-harmless" {
			t.Fatalf("pruned row survived: %+v", x)
		}
	}
}

func mustChanges(t *testing.T, db *store.DB) store.ModelChanges {
	t.Helper()
	changes, err := db.ModelChanges()
	if err != nil {
		t.Fatalf("model changes: %v", err)
	}
	return changes
}

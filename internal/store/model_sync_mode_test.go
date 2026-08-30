package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

// syncModeFixture creates one channel in the given sync mode.
func syncModeFixture(t *testing.T, db *store.DB, name string, mode string) int64 {
	t.Helper()
	id, err := db.Channel.Create(&domain.Channel{
		Name: name, BaseURL: "https://api.example.com", Weight: 100,
		Status: domain.StatusEnabled, ModelSyncMode: mode,
	})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	return id
}

func reconcile(t *testing.T, db *store.DB, channelID int64, models ...string) store.ReconcileResult {
	t.Helper()
	result, err := db.DiscoveredModel.Reconcile(context.Background(), store.ReconcileInput{
		ChannelID: channelID, Models: models, Source: "test",
		CheckedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	return result
}

func routePatterns(t *testing.T, db *store.DB) map[string]bool {
	t.Helper()
	routes, err := db.Route.List()
	if err != nil {
		t.Fatalf("route list: %v", err)
	}
	out := map[string]bool{}
	for _, route := range routes {
		out[route.ModelPattern] = true
	}
	return out
}

func TestChannelSyncModeNormalize(t *testing.T) {
	db := openTestDB(t)

	// Create with an empty mode falls back to manual (safe default).
	emptyID := syncModeFixture(t, db, "empty-mode", "")
	got, err := db.Channel.GetByID(emptyID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ModelSyncMode != domain.ModelSyncModeManual {
		t.Errorf("empty create mode = %q, want manual", got.ModelSyncMode)
	}

	autoID := syncModeFixture(t, db, "auto-mode", domain.ModelSyncModeAuto)
	got, err = db.Channel.GetByID(autoID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ModelSyncMode != domain.ModelSyncModeAuto {
		t.Errorf("explicit create mode = %q, want auto", got.ModelSyncMode)
	}

	// Update round-trips the mode (full-row UPDATE must not lose it).
	got.ModelSyncMode = domain.ModelSyncModeAuto
	if err := db.Channel.Update(got); err != nil {
		t.Fatal(err)
	}
	got, err = db.Channel.GetByID(autoID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ModelSyncMode != domain.ModelSyncModeAuto {
		t.Errorf("mode after update = %q, want auto", got.ModelSyncMode)
	}

	got, err = db.Channel.GetByID(emptyID)
	if err != nil {
		t.Fatal(err)
	}
	got.ModelSyncMode = domain.ModelSyncModeAuto
	if err := db.Channel.Update(got); err != nil {
		t.Fatal(err)
	}
	got, err = db.Channel.GetByID(emptyID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ModelSyncMode != domain.ModelSyncModeAuto {
		t.Errorf("mode after switch = %q, want auto", got.ModelSyncMode)
	}
}

// Manual-sync discovery must only refresh the snapshot: no route or member may
// appear, while models_csv still tracks the upstream list. An auto channel in
// the same run keeps adopting everything.
func TestReconcileManualModeSkipsRoutes(t *testing.T) {
	db := openTestDB(t)
	manualID := syncModeFixture(t, db, "manual-ch", domain.ModelSyncModeManual)
	autoID := syncModeFixture(t, db, "auto-ch", domain.ModelSyncModeAuto)

	reconcile(t, db, manualID, "m-a", "m-b")
	reconcile(t, db, autoID, "a-1")

	patterns := routePatterns(t, db)
	if patterns["m-a"] || patterns["m-b"] {
		t.Errorf("manual channel created routes: %v", patterns)
	}
	if !patterns["a-1"] {
		t.Errorf("auto channel did not adopt its model: %v", patterns)
	}

	snapshot, err := db.DiscoveredModel.List(&manualID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot) != 2 {
		t.Fatalf("manual snapshot size = %d, want 2 (snapshot must still refresh)", len(snapshot))
	}

	channel, err := db.Channel.GetByID(manualID)
	if err != nil {
		t.Fatal(err)
	}
	if channel.ModelsCSV != "m-a,m-b" {
		t.Errorf("models_csv = %q, want m-a,m-b", channel.ModelsCSV)
	}
}

// Reconcile never touches an existing member's enabled flag, even for a
// manual-sync channel whose adopted member is disabled by the operator.
func TestReconcileManualModeKeepsAdoptedMemberState(t *testing.T) {
	db := openTestDB(t)
	manualID := syncModeFixture(t, db, "manual-ch", domain.ModelSyncModeManual)

	routeID, err := db.Route.Create(&domain.Route{ModelPattern: "m-a", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	memberID, err := db.RouteMember.Create(&domain.RouteMember{
		RouteID: routeID, ChannelID: manualID, Weight: 100, Enabled: true, Auto: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	member := getMember(t, db, memberID)
	member.Enabled = false
	if err := db.RouteMember.Update(member); err != nil {
		t.Fatal(err)
	}
	if err := db.RouteMember.ApplyManualIntent(memberID, false); err != nil {
		t.Fatal(err)
	}

	reconcile(t, db, manualID, "m-a")

	got := getMember(t, db, memberID)
	if got.Enabled {
		t.Error("reconcile resurrected a manually disabled member")
	}
}

// An adopted manual-sync member (auto=1, manual_override=0) whose model
// disappears from upstream is recycled by the same stale cleanup as auto
// members, so dead routes do not linger.
func TestReconcileManualModeStaleCleanup(t *testing.T) {
	db := openTestDB(t)
	manualID := syncModeFixture(t, db, "manual-ch", domain.ModelSyncModeManual)

	// First discovery puts the model into the snapshot (no route/member in
	// manual mode); the operator then adopts it from the panel.
	reconcile(t, db, manualID, "adopted-model")
	routeID, err := db.Route.Create(&domain.Route{ModelPattern: "adopted-model", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	memberID, err := db.RouteMember.Create(&domain.RouteMember{
		RouteID: routeID, ChannelID: manualID, Weight: 100, Enabled: true, Auto: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Next discovery drops the model upstream: the adopted member is recycled.
	reconcile(t, db, manualID, "other-model")

	if got, getErr := db.RouteMember.GetByID(memberID); getErr != nil || got != nil {
		t.Errorf("stale adopted member should have been removed (got %v, err %v)", got, getErr)
	}
	patterns := routePatterns(t, db)
	if patterns["adopted-model"] {
		t.Error("empty route for a vanished adopted model should have been removed")
	}
	snapshot, err := db.DiscoveredModel.List(&manualID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot) != 1 || snapshot[0].ModelName != "other-model" {
		t.Fatalf("snapshot = %+v, want only other-model", snapshot)
	}
}

// Manual-sync channels never show up in the missing-models report: unadopted
// models are intent, not gaps.
func TestMissingModelsSkipsManualChannels(t *testing.T) {
	db := openTestDB(t)
	manualID := syncModeFixture(t, db, "manual-ch", domain.ModelSyncModeManual)
	autoID := syncModeFixture(t, db, "auto-ch", domain.ModelSyncModeAuto)

	if err := db.Channel.Update(&domain.Channel{
		ID: manualID, Name: "manual-ch", BaseURL: "https://api.example.com",
		Status: domain.StatusEnabled, ModelSyncMode: domain.ModelSyncModeManual, ModelsCSV: "gone-model",
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Channel.Update(&domain.Channel{
		ID: autoID, Name: "auto-ch", BaseURL: "https://api.example.com",
		Status: domain.StatusEnabled, ModelSyncMode: domain.ModelSyncModeAuto, ModelsCSV: "auto-model",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO discovered_models (channel_id, model_name, available, source, latency_ms, checked_at) VALUES (?, 'snapshot-model', 1, 'test', 0, datetime('now'))`, manualID); err != nil {
		t.Fatal(err)
	}

	missing, err := db.MissingModels()
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 1 || missing[0].Model != "auto-model" {
		t.Fatalf("missing = %+v, want only auto-model (manual channel skipped)", missing)
	}
}

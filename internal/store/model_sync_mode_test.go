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

// The edit drawer initializes its whole form from GET /admin/channels/overview,
// so ListOverviews must carry every per-channel override the drawer writes back.
// A column missing from that projection silently round-trips as its zero value
// and saving the form would then wipe the stored configuration (regression:
// model_sync_mode always read back as "manual").
func TestListOverviewsCarriesChannelOverrides(t *testing.T) {
	db := openTestDB(t)
	id := syncModeFixture(t, db, "override-ch", domain.ModelSyncModeAuto)

	channel, err := db.Channel.GetByID(id)
	if err != nil {
		t.Fatal(err)
	}
	channel.ModelSyncMode = domain.ModelSyncModeAuto
	channel.MaxReasoningEffort = "high"
	channel.PayloadRules = `[{"name":"cap","match":{},"actions":[]}]`
	channel.MaxConcurrent = 7
	channel.ProxyURL = "http://127.0.0.1:7897"
	if err := db.Channel.Update(channel); err != nil {
		t.Fatal(err)
	}

	rows, err := db.Channel.ListOverviews(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var found *domain.ChannelOverview
	for index := range rows {
		if rows[index].Channel.ID == id {
			found = &rows[index]
			break
		}
	}
	if found == nil {
		t.Fatalf("channel %d missing from ListOverviews", id)
	}
	if found.Channel.ModelSyncMode != domain.ModelSyncModeAuto {
		t.Errorf("model_sync_mode = %q, want auto", found.Channel.ModelSyncMode)
	}
	if found.Channel.MaxReasoningEffort != "high" {
		t.Errorf("max_reasoning_effort = %q, want high", found.Channel.MaxReasoningEffort)
	}
	if found.Channel.PayloadRules != `[{"name":"cap","match":{},"actions":[]}]` {
		t.Errorf("payload_rules = %q, want the stored rule array", found.Channel.PayloadRules)
	}
	if found.Channel.MaxConcurrent != 7 {
		t.Errorf("max_concurrent = %d, want 7", found.Channel.MaxConcurrent)
	}
	if found.Channel.ProxyURL != "http://127.0.0.1:7897" {
		t.Errorf("proxy_url = %q, want http://127.0.0.1:7897", found.Channel.ProxyURL)
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

// Adopted manual-sync members survive disappearance for operator remapping.
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

	// Next discovery drops the model upstream: retain the adopted binding.
	reconcile(t, db, manualID, "other-model")

	if got, getErr := db.RouteMember.GetByID(memberID); getErr != nil || got == nil {
		t.Errorf("stale adopted member should be retained (got %v, err %v)", got, getErr)
	}
	patterns := routePatterns(t, db)
	if !patterns["adopted-model"] {
		t.Error("route for a vanished adopted model should be retained")
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

// A channel created without an explicit mode inherits the Admin-configured
// default from runtime settings; explicit modes always win, and a missing or
// malformed runtime value degrades to manual.
func TestChannelCreateUsesRuntimeDefaultSyncMode(t *testing.T) {
	db := openTestDB(t)

	// No runtime row yet → manual.
	noRowID := syncModeFixture(t, db, "no-runtime-row", "")
	if got, _ := db.Channel.GetByID(noRowID); got.ModelSyncMode != domain.ModelSyncModeManual {
		t.Errorf("no runtime row: mode = %q, want manual", got.ModelSyncMode)
	}

	// Admin sets the default to auto → empty create inherits it, an explicit
	// manual still wins.
	if err := db.RuntimeSettings.Save(&store.RuntimeSettingsRow{HasOverride: true, DefaultModelSyncMode: domain.ModelSyncModeAuto}); err != nil {
		t.Fatal(err)
	}
	inheritID := syncModeFixture(t, db, "inherits-auto", "")
	if got, _ := db.Channel.GetByID(inheritID); got.ModelSyncMode != domain.ModelSyncModeAuto {
		t.Errorf("inherited mode = %q, want auto", got.ModelSyncMode)
	}
	explicitID := syncModeFixture(t, db, "explicit-manual", domain.ModelSyncModeManual)
	if got, _ := db.Channel.GetByID(explicitID); got.ModelSyncMode != domain.ModelSyncModeManual {
		t.Errorf("explicit mode = %q, want manual", got.ModelSyncMode)
	}

	// A malformed stored value must degrade to manual, not leak through.
	if err := db.RuntimeSettings.Save(&store.RuntimeSettingsRow{HasOverride: true, DefaultModelSyncMode: "sometimes"}); err != nil {
		t.Fatal(err)
	}
	garbageID := syncModeFixture(t, db, "garbage-default", "")
	if got, _ := db.Channel.GetByID(garbageID); got.ModelSyncMode != domain.ModelSyncModeManual {
		t.Errorf("garbage runtime default: mode = %q, want manual", got.ModelSyncMode)
	}
}

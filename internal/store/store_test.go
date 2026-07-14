package store_test

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

func openTestDB(t *testing.T) *store.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	// ensure file path is under temp
	_ = filepath.Join(dir, "meta-gateway.db")
	return db
}

func TestSiteChannelRouteCRUD(t *testing.T) {
	db := openTestDB(t)

	siteID, err := db.Site.Create(&domain.Site{
		Name:     "demo",
		BaseURL:  "https://api.example.com",
		Platform: "openai-compatible",
		Status:   domain.StatusEnabled,
	})
	if err != nil {
		t.Fatalf("create site: %v", err)
	}

	credID, err := db.Credential.Create(&domain.Credential{
		SiteID:    siteID,
		Kind:      "api_key",
		SecretEnc: []byte("v1:cipher"),
		Status:    domain.StatusEnabled,
	})
	if err != nil {
		t.Fatalf("create credential: %v", err)
	}

	chID, err := db.Channel.Create(&domain.Channel{
		SiteID:       &siteID,
		CredentialID: &credID,
		Name:         "main",
		BaseURL:      "https://api.example.com",
		ModelsCSV:    "gpt-4o,gpt-4o-mini",
		GroupName:    "default",
		Priority:     0,
		Weight:       100,
		Status:       domain.StatusEnabled,
	})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}

	routeID, err := db.Route.Create(&domain.Route{
		ModelPattern: "gpt-4o",
		Enabled:      true,
	})
	if err != nil {
		t.Fatalf("create route: %v", err)
	}

	memberID, err := db.RouteMember.Create(&domain.RouteMember{
		RouteID:   routeID,
		ChannelID: chID,
		Priority:  0,
		Weight:    100,
		Enabled:   true,
	})
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
	if memberID <= 0 {
		t.Fatal("expected member id")
	}

	gotRoute, err := db.Route.GetByModel("gpt-4o")
	if err != nil || gotRoute == nil {
		t.Fatalf("get by model: %v %#v", err, gotRoute)
	}
	if !gotRoute.Enabled {
		t.Fatal("route should be enabled")
	}

	members, err := db.RouteMember.ListByRoute(routeID)
	if err != nil {
		t.Fatalf("list members: %v", err)
	}
	if len(members) != 1 || members[0].ChannelID != chID {
		t.Fatalf("unexpected members: %+v", members)
	}

	channels, err := db.Channel.ListEnabled()
	if err != nil || len(channels) != 1 {
		t.Fatalf("list enabled: %v len=%d", err, len(channels))
	}
}

func TestDownstreamKeyByHash(t *testing.T) {
	db := openTestDB(t)
	id, err := db.DownstreamKey.Create(&domain.DownstreamKey{
		TokenHash: "abc",
		Name:      "app",
		Enabled:   true,
		Scopes:    "relay",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := db.DownstreamKey.GetByHash("abc")
	if err != nil || got == nil || got.ID != id {
		t.Fatalf("get by hash: %v %#v", err, got)
	}
	missing, err := db.DownstreamKey.GetByHash("nope")
	if err != nil || missing != nil {
		t.Fatalf("expected nil missing, got %#v err=%v", missing, err)
	}
}

func TestMigrationsAreTrackedAndIdempotent(t *testing.T) {
	db := openTestDB(t)
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 6 {
		t.Fatalf("got %d applied migrations, want 6", count)
	}
	if err := store.Migrate(db.DB); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil || count != 6 {
		t.Fatalf("migration history after rerun: count=%d err=%v", count, err)
	}
}

func TestCheckinCredentialAndLogs(t *testing.T) {
	db := openTestDB(t)
	siteID, err := db.Site.Create(&domain.Site{Name: "checkin", BaseURL: "https://example.com", Platform: "new-api", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	credentialID, err := db.Credential.Create(&domain.Credential{SiteID: siteID, Kind: "session", SecretEnc: []byte("cipher"), Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	credential, err := db.Credential.GetByID(credentialID)
	if err != nil || credential.CheckinEnabled {
		t.Fatalf("new credential scheduling must default off: %+v err=%v", credential, err)
	}
	if err := db.Credential.SetCheckinEnabled(credentialID, true); err != nil {
		t.Fatal(err)
	}
	enabled, err := db.Credential.ListCheckinEnabled()
	if err != nil || len(enabled) != 1 || enabled[0].ID != credentialID {
		t.Fatalf("enabled credentials: %+v err=%v", enabled, err)
	}

	older := time.Date(2026, 7, 14, 1, 2, 3, 456000000, time.UTC)
	newer := older.Add(time.Minute)
	for _, entry := range []domain.CheckinLog{
		{SiteID: siteID, CredentialID: credentialID, Source: "manual", Status: "failed", Category: "upstream_status", Message: "upstream request failed", LatencyMs: 12, RanAt: older},
		{SiteID: siteID, CredentialID: credentialID, Source: "scheduled", Status: "success", Category: "checked_in", Message: "check-in succeeded", Reward: "1.25", LatencyMs: 8, RanAt: newer},
	} {
		if err := db.CheckinLog.Create(&entry); err != nil {
			t.Fatal(err)
		}
	}
	logs, err := db.CheckinLog.List(store.CheckinLogFilter{CredentialID: &credentialID, Status: "success", Limit: 10})
	if err != nil || len(logs) != 1 || logs[0].Reward != "1.25" || !logs[0].RanAt.Equal(newer) {
		t.Fatalf("filtered logs: %+v err=%v", logs, err)
	}
	if err := db.Credential.Delete(credentialID); err != nil {
		t.Fatal(err)
	}
	logs, err = db.CheckinLog.List(store.CheckinLogFilter{SiteID: &siteID})
	if err != nil || len(logs) != 0 {
		t.Fatalf("credential cascade logs: %+v err=%v", logs, err)
	}
}

func TestDiscoveryReconcileIsIdempotentAndProtectsManualMembers(t *testing.T) {
	db := openTestDB(t)
	channelID, err := db.Channel.Create(&domain.Channel{Name: "discovery", Priority: 7, Weight: 33, Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	checkedAt := time.Date(2026, 7, 14, 1, 2, 3, 0, time.UTC)
	input := store.ReconcileInput{ChannelID: channelID, Models: []string{"model-a", "model-b"}, Source: "openai-compatible", LatencyMs: 12, CheckedAt: checkedAt}
	first, err := db.DiscoveredModel.Reconcile(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if first.CreatedRoutes != 2 || first.CreatedMembers != 2 {
		t.Fatalf("unexpected first result: %+v", first)
	}
	second, err := db.DiscoveredModel.Reconcile(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if second != (store.ReconcileResult{}) {
		t.Fatalf("repeated refresh was not idempotent: %+v", second)
	}

	routeA, _ := db.Route.GetByModel("model-a")
	members, _ := db.RouteMember.ListByRoute(routeA.ID)
	memberA := members[0]
	memberA.Priority = 99
	memberA.Weight = 5
	memberA.ManualOverride = true
	if err := db.RouteMember.Update(&memberA); err != nil {
		t.Fatal(err)
	}

	input.Models = []string{"model-b"}
	changed, err := db.DiscoveredModel.Reconcile(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if changed.DisabledMembers != 0 {
		t.Fatalf("manual override was disabled: %+v", changed)
	}
	got, _ := db.RouteMember.GetByID(memberA.ID)
	if !got.Enabled || !got.ManualOverride || got.Priority != 99 || got.Weight != 5 {
		t.Fatalf("manual member changed: %+v", got)
	}

	models, err := db.DiscoveredModel.List(&channelID)
	if err != nil || len(models) != 1 || models[0].ModelName != "model-b" {
		t.Fatalf("unexpected snapshot: %+v err=%v", models, err)
	}
	channel, _ := db.Channel.GetByID(channelID)
	if channel.ModelsCSV != "model-b" {
		t.Fatalf("models_csv=%q", channel.ModelsCSV)
	}
}

func TestDiscoveryReconcileDisablesAndReenablesAutomaticMember(t *testing.T) {
	db := openTestDB(t)
	channelID, _ := db.Channel.Create(&domain.Channel{Name: "discovery", Priority: 2, Weight: 10, Status: domain.StatusEnabled})
	base := store.ReconcileInput{ChannelID: channelID, Models: []string{"model-a"}, Source: "new-api", CheckedAt: time.Now()}
	if _, err := db.DiscoveredModel.Reconcile(t.Context(), base); err != nil {
		t.Fatal(err)
	}
	route, _ := db.Route.GetByModel("model-a")
	members, _ := db.RouteMember.ListByRoute(route.ID)
	member := members[0]
	member.Priority = 88
	member.Weight = 4
	if err := db.RouteMember.Update(&member); err != nil {
		t.Fatal(err)
	}
	base.Models = nil
	result, err := db.DiscoveredModel.Reconcile(t.Context(), base)
	if err != nil || result.DisabledMembers != 1 {
		t.Fatalf("empty reconcile: %+v err=%v", result, err)
	}
	base.Models = []string{"model-a"}
	result, err = db.DiscoveredModel.Reconcile(t.Context(), base)
	if err != nil || result.EnabledMembers != 1 {
		t.Fatalf("restore reconcile: %+v err=%v", result, err)
	}
	got, _ := db.RouteMember.GetByID(member.ID)
	if !got.Enabled || got.Priority != 88 || got.Weight != 4 {
		t.Fatalf("automatic member defaults were reset: %+v", got)
	}
}

func TestDiscoveryReconcileDoesNotChangeManualMember(t *testing.T) {
	db := openTestDB(t)
	channelID, _ := db.Channel.Create(&domain.Channel{Name: "manual", Status: domain.StatusEnabled})
	routeID, _ := db.Route.Create(&domain.Route{ModelPattern: "manual-model", Enabled: true})
	memberID, err := db.RouteMember.Create(&domain.RouteMember{RouteID: routeID, ChannelID: channelID, Priority: 42, Weight: 9, Enabled: true, Auto: false})
	if err != nil {
		t.Fatal(err)
	}
	input := store.ReconcileInput{ChannelID: channelID, Models: []string{"manual-model"}, CheckedAt: time.Now()}
	if _, err := db.DiscoveredModel.Reconcile(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	input.Models = nil
	result, err := db.DiscoveredModel.Reconcile(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	member, _ := db.RouteMember.GetByID(memberID)
	if result.DisabledMembers != 0 || !member.Enabled || member.Auto || member.Priority != 42 || member.Weight != 9 {
		t.Fatalf("manual member changed: result=%+v member=%+v", result, member)
	}
}

func TestDiscoveredModelsCascadeWithChannel(t *testing.T) {
	db := openTestDB(t)
	channelID, _ := db.Channel.Create(&domain.Channel{Name: "cascade", Status: domain.StatusEnabled})
	_, err := db.DiscoveredModel.Reconcile(t.Context(), store.ReconcileInput{ChannelID: channelID, Models: []string{"model"}, CheckedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Channel.Delete(channelID); err != nil {
		t.Fatal(err)
	}
	models, err := db.DiscoveredModel.List(&channelID)
	if err != nil || len(models) != 0 {
		t.Fatalf("cascade failed: %+v err=%v", models, err)
	}
}

func TestDiscoveryReconcileRollsBackAllState(t *testing.T) {
	db := openTestDB(t)
	channelID, _ := db.Channel.Create(&domain.Channel{Name: "rollback", Status: domain.StatusEnabled})
	valid := store.ReconcileInput{ChannelID: channelID, Models: []string{"old-model"}, Source: "openai-compatible", CheckedAt: time.Now()}
	if _, err := db.DiscoveredModel.Reconcile(t.Context(), valid); err != nil {
		t.Fatal(err)
	}
	invalid := valid
	invalid.Models = []string{"duplicate", "duplicate"}
	if _, err := db.DiscoveredModel.Reconcile(t.Context(), invalid); err == nil {
		t.Fatal("expected duplicate snapshot insert to fail")
	}
	models, err := db.DiscoveredModel.List(&channelID)
	if err != nil || len(models) != 1 || models[0].ModelName != "old-model" {
		t.Fatalf("snapshot did not roll back: models=%+v err=%v", models, err)
	}
	channel, _ := db.Channel.GetByID(channelID)
	if channel.ModelsCSV != "old-model" {
		t.Fatalf("channel models did not roll back: %q", channel.ModelsCSV)
	}
	var duplicateRoutes int
	if err := db.QueryRow(`SELECT COUNT(*) FROM routes WHERE model_pattern = 'duplicate'`).Scan(&duplicateRoutes); err != nil || duplicateRoutes != 0 {
		t.Fatalf("route changes did not roll back: count=%d err=%v", duplicateRoutes, err)
	}
}

func TestP0P2DatabaseUpgradesWithoutDataLoss(t *testing.T) {
	dir := t.TempDir()
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "meta-gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE sites (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL DEFAULT '', base_url TEXT NOT NULL DEFAULT '', platform TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT 'enabled', created_at TEXT NOT NULL DEFAULT (datetime('now')), updated_at TEXT NOT NULL DEFAULT (datetime('now'))); INSERT INTO sites(name, base_url, platform) VALUES ('legacy', 'https://legacy.example', 'openai-compatible');`); err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()

	db, err := store.Open(dir)
	if err != nil {
		t.Fatalf("upgrade open: %v", err)
	}
	defer db.Close()
	sites, err := db.Site.List()
	if err != nil || len(sites) != 1 || sites[0].Name != "legacy" {
		t.Fatalf("legacy data after upgrade: sites=%+v err=%v", sites, err)
	}
}

func TestP0P4DatabaseUpgradesToCheckinWithoutEnablingCredentials(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	siteID, err := db.Site.Create(&domain.Site{Name: "legacy-p4", BaseURL: "https://legacy.example", Platform: "new-api", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	credentialID, err := db.Credential.Create(&domain.Credential{SiteID: siteID, Kind: "session", SecretEnc: []byte("v1:legacy"), Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP TABLE checkin_logs; ALTER TABLE credentials DROP COLUMN checkin_enabled; DELETE FROM schema_migrations WHERE name = '004_checkin.sql'`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := store.Open(dir)
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	defer upgraded.Close()
	credential, err := upgraded.Credential.GetByID(credentialID)
	if err != nil || credential == nil || credential.CheckinEnabled || credential.Kind != "session" {
		t.Fatalf("credential after upgrade=%+v err=%v", credential, err)
	}
	var migrationCount int
	if err := upgraded.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE name = '004_checkin.sql'`).Scan(&migrationCount); err != nil || migrationCount != 1 {
		t.Fatalf("migration count=%d err=%v", migrationCount, err)
	}
	if err := store.Migrate(upgraded.DB); err != nil {
		t.Fatalf("idempotent migration: %v", err)
	}
}

func TestRoutingUniqueConstraints(t *testing.T) {
	db := openTestDB(t)
	routeID, err := db.Route.Create(&domain.Route{ModelPattern: "unique-model", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Route.Create(&domain.Route{ModelPattern: "unique-model", Enabled: true}); err == nil {
		t.Fatal("expected duplicate route to fail")
	}
	channelID, err := db.Channel.Create(&domain.Channel{Name: "channel", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	member := &domain.RouteMember{RouteID: routeID, ChannelID: channelID, Enabled: true, Weight: 1}
	if _, err := db.RouteMember.Create(member); err != nil {
		t.Fatal(err)
	}
	if _, err := db.RouteMember.Create(member); err == nil {
		t.Fatal("expected duplicate member to fail")
	}
}

func TestRouteMemberCooldownRoundTrip(t *testing.T) {
	db := openTestDB(t)
	routeID, _ := db.Route.Create(&domain.Route{ModelPattern: "cooldown-model", Enabled: true})
	channelID, _ := db.Channel.Create(&domain.Channel{Name: "channel", Status: domain.StatusEnabled})
	memberID, err := db.RouteMember.Create(&domain.RouteMember{RouteID: routeID, ChannelID: channelID, Enabled: true, Weight: 1})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 14, 0, 0, 0, 123, time.UTC)
	if err := db.RouteMember.RecordFailure(memberID, now, time.Minute, "transport"); err != nil {
		t.Fatal(err)
	}
	member, err := db.RouteMember.GetByID(memberID)
	if err != nil {
		t.Fatal(err)
	}
	want := now.Add(time.Minute)
	if member.CooldownUntil == nil || !member.CooldownUntil.Equal(want) || member.FailCount != 1 {
		t.Fatalf("cooldown round trip: %+v", member)
	}
}

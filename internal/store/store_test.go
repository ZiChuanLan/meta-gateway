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
	if gotRoute.RoutingMode != domain.RoutingModeAuto {
		t.Fatalf("default routing mode = %q, want %q", gotRoute.RoutingMode, domain.RoutingModeAuto)
	}

	gotRoute.RoutingMode = domain.RoutingModeLatency
	if err := db.Route.Update(gotRoute); err != nil {
		t.Fatalf("update routing mode: %v", err)
	}
	reloaded, err := db.Route.GetByID(routeID)
	if err != nil || reloaded == nil {
		t.Fatalf("reload route: %v %#v", err, reloaded)
	}
	if reloaded.RoutingMode != domain.RoutingModeLatency {
		t.Fatalf("routing mode after update = %q, want %q", reloaded.RoutingMode, domain.RoutingModeLatency)
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
	if count != 49 {
		t.Fatalf("got %d applied migrations, want 49", count)
	}
	if err := store.Migrate(db.DB); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil || count != 49 {
		t.Fatalf("migration history after rerun: count=%d err=%v", count, err)
	}
}

func TestDeriveHealthStateFiveStates(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name     string
		overview domain.ChannelOverview
		want     string
	}{
		{"disabled", domain.ChannelOverview{Channel: domain.Channel{Status: domain.StatusDisabled}}, "disabled"},
		{"auto-disabled", domain.ChannelOverview{Channel: domain.Channel{Status: domain.StatusAutoDisabled}}, "unhealthy"},
		{"probe-failed", domain.ChannelOverview{Channel: domain.Channel{Status: domain.StatusEnabled}, LastProbeAt: &now, LastProbeOK: false, SiteUsable: true}, "unhealthy"},
		{"probe-ok-with-failures", domain.ChannelOverview{Channel: domain.Channel{Status: domain.StatusEnabled}, LastProbeOK: true, FailureCount: 3, SiteUsable: true}, "degraded"},
		{"probe-ok-with-cooling", domain.ChannelOverview{Channel: domain.Channel{Status: domain.StatusEnabled}, LastProbeOK: true, CoolingMemberCount: 1, SiteUsable: true}, "degraded"},
		{"probe-slow", domain.ChannelOverview{Channel: domain.Channel{Status: domain.StatusEnabled}, LastProbeOK: true, LastProbeError: "probe_slow", SiteUsable: true}, "degraded"},
		{"healthy", domain.ChannelOverview{Channel: domain.Channel{Status: domain.StatusEnabled}, LastProbeOK: true, SiteUsable: true}, "healthy"},
		{"unknown-no-probe", domain.ChannelOverview{Channel: domain.Channel{Status: domain.StatusEnabled}, SiteUsable: true}, "unknown"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := store.DeriveHealthState(tc.overview); got != tc.want {
				t.Fatalf("deriveHealthState=%q want %q", got, tc.want)
			}
		})
	}
}

func TestDeriveHealthReasonAndConnectivityAreIndependent(t *testing.T) {
	now := time.Now()
	overview := domain.ChannelOverview{
		Channel:           domain.Channel{Status: domain.StatusEnabled},
		LastProbeOK:       true,
		FailureCount:      2,
		CoolingMemberCount: 0,
	}
	if got := store.DeriveHealthState(overview); got != domain.HealthStateDegraded {
		t.Fatalf("health state=%q want degraded", got)
	}
	if got := store.DeriveHealthReason(overview); got != "route_failures" {
		t.Fatalf("health reason=%q want route_failures", got)
	}
	overview.LastProbeError = "probe_slow"
	if got := store.DeriveHealthReason(overview); got != "probe_slow" {
		t.Fatalf("slow probe reason=%q want probe_slow", got)
	}
	if got := store.DeriveConnectivityState(overview); got != domain.ConnectivityStateUnknown {
		t.Fatalf("connectivity without ping=%q want unknown", got)
	}

	overview.LastPingAt = &now
	overview.LastPingOK = true
	if got := store.DeriveConnectivityState(overview); got != domain.ConnectivityStateReachable {
		t.Fatalf("successful ping=%q want reachable", got)
	}
	overview.LastPingOK = false
	if got := store.DeriveConnectivityState(overview); got != domain.ConnectivityStateUnreachable {
		t.Fatalf("failed ping=%q want unreachable", got)
	}

	overview.LastProbeOK = false
	overview.LastProbeAt = &now
	overview.LastProbeError = "upstream_unauthorized"
	if got := store.DeriveHealthReason(overview); got != "authentication_failed" {
		t.Fatalf("auth probe reason=%q want authentication_failed", got)
	}
}

func TestSiteDisableCascadesChannels(t *testing.T) {
	db := openTestDB(t)
	siteID, err := db.Site.Create(&domain.Site{Name: "cascade", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	ch1, _ := db.Channel.Create(&domain.Channel{SiteID: &siteID, Name: "c1", Status: domain.StatusEnabled})
	ch2, _ := db.Channel.Create(&domain.Channel{SiteID: &siteID, Name: "c2", Status: domain.StatusEnabled})
	manualID, _ := db.Channel.Create(&domain.Channel{SiteID: &siteID, Name: "manual", Status: domain.StatusDisabled})
	otherSite, _ := db.Site.Create(&domain.Site{Name: "other", Status: domain.StatusEnabled})
	otherID, _ := db.Channel.Create(&domain.Channel{SiteID: &otherSite, Name: "other", Status: domain.StatusEnabled})

	site, _ := db.Site.GetByID(siteID)
	site.Status = domain.StatusDisabled
	if err := db.Site.Update(site); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{ch1, ch2} {
		ch, err := db.Channel.GetByID(id)
		if err != nil || ch.Status != domain.StatusDisabled {
			t.Fatalf("channel %d status=%s err=%v, want disabled", id, ch.Status, err)
		}
	}
	manual, _ := db.Channel.GetByID(manualID)
	if manual.Status != domain.StatusDisabled {
		t.Fatalf("already-disabled channel must stay disabled: %s", manual.Status)
	}
	other, _ := db.Channel.GetByID(otherID)
	if other.Status != domain.StatusEnabled {
		t.Fatalf("other site's channel must stay enabled: %s", other.Status)
	}
}

func TestSiteDeleteCascadesChannelsModelsAndEmptyRoutes(t *testing.T) {
	db := openTestDB(t)
	siteID, err := db.Site.Create(&domain.Site{
		Name: "owned", BaseURL: "https://owned.example", Platform: "openai-compatible", Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	credID, err := db.Credential.Create(&domain.Credential{
		SiteID: siteID, Kind: "api_key", SecretEnc: []byte("v1:cipher"), Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	channelID, err := db.Channel.Create(&domain.Channel{
		SiteID: &siteID, CredentialID: &credID, Name: "owned-ch", Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DiscoveredModel.Reconcile(t.Context(), store.ReconcileInput{
		ChannelID: channelID, Models: []string{"ghost-model"}, Source: "openai-compatible", CheckedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Site.Delete(siteID); err != nil {
		t.Fatalf("delete site: %v", err)
	}
	ch, err := db.Channel.GetByID(channelID)
	if err != nil || ch != nil {
		t.Fatalf("channel should cascade-delete: ch=%+v err=%v", ch, err)
	}
	models, err := db.DiscoveredModel.List(&channelID)
	if err != nil || len(models) != 0 {
		t.Fatalf("discovered models should cascade: %+v err=%v", models, err)
	}
	route, err := db.Route.GetByModel("ghost-model")
	if err != nil || route != nil {
		t.Fatalf("empty route should be cleaned: route=%+v err=%v", route, err)
	}
}

func TestChannelDeleteCleansEmptyRoutes(t *testing.T) {
	db := openTestDB(t)
	siteID, _ := db.Site.Create(&domain.Site{
		Name: "ch-del", BaseURL: "https://ch.example", Platform: "openai-compatible", Status: domain.StatusEnabled,
	})
	credID, _ := db.Credential.Create(&domain.Credential{
		SiteID: siteID, Kind: "api_key", SecretEnc: []byte("v1:cipher"), Status: domain.StatusEnabled,
	})
	channelID, err := db.Channel.Create(&domain.Channel{
		SiteID: &siteID, CredentialID: &credID, Name: "only", Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DiscoveredModel.Reconcile(t.Context(), store.ReconcileInput{
		ChannelID: channelID, Models: []string{"only-model"}, Source: "openai-compatible", CheckedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Channel.Delete(channelID); err != nil {
		t.Fatalf("delete channel: %v", err)
	}
	models, err := db.DiscoveredModel.List(&channelID)
	if err != nil || len(models) != 0 {
		t.Fatalf("models remain: %+v err=%v", models, err)
	}
	route, err := db.Route.GetByModel("only-model")
	if err != nil || route != nil {
		t.Fatalf("empty route remains: route=%+v err=%v", route, err)
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
	if changed.DeletedMembers != 0 {
		t.Fatalf("manual override was deleted: %+v", changed)
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

func TestDiscoveryReconcileRemovesAndRecreatesAutomaticMember(t *testing.T) {
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
	if err != nil || result.DeletedMembers != 1 || result.DeletedRoutes != 1 {
		t.Fatalf("empty reconcile: %+v err=%v", result, err)
	}
	if got, _ := db.RouteMember.GetByID(member.ID); got != nil {
		t.Fatalf("automatic member was not deleted: %+v", got)
	}
	if got, _ := db.Route.GetByModel("model-a"); got != nil {
		t.Fatalf("empty route was not deleted: %+v", got)
	}
	base.Models = []string{"model-a"}
	result, err = db.DiscoveredModel.Reconcile(t.Context(), base)
	if err != nil || result.CreatedRoutes != 1 || result.CreatedMembers != 1 {
		t.Fatalf("restore reconcile: %+v err=%v", result, err)
	}
	newRoute, _ := db.Route.GetByModel("model-a")
	if newRoute == nil {
		t.Fatal("route was not recreated")
	}
	restored, _ := db.RouteMember.ListByRoute(newRoute.ID)
	if len(restored) != 1 || !restored[0].Enabled || restored[0].Priority != 2 || restored[0].Weight != 10 {
		t.Fatalf("member was not recreated with channel defaults: %+v", restored)
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
	if result.DeletedMembers != 0 || !member.Enabled || member.Auto || member.Priority != 42 || member.Weight != 9 {
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

func TestRuntimeSettingsPreservesExplicitFalse(t *testing.T) {
	db := openTestDB(t)
	// Explicitly turning latency-aware routing OFF must survive a save/load
	// round trip. The old Save() mapped 0 → 1 ("default on"), silently
	// re-enabling the feature on the next process start.
	row := &store.RuntimeSettingsRow{
		HasOverride:            true,
		RoutingLatencyAware:    0,
		RoutingErrorAware:      1,
		RoutingConcurrencyLimit: 64,
	}
	if err := db.RuntimeSettings.Save(row); err != nil {
		t.Fatal(err)
	}
	got, err := db.RuntimeSettings.Get()
	if err != nil {
		t.Fatal(err)
	}
	if got.RoutingLatencyAware != 0 {
		t.Fatalf("latency aware explicit false lost: got %d want 0", got.RoutingLatencyAware)
	}
}

func TestRuntimeSettingsHealthSweepRoundTrip(t *testing.T) {
	db := openTestDB(t)
	row := &store.RuntimeSettingsRow{
		HasOverride:                  true,
		HealthSweepEnabled:           1,
		HealthSweepIntervalSeconds:   120,
		HealthSweepJitterSeconds:     15,
		HealthSweepDegradedMs:        1500,
		HealthSweepConcurrency:       6,
		HealthSweepTimeoutSeconds:    20,
		ChannelRetryTimes:            2,
		KeyPoolRotation:              0,
	}
	if err := db.RuntimeSettings.Save(row); err != nil {
		t.Fatal(err)
	}
	got, err := db.RuntimeSettings.Get()
	if err != nil {
		t.Fatal(err)
	}
	if !got.HasOverride || got.HealthSweepEnabled != 1 ||
		got.HealthSweepIntervalSeconds != 120 || got.HealthSweepJitterSeconds != 15 ||
		got.HealthSweepDegradedMs != 1500 || got.HealthSweepConcurrency != 6 ||
		got.HealthSweepTimeoutSeconds != 20 || got.ChannelRetryTimes != 2 || got.KeyPoolRotation != 0 {
		t.Fatalf("health sweep round trip mismatch: %+v", got)
	}
}

func TestRuntimeSettingsHealthSweepUnsetFollowsEnv(t *testing.T) {
	db := openTestDB(t)
	// A row saved before the health-sweep columns existed (NULL) must fall back
	// to the env bootstrap instead of zeroing the sweep.
	if _, err := db.Exec(`INSERT OR REPLACE INTO runtime_settings (id, has_override, updated_at) VALUES (1, 1, datetime('now'))`); err != nil {
		t.Fatal(err)
	}
	got, err := db.RuntimeSettings.Get()
	if err != nil {
		t.Fatal(err)
	}
	for name, v := range map[string]int{
		"enabled": got.HealthSweepEnabled, "interval": got.HealthSweepIntervalSeconds,
		"jitter": got.HealthSweepJitterSeconds, "degraded": got.HealthSweepDegradedMs,
		"concurrency": got.HealthSweepConcurrency, "timeout": got.HealthSweepTimeoutSeconds,
	} {
		if v != -1 {
			t.Fatalf("%s: unset column must read -1, got %d", name, v)
		}
	}
	if got.ChannelRetryTimes != -1 {
		t.Fatalf("channel_retry_times: unset column must read -1, got %d", got.ChannelRetryTimes)
	}
	if got.KeyPoolRotation != -1 {
		t.Fatalf("key_pool_rotation: unset column must read -1, got %d", got.KeyPoolRotation)
	}
}

func TestRouteRetryOverridesRoundTrip(t *testing.T) {
	db := openTestDB(t)
	// Route with overrides: retry 3 rounds, same-key re-send 2.
	retryTimes, channelRetry := 3, 2
	id, err := db.Route.Create(&domain.Route{
		ModelPattern:      "override-model",
		Enabled:           true,
		RetryTimes:        &retryTimes,
		ChannelRetryTimes: &channelRetry,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := db.Route.GetByID(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.RetryTimes == nil || *got.RetryTimes != 3 || got.ChannelRetryTimes == nil || *got.ChannelRetryTimes != 2 {
		t.Fatalf("route overrides lost: %+v", got)
	}
	// Update to nil must clear the override back to NULL (follow global).
	got.RetryTimes = nil
	got.ChannelRetryTimes = nil
	if err := db.Route.Update(got); err != nil {
		t.Fatal(err)
	}
	cleared, _ := db.Route.GetByID(id)
	if cleared.RetryTimes != nil || cleared.ChannelRetryTimes != nil {
		t.Fatalf("override clear failed: %+v", cleared)
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
	if err := db.RouteMember.RecordFailure(memberID, now, time.Minute, "transport"); err != nil {
		t.Fatal(err)
	}
	member, err = db.RouteMember.GetByID(memberID)
	if err != nil {
		t.Fatal(err)
	}
	want = now.Add(2 * time.Minute)
	if member.CooldownUntil == nil || !member.CooldownUntil.Equal(want) || member.FailCount != 2 {
		t.Fatalf("escalated cooldown: %+v", member)
	}
	// The third consecutive failure within the active backoff trips the circuit
	// breaker: the member is disabled outright instead of growing the cooldown.
	if err := db.RouteMember.RecordFailure(memberID, now, time.Minute, "transport"); err != nil {
		t.Fatal(err)
	}
	member, err = db.RouteMember.GetByID(memberID)
	if err != nil {
		t.Fatal(err)
	}
	if member.Enabled || member.CooldownUntil != nil || member.FailCount != 3 || member.LastError != "transport" {
		t.Fatalf("expected circuit breaker disable: %+v", member)
	}
	// The disabled member is excluded from routing until an admin clears its
	// automatic health state.
	route, candidates, err := db.RouteMember.RoutingCandidates("cooldown-model")
	if err != nil || route == nil || len(candidates) != 1 {
		t.Fatalf("candidates=%+v err=%v", candidates, err)
	}
	if candidates[0].Member.Enabled {
		t.Fatalf("disabled member still eligible: %+v", candidates[0])
	}
	if err := db.RouteMember.ClearHealth(memberID); err != nil {
		t.Fatal(err)
	}
	member, err = db.RouteMember.GetByID(memberID)
	if err != nil {
		t.Fatal(err)
	}
	if !member.Enabled || member.CooldownUntil != nil || member.FailCount != 0 || member.LastError != "" {
		t.Fatalf("cleared health: %+v", member)
	}
}

func TestRouteMemberBackoffCapsAtMax(t *testing.T) {
	db := openTestDB(t)
	routeID, _ := db.Route.Create(&domain.Route{ModelPattern: "cap-model", Enabled: true})
	channelID, _ := db.Channel.Create(&domain.Channel{Name: "channel", Status: domain.StatusEnabled})
	memberID, err := db.RouteMember.Create(&domain.RouteMember{RouteID: routeID, ChannelID: channelID, Enabled: true, Weight: 1})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC)
	// A base cool-down of 20h doubles to 40h on the second consecutive failure,
	// which must be capped at maxCooldownBackoff (24h) rather than overflowing.
	if err := db.RouteMember.RecordFailure(memberID, now, 20*time.Hour, "transport"); err != nil {
		t.Fatal(err)
	}
	if err := db.RouteMember.RecordFailure(memberID, now, 20*time.Hour, "transport"); err != nil {
		t.Fatal(err)
	}
	member, err := db.RouteMember.GetByID(memberID)
	if err != nil {
		t.Fatal(err)
	}
	want := now.Add(24 * time.Hour)
	if member.CooldownUntil == nil || !member.CooldownUntil.Equal(want) || member.FailCount != 2 {
		t.Fatalf("backoff not capped: %+v want until %v", member, want)
	}
}

func TestRouteMemberRecoverExpired(t *testing.T) {
	db := openTestDB(t)
	routeID, _ := db.Route.Create(&domain.Route{ModelPattern: "recover-model", Enabled: true})
	channelID, _ := db.Channel.Create(&domain.Channel{Name: "channel", Status: domain.StatusEnabled})
	memberID, err := db.RouteMember.Create(&domain.RouteMember{RouteID: routeID, ChannelID: channelID, Enabled: true, Weight: 1})
	if err != nil {
		t.Fatal(err)
	}
	// Fail with a cooldown that has already expired (past timestamp).
	past := time.Now().UTC().Add(-10 * time.Minute)
	if err := db.RouteMember.RecordFailure(memberID, past, time.Minute, "transport"); err != nil {
		t.Fatal(err)
	}
	member, err := db.RouteMember.GetByID(memberID)
	if err != nil {
		t.Fatal(err)
	}
	if member.FailCount != 1 || member.CooldownUntil == nil {
		t.Fatalf("expected recorded failure, got %+v", member)
	}
	// RecoverExpired clears the active health state after the penalty ends.
	if err := db.RouteMember.RecoverExpired(); err != nil {
		t.Fatal(err)
	}
	member, err = db.RouteMember.GetByID(memberID)
	if err != nil {
		t.Fatal(err)
	}
	if member.CooldownUntil != nil || member.FailCount != 0 || member.LastError != "" {
		t.Fatalf("expired cooldown not recovered: %+v", member)
	}
	// A future cooldown must NOT be cleared.
	future := time.Now().UTC().Add(5 * time.Minute)
	if err := db.RouteMember.RecordFailure(memberID, future, time.Minute, "transport"); err != nil {
		t.Fatal(err)
	}
	if err := db.RouteMember.RecoverExpired(); err != nil {
		t.Fatal(err)
	}
	member, err = db.RouteMember.GetByID(memberID)
	if err != nil {
		t.Fatal(err)
	}
	if member.CooldownUntil == nil || member.FailCount != 1 {
		t.Fatalf("future cooldown must survive, got %+v", member)
	}

	// A separate member with an expired prior cooldown starts a fresh base cycle.
	resetChannelID, _ := db.Channel.Create(&domain.Channel{Name: "reset-channel", Status: domain.StatusEnabled})
	resetMemberID, err := db.RouteMember.Create(&domain.RouteMember{RouteID: routeID, ChannelID: resetChannelID, Enabled: true, Weight: 1})
	if err != nil {
		t.Fatal(err)
	}
	secondPast := time.Now().UTC().Add(-10 * time.Minute)
	if err := db.RouteMember.RecordFailure(resetMemberID, secondPast, time.Minute, "transport"); err != nil {
		t.Fatal(err)
	}
	secondNow := time.Now().UTC()
	if err := db.RouteMember.RecordFailure(resetMemberID, secondNow, time.Minute, "transport"); err != nil {
		t.Fatal(err)
	}
	resetMember, err := db.RouteMember.GetByID(resetMemberID)
	if err != nil {
		t.Fatal(err)
	}
	if resetMember.FailCount != 1 || resetMember.CooldownUntil == nil || !resetMember.CooldownUntil.Equal(secondNow.Add(time.Minute)) {
		t.Fatalf("expired failure cycle did not reset backoff: %+v", resetMember)
	}
}

func TestChannelUpdatePropagatesDefaultsOnlyToAutomaticMembers(t *testing.T) {
	db := openTestDB(t)
	channelID, err := db.Channel.Create(&domain.Channel{Name: "defaults", Priority: 1, Weight: 10, Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DiscoveredModel.Reconcile(t.Context(), store.ReconcileInput{
		ChannelID: channelID,
		Models:    []string{"auto-model"},
		CheckedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	autoRoute, _ := db.Route.GetByModel("auto-model")
	autoMembers, _ := db.RouteMember.ListByRoute(autoRoute.ID)

	manualRouteID, _ := db.Route.Create(&domain.Route{ModelPattern: "manual-model", Enabled: true})
	manualID, err := db.RouteMember.Create(&domain.RouteMember{
		RouteID: manualRouteID, ChannelID: channelID, Priority: 77, Weight: 7,
		Enabled: true, Auto: false, ManualOverride: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	channel, _ := db.Channel.GetByID(channelID)
	channel.Priority = 9
	channel.Weight = 90
	if err := db.Channel.Update(channel); err != nil {
		t.Fatal(err)
	}
	automatic, _ := db.RouteMember.GetByID(autoMembers[0].ID)
	manual, _ := db.RouteMember.GetByID(manualID)
	if automatic.Priority != 9 || automatic.Weight != 90 {
		t.Fatalf("automatic member did not inherit defaults: %+v", automatic)
	}
	if manual.Priority != 77 || manual.Weight != 7 {
		t.Fatalf("manual member changed: %+v", manual)
	}
}

func TestProxyLogListFilter(t *testing.T) {
	db := openTestDB(t)

	siteA, err := db.Site.Create(&domain.Site{
		Name: "site-a", BaseURL: "https://a.example.com", Platform: "openai-compatible", Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatalf("site a: %v", err)
	}
	siteB, err := db.Site.Create(&domain.Site{
		Name: "site-b", BaseURL: "https://b.example.com", Platform: "openai-compatible", Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatalf("site b: %v", err)
	}
	chA, err := db.Channel.Create(&domain.Channel{
		SiteID: &siteA, Name: "ch-a", Status: domain.StatusEnabled, Weight: 100,
	})
	if err != nil {
		t.Fatalf("channel a: %v", err)
	}
	chB, err := db.Channel.Create(&domain.Channel{
		SiteID: &siteB, Name: "ch-b", Status: domain.StatusEnabled, Weight: 100,
	})
	if err != nil {
		t.Fatalf("channel b: %v", err)
	}

	insert := func(requestID string, channelID int64, model string, status int) int64 {
		t.Helper()
		id, err := db.ProxyLog.Insert(&domain.ProxyLog{
			RequestID: requestID, ChannelID: channelID, Model: model,
			Status: status, LatencyMs: 10, Attempt: 1, ErrorBrief: "",
		})
		if err != nil {
			t.Fatalf("insert %s: %v", requestID, err)
		}
		return id
	}
	id1 := insert("req-a-ok", chA, "gpt-a", 200)
	id2 := insert("req-a-fail", chA, "gpt-a", 502)
	id3 := insert("req-b-ok", chB, "gpt-b", 200)
	id4 := insert("req-b-fail", chB, "gpt-other", 500)
	_ = id1

	mustIDs := func(logs []domain.ProxyLog) []int64 {
		out := make([]int64, len(logs))
		for i, l := range logs {
			out[i] = l.ID
		}
		return out
	}
	containsOnly := func(got []int64, want ...int64) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("len got=%v want=%v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("order/content got=%v want=%v", got, want)
			}
		}
	}

	// Default List still returns newest first, capped.
	all, err := db.ProxyLog.List(10)
	if err != nil {
		t.Fatal(err)
	}
	containsOnly(mustIDs(all), id4, id3, id2, id1)

	// Site filter via channel join.
	siteLogs, err := db.ProxyLog.ListFilter(store.ProxyLogFilter{SiteID: &siteA, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	containsOnly(mustIDs(siteLogs), id2, id1)

	// Channel filter.
	chLogs, err := db.ProxyLog.ListFilter(store.ProxyLogFilter{ChannelID: &chB, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	containsOnly(mustIDs(chLogs), id4, id3)

	// Exact model filter.
	modelLogs, err := db.ProxyLog.ListFilter(store.ProxyLogFilter{Model: "gpt-a", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	containsOnly(mustIDs(modelLogs), id2, id1)

	// Failed-only (status >= 400).
	failed, err := db.ProxyLog.ListFilter(store.ProxyLogFilter{FailedOnly: true, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	containsOnly(mustIDs(failed), id4, id2)

	// Exact status.
	status502 := 502
	exact, err := db.ProxyLog.ListFilter(store.ProxyLogFilter{Status: &status502, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	containsOnly(mustIDs(exact), id2)

	// before_id pagination (newest-first cursor).
	page1, err := db.ProxyLog.ListFilter(store.ProxyLogFilter{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	containsOnly(mustIDs(page1), id4, id3)
	page2, err := db.ProxyLog.ListFilter(store.ProxyLogFilter{BeforeID: &page1[len(page1)-1].ID, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	containsOnly(mustIDs(page2), id2, id1)

	// Route association: a log with route_id surfaces the route pattern.
	routeID, err := db.Route.Create(&domain.Route{ModelPattern: "gpt-route-1", Enabled: true})
	if err != nil {
		t.Fatalf("route create: %v", err)
	}
	idRouted, err := db.ProxyLog.Insert(&domain.ProxyLog{
		RequestID: "req-routed", ChannelID: chA, RouteID: routeID, Model: "gpt-a",
		Status: 200, LatencyMs: 5, Attempt: 1,
	})
	if err != nil {
		t.Fatalf("routed insert: %v", err)
	}
	routed, err := db.ProxyLog.ListFilter(store.ProxyLogFilter{Model: "gpt-a", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var found *domain.ProxyLog
	for i := range routed {
		if routed[i].ID == idRouted {
			found = &routed[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("routed log not returned")
	}
	if found.RouteID != routeID || found.RoutePattern != "gpt-route-1" {
		t.Fatalf("route join got route_id=%d pattern=%q, want %d / gpt-route-1", found.RouteID, found.RoutePattern, routeID)
	}
	// Unrouted logs keep an empty pattern (not NULL).
	unrouted, err := db.ProxyLog.ListFilter(store.ProxyLogFilter{Model: "gpt-b", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(unrouted) > 0 && unrouted[0].RoutePattern != "" {
		t.Fatalf("unrouted log got pattern %q, want empty", unrouted[0].RoutePattern)
	}

	// Site + channel AND: channel on other site yields empty.
	empty, err := db.ProxyLog.ListFilter(store.ProxyLogFilter{SiteID: &siteA, ChannelID: &chB, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 {
		t.Fatalf("expected empty site/channel AND, got %v", mustIDs(empty))
	}
}

// TestCacheTokenAccounting verifies cache-read/creation tokens flow through
// ProxyLog insert + token update and Usage insert + list.
func TestCacheTokenAccounting(t *testing.T) {
	db := openTestDB(t)

	ch, err := db.Channel.Create(&domain.Channel{Name: "ch-cache", Status: domain.StatusEnabled, Weight: 100})
	if err != nil {
		t.Fatalf("channel: %v", err)
	}

	// ProxyLog: insert then update tokens with cache detail.
	logID, err := db.ProxyLog.Insert(&domain.ProxyLog{
		RequestID: "req-cache", ChannelID: ch, Model: "claude-x", Status: 200,
		LatencyMs: 10, Attempt: 1,
	})
	if err != nil {
		t.Fatalf("log insert: %v", err)
	}
	if err := db.ProxyLog.UpdateTokensByRequestID("req-cache", 100, 50, 150, 40, 20); err != nil {
		t.Fatalf("log update: %v", err)
	}
	logs, err := db.ProxyLog.List(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 || logs[0].ID != logID {
		t.Fatalf("log list: %+v", logs)
	}
	log := logs[0]
	if log.PromptTokens != 100 || log.CompletionTokens != 50 || log.TotalTokens != 150 {
		t.Fatalf("log tokens: %+v", log)
	}
	if log.CacheReadTokens != 40 || log.CacheCreationTokens != 20 {
		t.Fatalf("log cache tokens: %+v", log)
	}

	// UsageRecord: insert with cache detail and read it back.
	if _, err := db.Usage.Insert(&domain.UsageRecord{
		RequestID: "req-cache", DownstreamKeyID: 7, ChannelID: ch, Model: "claude-x",
		Path: "chat/completions", Stream: true,
		PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150,
		CacheReadTokens: 40, CacheCreationTokens: 20,
		Status: 200,
	}); err != nil {
		t.Fatalf("usage insert: %v", err)
	}
	usage, err := db.Usage.List(store.UsageFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(usage) != 1 {
		t.Fatalf("usage list: %+v", usage)
	}
	if usage[0].CacheReadTokens != 40 || usage[0].CacheCreationTokens != 20 {
		t.Fatalf("usage cache tokens: %+v", usage[0])
	}
}

// TestProgressiveCooldownTiers verifies tiered backoff: fail 2 → levels[0],
// fail 3 → levels[1], fail 4 → levels[2], and the configurable breaker
// threshold parks the member once reached.
func TestProgressiveCooldownTiers(t *testing.T) {
	db := openTestDB(t)
	db.RouteMember.SetProgressiveCooldown(true, 30*time.Second,
		[3]time.Duration{10 * time.Minute, time.Hour, 24 * time.Hour}, 5)

	routeID, _ := db.Route.Create(&domain.Route{ModelPattern: "tiered", Enabled: true})
	channelID, _ := db.Channel.Create(&domain.Channel{Name: "tiered-ch", Status: domain.StatusEnabled})
	memberID, err := db.RouteMember.Create(&domain.RouteMember{RouteID: routeID, ChannelID: channelID, Enabled: true, Weight: 1})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC)
	base := 30 * time.Second

	cooldownFor := func() time.Duration {
		t.Helper()
		member, err := db.RouteMember.GetByID(memberID)
		if err != nil {
			t.Fatal(err)
		}
		if member.CooldownUntil == nil {
			t.Fatalf("no cooldown set, fail_count=%d", member.FailCount)
		}
		return member.CooldownUntil.Sub(now)
	}

	if err := db.RouteMember.RecordFailure(memberID, now, base, "transport"); err != nil {
		t.Fatal(err)
	}
	if got := cooldownFor(); got != base {
		t.Fatalf("fail 1 cooldown = %v, want %v", got, base)
	}
	if err := db.RouteMember.RecordFailure(memberID, now, base, "transport"); err != nil {
		t.Fatal(err)
	}
	if got := cooldownFor(); got != 10*time.Minute {
		t.Fatalf("fail 2 cooldown = %v, want 10m", got)
	}
	if err := db.RouteMember.RecordFailure(memberID, now, base, "transport"); err != nil {
		t.Fatal(err)
	}
	if got := cooldownFor(); got != time.Hour {
		t.Fatalf("fail 3 cooldown = %v, want 1h", got)
	}
	if err := db.RouteMember.RecordFailure(memberID, now, base, "transport"); err != nil {
		t.Fatal(err)
	}
	if got := cooldownFor(); got != 24*time.Hour {
		t.Fatalf("fail 4 cooldown = %v, want 24h", got)
	}

	// Fifth consecutive failure trips the breaker (threshold 5): disabled.
	if err := db.RouteMember.RecordFailure(memberID, now, base, "transport"); err != nil {
		t.Fatal(err)
	}
	member, err := db.RouteMember.GetByID(memberID)
	if err != nil {
		t.Fatal(err)
	}
	if member.Enabled || member.CooldownUntil != nil {
		t.Fatalf("breaker did not park member: %+v", member)
	}
}

// TestProgressiveCooldownSuccessDecay verifies one-tier-per-success recovery:
// a member with a 24h penalty steps down to 1h after one success, then 10m,
// then base, then fully clear — never skipping tiers.
func TestProgressiveCooldownSuccessDecay(t *testing.T) {
	db := openTestDB(t)
	db.RouteMember.SetProgressiveCooldown(true, 30*time.Second,
		[3]time.Duration{10 * time.Minute, time.Hour, 24 * time.Hour}, 5)

	routeID, _ := db.Route.Create(&domain.Route{ModelPattern: "decay", Enabled: true})
	channelID, _ := db.Channel.Create(&domain.Channel{Name: "decay-ch", Status: domain.StatusEnabled})
	memberID, err := db.RouteMember.Create(&domain.RouteMember{RouteID: routeID, ChannelID: channelID, Enabled: true, Weight: 1})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC)

	// Reach fail_count 4 (24h tier).
	for i := 0; i < 4; i++ {
		if err := db.RouteMember.RecordFailure(memberID, now, 30*time.Second, "transport"); err != nil {
			t.Fatal(err)
		}
	}

	cooldownFor := func() time.Duration {
		t.Helper()
		member, err := db.RouteMember.GetByID(memberID)
		if err != nil {
			t.Fatal(err)
		}
		if member.CooldownUntil == nil {
			t.Fatalf("no cooldown, fail_count=%d", member.FailCount)
		}
		return member.CooldownUntil.Sub(now)
	}

	// Success 1: 24h → 1h.
	if err := db.RouteMember.RecordSuccess(memberID, now); err != nil {
		t.Fatal(err)
	}
	if got := cooldownFor(); got != time.Hour {
		t.Fatalf("after 1 success cooldown = %v, want 1h", got)
	}
	// Success 2: 1h → 10m.
	if err := db.RouteMember.RecordSuccess(memberID, now); err != nil {
		t.Fatal(err)
	}
	if got := cooldownFor(); got != 10*time.Minute {
		t.Fatalf("after 2 successes cooldown = %v, want 10m", got)
	}
	// Success 3: 10m → base (30s).
	if err := db.RouteMember.RecordSuccess(memberID, now); err != nil {
		t.Fatal(err)
	}
	if got := cooldownFor(); got != 30*time.Second {
		t.Fatalf("after 3 successes cooldown = %v, want 30s", got)
	}
	// Success 4: fully clear.
	if err := db.RouteMember.RecordSuccess(memberID, now); err != nil {
		t.Fatal(err)
	}
	member, err := db.RouteMember.GetByID(memberID)
	if err != nil {
		t.Fatal(err)
	}
	if member.FailCount != 0 || member.CooldownUntil != nil {
		t.Fatalf("not fully recovered: %+v", member)
	}
}

// TestProgressiveCooldownDisabledRestoresExponentialBackoff verifies that
// disabling progressive mode restores the legacy 2^n behavior exactly.
func TestProgressiveCooldownDisabledRestoresExponentialBackoff(t *testing.T) {
	db := openTestDB(t)
	db.RouteMember.SetProgressiveCooldown(true, 30*time.Second, [3]time.Duration{10 * time.Minute, time.Hour, 24 * time.Hour}, 5)
	db.RouteMember.SetProgressiveCooldown(false, 0, [3]time.Duration{}, 0)

	routeID, _ := db.Route.Create(&domain.Route{ModelPattern: "legacy", Enabled: true})
	channelID, _ := db.Channel.Create(&domain.Channel{Name: "legacy-ch", Status: domain.StatusEnabled})
	memberID, err := db.RouteMember.Create(&domain.RouteMember{RouteID: routeID, ChannelID: channelID, Enabled: true, Weight: 1})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC)
	if err := db.RouteMember.RecordFailure(memberID, now, time.Minute, "transport"); err != nil {
		t.Fatal(err)
	}
	if err := db.RouteMember.RecordFailure(memberID, now, time.Minute, "transport"); err != nil {
		t.Fatal(err)
	}
	member, err := db.RouteMember.GetByID(memberID)
	if err != nil {
		t.Fatal(err)
	}
	// Legacy exponential: 1m → 2m.
	if member.FailCount != 2 || member.CooldownUntil == nil || !member.CooldownUntil.Equal(now.Add(2*time.Minute)) {
		t.Fatalf("legacy backoff broken: %+v", member)
	}
}

// TestProxyLogObservabilityMeta verifies first-byte latency and client family
// attach to the newest log row after a relay.
func TestProxyLogObservabilityMeta(t *testing.T) {
	db := openTestDB(t)
	ch, err := db.Channel.Create(&domain.Channel{Name: "ch-meta", Status: domain.StatusEnabled, Weight: 100})
	if err != nil {
		t.Fatalf("channel: %v", err)
	}
	if _, err := db.ProxyLog.Insert(&domain.ProxyLog{
		RequestID: "req-meta", ChannelID: ch, Model: "m", Status: 200,
		LatencyMs: 10, Attempt: 1, Stream: true,
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := db.ProxyLog.UpdateMetaByRequestID("req-meta", 42, "cli"); err != nil {
		t.Fatalf("update meta: %v", err)
	}
	logs, err := db.ProxyLog.List(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 || logs[0].FirstByteMs != 42 || logs[0].ClientFamily != "cli" {
		t.Fatalf("observability meta not persisted: %+v", logs)
	}
}

func TestChannelStableFirstLifecycle(t *testing.T) {
	db := openTestDB(t)
	siteID, _ := db.Site.Create(&domain.Site{Name: "gray", BaseURL: "https://gray.example", Platform: "openai-compatible", Status: domain.StatusEnabled})
	credID, _ := db.Credential.Create(&domain.Credential{SiteID: siteID, Kind: "api_key", SecretEnc: []byte("v1:cipher"), Status: domain.StatusEnabled})

	id, err := db.Channel.Create(&domain.Channel{SiteID: &siteID, CredentialID: &credID, Name: "gray", Status: domain.StatusEnabled, StableFirst: true})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := db.Channel.GetByID(id)
	if err != nil || ch == nil || !ch.StableFirst {
		t.Fatalf("stable_first not persisted: %+v err=%v", ch, err)
	}

	// Successes count toward promotion; promote after 3 with no failures.
	promoted, err := db.Channel.RecordGraySuccess(id, 3)
	if err != nil {
		t.Fatal(err)
	}
	if promoted {
		t.Fatal("promoted too early")
	}
	promoted, _ = db.Channel.RecordGraySuccess(id, 3)
	if promoted {
		t.Fatal("promoted too early (2)")
	}
	promoted, err = db.Channel.RecordGraySuccess(id, 3)
	if err != nil || !promoted {
		t.Fatalf("expected promotion on 3rd success: promoted=%v err=%v", promoted, err)
	}
	ch, _ = db.Channel.GetByID(id)
	if ch.StableFirst || ch.StableFirstRequests != 0 {
		t.Fatalf("promotion did not clear mark: %+v", ch)
	}
}

func TestChannelStableFirstFailureBlocksPromotion(t *testing.T) {
	db := openTestDB(t)
	siteID, _ := db.Site.Create(&domain.Site{Name: "gray2", BaseURL: "https://gray2.example", Platform: "openai-compatible", Status: domain.StatusEnabled})
	credID, _ := db.Credential.Create(&domain.Credential{SiteID: siteID, Kind: "api_key", SecretEnc: []byte("v1:cipher"), Status: domain.StatusEnabled})
	id, _ := db.Channel.Create(&domain.Channel{SiteID: &siteID, CredentialID: &credID, Name: "gray2", Status: domain.StatusEnabled, StableFirst: true})

	// One failure keeps consecutive_failures at 1; successes reach the count
	// but promotion is withheld until failures clear.
	_, _ = db.Channel.RecordRelayFailure(id)
	for i := 0; i < 5; i++ {
		if promoted, err := db.Channel.RecordGraySuccess(id, 3); err != nil || promoted {
			t.Fatalf("promotion with pending failure: promoted=%v err=%v", promoted, err)
		}
	}
	_ = db.Channel.RecordRelaySuccess(id)
	promoted, err := db.Channel.RecordGraySuccess(id, 3)
	if err != nil || !promoted {
		t.Fatalf("expected promotion after failure cleared: promoted=%v err=%v", promoted, err)
	}
}

func TestChannelStableFirstNonGrayNoop(t *testing.T) {
	db := openTestDB(t)
	siteID, _ := db.Site.Create(&domain.Site{Name: "plain", BaseURL: "https://plain.example", Platform: "openai-compatible", Status: domain.StatusEnabled})
	credID, _ := db.Credential.Create(&domain.Credential{SiteID: siteID, Kind: "api_key", SecretEnc: []byte("v1:cipher"), Status: domain.StatusEnabled})
	id, _ := db.Channel.Create(&domain.Channel{SiteID: &siteID, CredentialID: &credID, Name: "plain", Status: domain.StatusEnabled})

	promoted, err := db.Channel.RecordGraySuccess(id, 3)
	if err != nil || promoted {
		t.Fatalf("non-gray channel must no-op: promoted=%v err=%v", promoted, err)
	}
}

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
	if count != 2 {
		t.Fatalf("got %d applied migrations, want 2", count)
	}
	if err := store.Migrate(db.DB); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("migration history after rerun: count=%d err=%v", count, err)
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

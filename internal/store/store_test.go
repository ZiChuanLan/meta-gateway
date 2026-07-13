package store_test

import (
	"path/filepath"
	"testing"

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

package store_test

import (
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

// newAutoMatchChannel creates a channel that advertises modelsCSV; returns
// the channel id.
func newAutoMatchChannel(t *testing.T, db *store.DB, name, modelsCSV, status, syncMode string) int64 {
	t.Helper()
	siteID, err := db.Site.Create(&domain.Site{Name: name, BaseURL: "https://api.example.com", Platform: "openai-compatible", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	credID, err := db.Credential.Create(&domain.Credential{SiteID: siteID, Kind: "api_key", SecretEnc: []byte("enc"), Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	id, err := db.Channel.Create(&domain.Channel{
		SiteID: &siteID, CredentialID: credIDPtr(credID), Name: name,
		BaseURL: "https://api.example.com", TypeHint: "openai-compatible",
		Status: status, ModelsCSV: modelsCSV,
		ModelSyncMode: syncMode,
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func memberChannelIDs(t *testing.T, db *store.DB, routeID int64) map[int64]domain.RouteMember {
	t.Helper()
	members, err := db.RouteMember.ListByRoute(routeID)
	if err != nil {
		t.Fatal(err)
	}
	out := map[int64]domain.RouteMember{}
	for _, member := range members {
		out[member.ChannelID] = member
	}
	return out
}

func TestChannelsWithModel(t *testing.T) {
	db := openTestDB(t)

	csv := newAutoMatchChannel(t, db, "csv-ch", "gpt-4o,deepseek-v4-flash", domain.StatusEnabled, domain.ModelSyncModeAuto)
	disc := newAutoMatchChannel(t, db, "disc-ch", "", domain.StatusEnabled, domain.ModelSyncModeAuto)
	manual := newAutoMatchChannel(t, db, "manual-ch", "deepseek-v4-flash", domain.StatusEnabled, domain.ModelSyncModeManual)
	newAutoMatchChannel(t, db, "off-ch", "deepseek-v4-flash", domain.StatusDisabled, domain.ModelSyncModeAuto)
	// Discovery snapshot for disc-ch (inserted directly to bypass Reconcile).
	if _, err := db.Exec(`INSERT INTO discovered_models (channel_id, model_name, available, source, latency_ms, checked_at) VALUES (?, 'deepseek-v4-flash', 1, 'test', 0, datetime('now'))`, disc); err != nil {
		t.Fatal(err)
	}

	matches, err := db.ChannelsWithModel("deepseek-v4-flash")
	if err != nil {
		t.Fatal(err)
	}
	byID := map[int64]store.ModelChannelMatch{}
	for _, match := range matches {
		byID[match.ChannelID] = match
	}
	if len(matches) != 3 {
		t.Fatalf("matches = %+v, want csv+discovered+manual channels", matches)
	}
	if byID[csv].Source != "models_csv" || byID[disc].Source != "discovered" || byID[manual].Source != "models_csv" {
		t.Fatalf("sources = %+v", byID)
	}

	// Wildcard patterns reuse routing semantics.
	wild, err := db.ChannelsWithModel("deepseek-*")
	if err != nil {
		t.Fatal(err)
	}
	if len(wild) != 3 {
		t.Fatalf("wildcard matches = %+v, want the same 3 channels", wild)
	}

	// No match, empty pattern.
	none, err := db.ChannelsWithModel("gpt-5-nobody-has-it")
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Fatalf("none = %+v", none)
	}
	empty, err := db.ChannelsWithModel("  ")
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 {
		t.Fatalf("empty = %+v", empty)
	}
}

func TestCreateRouteWithAutoMatch(t *testing.T) {
	db := openTestDB(t)

	first := newAutoMatchChannel(t, db, "ch-1", "deepseek-v4-flash", domain.StatusEnabled, domain.ModelSyncModeAuto)
	second := newAutoMatchChannel(t, db, "ch-2", "", domain.StatusEnabled, domain.ModelSyncModeAuto)
	newAutoMatchChannel(t, db, "ch-off", "deepseek-v4-flash", domain.StatusDisabled, domain.ModelSyncModeAuto)
	if _, err := db.Exec(`INSERT INTO discovered_models (channel_id, model_name, available, source, latency_ms, checked_at) VALUES (?, 'deepseek-v4-flash', 1, 'test', 0, datetime('now'))`, second); err != nil {
		t.Fatal(err)
	}

	routeID, attached, err := db.CreateRouteWithAutoMatch(
		&domain.Route{ModelPattern: "deepseek-v4-flash", Enabled: true},
		[]int64{first, second},
	)
	if err != nil {
		t.Fatal(err)
	}
	if attached != 2 {
		t.Fatalf("attached = %d, want 2", attached)
	}
	members := memberChannelIDs(t, db, routeID)
	if len(members) != 2 {
		t.Fatalf("members = %+v, want channels %d and %d", members, first, second)
	}
	for _, id := range []int64{first, second} {
		member, ok := members[id]
		if !ok {
			t.Fatalf("channel %d not attached", id)
		}
		if !member.Enabled || !member.Auto || member.ManualOverride || member.Priority != 0 || member.Weight != 100 {
			t.Fatalf("member = %+v, want enabled auto member with default priority/weight", member)
		}
	}

	// The ids are intersected with the match set: an unknown id never becomes
	// a member, and only the selected channel is attached.
	partialID, partialAttached, err := db.CreateRouteWithAutoMatch(
		&domain.Route{ModelPattern: "deepseek-v4-*", Enabled: true},
		[]int64{first, 99999},
	)
	if err != nil {
		t.Fatal(err)
	}
	if partialAttached != 1 {
		t.Fatalf("partial attached = %d, want 1", partialAttached)
	}
	if partialMembers := memberChannelIDs(t, db, partialID); len(partialMembers) != 1 {
		t.Fatalf("partial members = %+v", partialMembers)
	}

	// Re-running with the same pattern reuses the route: already-attached
	// channels are not duplicated, and no second route appears.
	routesBefore, err := db.Route.List()
	if err != nil {
		t.Fatal(err)
	}
	reusedID, attached, err := db.CreateRouteWithAutoMatch(
		&domain.Route{ModelPattern: "deepseek-v4-flash", Enabled: true},
		[]int64{first},
	)
	if err != nil {
		t.Fatal(err)
	}
	if reusedID != routeID || attached != 0 {
		t.Fatalf("reuse = (id %d, attached %d), want (id %d, 0)", reusedID, attached, routeID)
	}
	routesAfter, err := db.Route.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(routesAfter) != len(routesBefore) {
		t.Fatalf("route count changed: %d -> %d", len(routesBefore), len(routesAfter))
	}

	// Without ids the route create stays strict (duplicate pattern fails).
	bareID, attached, err := db.CreateRouteWithAutoMatch(&domain.Route{ModelPattern: "other-model", Enabled: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if attached != 0 {
		t.Fatalf("bare attached = %d, want 0", attached)
	}
	if members := memberChannelIDs(t, db, bareID); len(members) != 0 {
		t.Fatalf("bare members = %+v", members)
	}
}

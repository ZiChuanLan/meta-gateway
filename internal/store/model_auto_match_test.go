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
	out := map[int64]domain.RouteMember{}
	for _, member := range mustListMembers(t, db, routeID) {
		out[member.ChannelID] = member
	}
	return out
}

func mustListMembers(t *testing.T, db *store.DB, routeID int64) []domain.RouteMember {
	t.Helper()
	members, err := db.RouteMember.ListByRoute(routeID)
	if err != nil {
		t.Fatal(err)
	}
	return members
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
		store.ModelMatchExact,
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
		store.ModelMatchExact,
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
		store.ModelMatchExact,
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
	bareID, attached, err := db.CreateRouteWithAutoMatch(&domain.Route{ModelPattern: "other-model", Enabled: true}, nil, store.ModelMatchExact)
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

// TestAttachChannelsToRoute covers the console's "add every channel that serves
// this model" action on a route that already exists.
func TestAttachChannelsToRoute(t *testing.T) {
	db := openTestDB(t)

	first := newAutoMatchChannel(t, db, "ch-1", "deepseek-v4-flash", domain.StatusEnabled, domain.ModelSyncModeAuto)
	second := newAutoMatchChannel(t, db, "ch-2", "deepseek-v4-flash,gpt-4o", domain.StatusEnabled, domain.ModelSyncModeManual)
	offline := newAutoMatchChannel(t, db, "ch-off", "deepseek-v4-flash", domain.StatusDisabled, domain.ModelSyncModeAuto)

	routeID, err := db.Route.Create(&domain.Route{ModelPattern: "deepseek-v4-flash", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	// An unknown id is not a match, and a disabled channel is not a match: both
	// are counted as skipped rather than silently worked around.
	added, skipped, err := db.AttachChannelsToRoute(routeID, "deepseek-v4-flash", store.ModelMatchExact, []int64{first, second, offline, 99999}, "")
	if err != nil {
		t.Fatal(err)
	}
	if added != 2 || skipped != 2 {
		t.Fatalf("attach = (added %d, skipped %d), want (2, 2)", added, skipped)
	}
	members := memberChannelIDs(t, db, routeID)
	if len(members) != 2 {
		t.Fatalf("members = %+v, want the two enabled channels", members)
	}
	for _, id := range []int64{first, second} {
		member, ok := members[id]
		if !ok {
			t.Fatalf("channel %d not attached", id)
		}
		if !member.Enabled || !member.Auto || member.GroupName != domain.DefaultRouteGroup {
			t.Fatalf("member = %+v, want an enabled auto member in the default group", member)
		}
	}
	if _, ok := members[offline]; ok {
		t.Fatal("disabled channel must not be attached")
	}

	// Idempotent: they are already in the default group, so a second run is a
	// plain no-op — not a "skip", which is reserved for ids the match set
	// refused (disabled, unknown, or no longer serving the model).
	added, skipped, err = db.AttachChannelsToRoute(routeID, "deepseek-v4-flash", store.ModelMatchExact, []int64{first, second}, "")
	if err != nil {
		t.Fatal(err)
	}
	if added != 0 || skipped != 0 {
		t.Fatalf("re-attach = (added %d, skipped %d), want (0, 0)", added, skipped)
	}

	// A channel that only lives in another group still gets the default
	// membership: groups are additive, so an API key bound to `default` must
	// not silently miss a channel that another group already reaches.
	grouped := newAutoMatchChannel(t, db, "ch-grouped", "deepseek-v4-flash", domain.StatusEnabled, domain.ModelSyncModeAuto)
	if _, err := db.RouteMember.Create(&domain.RouteMember{
		RouteID: routeID, ChannelID: grouped, GroupName: "blue", Enabled: true, Weight: 100,
	}); err != nil {
		t.Fatal(err)
	}
	added, skipped, err = db.AttachChannelsToRoute(routeID, "deepseek-v4-flash", store.ModelMatchExact, []int64{grouped}, "")
	if err != nil {
		t.Fatal(err)
	}
	if added != 1 || skipped != 0 {
		t.Fatalf("grouped attach = (added %d, skipped %d), want (1, 0)", added, skipped)
	}

	// The target group is a parameter: attaching the same channels into `blue`
	// is the same routes, one group over, and must create a second membership
	// rather than being absorbed by the default-group hit. (`grouped` already
	// lives in blue from the manual insert above, so it is now the no-op case.)
	added, skipped, err = db.AttachChannelsToRoute(routeID, "deepseek-v4-flash", store.ModelMatchExact, []int64{first, second, grouped}, "blue")
	if err != nil {
		t.Fatal(err)
	}
	if added != 2 || skipped != 0 {
		t.Fatalf("blue attach = (added %d, skipped %d), want (2, 0)", added, skipped)
	}
	blueMembers := 0
	for _, member := range mustListMembers(t, db, routeID) {
		if member.GroupName == "blue" {
			blueMembers++
		}
	}
	if blueMembers != 3 {
		t.Fatalf("blue members = %d, want all three channels", blueMembers)
	}
	added, skipped, err = db.AttachChannelsToRoute(routeID, "deepseek-v4-flash", store.ModelMatchExact, []int64{first, second, grouped}, "blue")
	if err != nil {
		t.Fatal(err)
	}
	if added != 0 || skipped != 0 {
		t.Fatalf("blue re-attach = (added %d, skipped %d), want (0, 0)", added, skipped)
	}
	// Adding to blue does not disturb default: each channel now has one row per
	// group it belongs to, so six in total.
	if rows := mustListMembers(t, db, routeID); len(rows) != 6 {
		t.Fatalf("member rows = %d, want one per (channel, group) pair", len(rows))
	}

	// An empty request is a no-op, not an "attach everything" — the caller
	// decides whether an empty selection means all.
	added, skipped, err = db.AttachChannelsToRoute(routeID, "deepseek-v4-flash", store.ModelMatchExact, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if added != 0 || skipped != 0 {
		t.Fatalf("empty attach = (added %d, skipped %d), want (0, 0)", added, skipped)
	}
}

// TestRelatedMatchAttachesSiblings covers the "related" scope: a route named
// after the base model also accepts the channels that only list a "-variant"
// of it, and those members carry a {"real": …} redirect so the upstream gets a
// name it actually serves. An operator-visible symptom without the redirect is
// "the member is attached but every request 404s upstream".
func TestRelatedMatchAttachesSiblings(t *testing.T) {
	db := openTestDB(t)

	exact := newAutoMatchChannel(t, db, "exact", "mimo-v2.5", domain.StatusEnabled, domain.ModelSyncModeAuto)
	variant := newAutoMatchChannel(t, db, "variant", "mimo-v2.5-flash,mimo-v2.5-free", domain.StatusEnabled, domain.ModelSyncModeAuto)
	// The variants a real catalog uses are not all dash-separated: this one
	// keeps the prefix but not the shape.
	dotted := newAutoMatchChannel(t, db, "dotted", "mimo-v2.5.1", domain.StatusEnabled, domain.ModelSyncModeAuto)
	other := newAutoMatchChannel(t, db, "other", "mimo-v2.6", domain.StatusEnabled, domain.ModelSyncModeAuto)

	// The preview reports the same set the write will use, and names the entry
	// that matched — the console shows it next to the channel.
	matches, err := db.ChannelsMatchingModel("mimo-v2.5", store.ModelMatchRelated)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[int64]store.ModelChannelMatch{}
	for _, match := range matches {
		byID[match.ChannelID] = match
	}
	if len(matches) != 3 {
		t.Fatalf("related matches = %+v, want the exact channel and both variants", matches)
	}
	if _, ok := byID[other]; ok {
		t.Fatalf("a different version must not match: %+v", matches)
	}
	if byID[variant].Model != "mimo-v2.5-flash" {
		t.Fatalf("variant match = %q, want the deterministic sibling name", byID[variant].Model)
	}
	if byID[exact].Model != "mimo-v2.5" {
		t.Fatalf("exact match = %q", byID[exact].Model)
	}

	routeID, err := db.Route.Create(&domain.Route{ModelPattern: "mimo-v2.5", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	added, skipped, err := db.AttachChannelsToRoute(routeID, "mimo-v2.5", store.ModelMatchRelated, []int64{exact, variant, dotted, other}, "")
	if err != nil {
		t.Fatal(err)
	}
	if added != 3 || skipped != 1 {
		t.Fatalf("related attach = (added %d, skipped %d), want (3, 1)", added, skipped)
	}
	members := memberChannelIDs(t, db, routeID)
	if members[exact].MappingJSON != "" {
		t.Fatalf("an exact hit must keep forwarding the route name: %+v", members[exact])
	}
	if members[variant].MappingJSON != `{"real":"mimo-v2.5-flash"}` {
		t.Fatalf("variant member mapping = %q, want the matched sibling", members[variant].MappingJSON)
	}
	if members[dotted].MappingJSON != `{"real":"mimo-v2.5.1"}` {
		t.Fatalf("dotted variant member mapping = %q, want the matched prefix sibling", members[dotted].MappingJSON)
	}

	// The exact scope is unchanged: it attaches only the channel that really
	// lists the base name, which is what existing callers rely on.
	strict, err := db.ChannelsMatchingModel("mimo-v2.5", store.ModelMatchExact)
	if err != nil {
		t.Fatal(err)
	}
	if len(strict) != 1 || strict[0].ChannelID != exact {
		t.Fatalf("exact matches = %+v, want only the base-name channel", strict)
	}

	// A pattern the operator already wrote as a wildcard keeps its own
	// semantics: "related" must not stack a second wildcard onto it.
	wild, err := db.ChannelsMatchingModel("mimo-v2.5*", store.ModelMatchRelated)
	if err != nil {
		t.Fatal(err)
	}
	if len(wild) != 3 {
		t.Fatalf("wildcard pattern matches = %+v, want the same three channels", wild)
	}
}

package store_test

import (
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

// keepaliveFixture builds one site with two channels sharing one credential,
// which is the shape the ban window has to be counted in: one key, one window.
func keepaliveFixture(t *testing.T, db *store.DB, policy string, idleDays int) (siteID, credentialID int64, channelIDs []int64) {
	t.Helper()
	siteID, err := db.Site.Create(&domain.Site{
		Name: "keepalive-site", BaseURL: "https://keepalive.example",
		Platform: "openai-compatible", Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	if policy != "" || idleDays > 0 {
		site, err := db.Site.GetByID(siteID)
		if err != nil {
			t.Fatal(err)
		}
		site.CallPolicy = policy
		site.KeepaliveEnabled = true
		site.KeepaliveIdleDays = idleDays
		site.KeepaliveSafetyMarginDays = 2
		if err := db.Site.UpdateCallPolicy(site); err != nil {
			t.Fatal(err)
		}
	}
	credentialID, err = db.Credential.Create(&domain.Credential{
		SiteID: siteID, Kind: "api_key", SecretEnc: []byte("enc"), Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ka-a", "ka-b"} {
		channelID, err := db.Channel.Create(&domain.Channel{
			SiteID: &siteID, CredentialID: &credentialID, Name: name,
			BaseURL: "https://keepalive.example", ModelsCSV: "model-keepalive",
			GroupName: "default", Weight: 100, Status: domain.StatusEnabled,
		})
		if err != nil {
			t.Fatal(err)
		}
		channelIDs = append(channelIDs, channelID)
	}
	return siteID, credentialID, channelIDs
}

func TestCallPolicyInheritsFromSiteAndChannelWins(t *testing.T) {
	db := openTestDB(t)
	_, _, channels := keepaliveFixture(t, db, domain.CallPolicyRealCallsOnly, 30)

	policies, err := db.Channel.CallPolicies(channels)
	if err != nil {
		t.Fatal(err)
	}
	for _, channelID := range channels {
		if got := policies[channelID]; got != domain.CallPolicyRealCallsOnly {
			t.Errorf("channel %d policy = %q, want the site's real_calls_only", channelID, got)
		}
	}

	// A channel that overrides the site wins; the other keeps inheriting.
	channel, err := db.Channel.GetByID(channels[0])
	if err != nil {
		t.Fatal(err)
	}
	channel.CallPolicy = domain.CallPolicyAllowProbe
	if err := db.Channel.Update(channel); err != nil {
		t.Fatal(err)
	}
	policies, err = db.Channel.CallPolicies(channels)
	if err != nil {
		t.Fatal(err)
	}
	if got := policies[channels[0]]; got != domain.CallPolicyAllowProbe {
		t.Errorf("overridden channel policy = %q, want allow_probe", got)
	}
	if got := policies[channels[1]]; got != domain.CallPolicyRealCallsOnly {
		t.Errorf("inheriting channel policy = %q, want real_calls_only", got)
	}
}

// The override has to survive a round-trip through the console's own projection,
// or saving the channel drawer would blank it — the failure mode that made
// model_sync_mode look like it "just forgot" its value.
func TestChannelPolicyRoundTripsThroughTheOverviewProjection(t *testing.T) {
	db := openTestDB(t)
	_, _, channels := keepaliveFixture(t, db, "", 0)

	keepalive := true
	channel, err := db.Channel.GetByID(channels[0])
	if err != nil {
		t.Fatal(err)
	}
	channel.CallPolicy = domain.CallPolicyRealCallsOnly
	channel.KeepaliveEnabled = &keepalive
	channel.KeepaliveIdleDays = 15
	if err := db.Channel.Update(channel); err != nil {
		t.Fatal(err)
	}

	overviews, err := db.Channel.ListOverviews(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var found *domain.ChannelOverview
	for i := range overviews {
		if overviews[i].Channel.ID == channels[0] {
			found = &overviews[i]
		}
	}
	if found == nil {
		t.Fatal("channel missing from ListOverviews")
	}
	if found.Channel.CallPolicy != domain.CallPolicyRealCallsOnly {
		t.Errorf("overview call_policy = %q, want real_calls_only", found.Channel.CallPolicy)
	}
	if found.Channel.KeepaliveEnabled == nil || !*found.Channel.KeepaliveEnabled {
		t.Errorf("overview keepalive_enabled = %v, want an explicit true", found.Channel.KeepaliveEnabled)
	}
	if found.Channel.KeepaliveIdleDays != 15 {
		t.Errorf("overview keepalive_idle_days = %d, want 15", found.Channel.KeepaliveIdleDays)
	}

	// The other channel inherits, so its switch must stay nil rather than
	// reading back as an explicit "off".
	for i := range overviews {
		if overviews[i].Channel.ID != channels[1] {
			continue
		}
		if overviews[i].Channel.KeepaliveEnabled != nil {
			t.Errorf("inheriting channel keepalive_enabled = %v, want nil", overviews[i].Channel.KeepaliveEnabled)
		}
	}
}

// The counting unit is the credential: two channels sharing a key are one ban
// window, so they must not be called twice, and the idle age is the newest call
// across both.
func TestKeepaliveTargetsGroupByCredentialAndShareTheIdleClock(t *testing.T) {
	db := openTestDB(t)
	_, credentialID, channels := keepaliveFixture(t, db, domain.CallPolicyAllowProbe, 30)

	old := time.Now().UTC().Add(-40 * 24 * time.Hour)
	fresh := time.Now().UTC().Add(-1 * time.Hour)
	if err := db.Channel.MarkRealCall(channels[0], old); err != nil {
		t.Fatal(err)
	}
	if err := db.Channel.MarkRealCall(channels[1], fresh); err != nil {
		t.Fatal(err)
	}

	targets, err := db.Channel.KeepaliveTargets(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 {
		t.Fatalf("targets = %d, want one row for the shared credential", len(targets))
	}
	target := targets[0]
	if target.CredentialID != credentialID {
		t.Errorf("credential = %d, want %d", target.CredentialID, credentialID)
	}
	if target.LastCallAt == nil {
		t.Fatal("last call is nil, want the newest call of the group")
	}
	if !target.LastCallAt.After(old.Add(time.Minute)) {
		t.Errorf("last call = %v: the group's idle clock did not take the newest call", target.LastCallAt)
	}
	if idle := time.Since(*target.LastCallAt).Hours() / 24; idle > 1 {
		t.Errorf("idle = %.2f days, want the fresh call to define it", idle)
	}
	if target.Model != "model-keepalive" {
		t.Errorf("model = %q, want the channel's served model", target.Model)
	}
	if target.Config.IdleDays != 30 || target.Config.SafetyMarginDays != 2 {
		t.Errorf("config = %+v, want the site's 30-day window", target.Config)
	}
}

// A channel with no usable model has nowhere to send a keepalive, and saying so
// is the difference between "protected" and "silently never called".
func TestKeepaliveTargetsReportAModelLessChannel(t *testing.T) {
	db := openTestDB(t)
	siteID, err := db.Site.Create(&domain.Site{
		Name: "no-model", BaseURL: "https://no-model.example",
		Platform: "openai-compatible", Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Channel.Create(&domain.Channel{
		SiteID: &siteID, Name: "empty", BaseURL: "https://no-model.example",
		Status: domain.StatusEnabled,
	}); err != nil {
		t.Fatal(err)
	}

	targets, err := db.Channel.KeepaliveTargets(15)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 {
		t.Fatalf("targets = %d, want 1", len(targets))
	}
	if targets[0].SkipReason != "no_usable_model" {
		t.Errorf("skip reason = %q, want no_usable_model", targets[0].SkipReason)
	}
	// The global fallback window fills in for a site that has none.
	if targets[0].Config.IdleDays != 15 {
		t.Errorf("idle days = %d, want the global 15", targets[0].Config.IdleDays)
	}
}

// "The upstream received a request" must never move backwards, or a stale
// concurrent write would silently reset the ban clock.
func TestMarkRealCallNeverMovesBackwards(t *testing.T) {
	db := openTestDB(t)
	_, _, channels := keepaliveFixture(t, db, "", 0)

	newer := time.Now().UTC().Add(-2 * time.Hour)
	older := time.Now().UTC().Add(-20 * 24 * time.Hour)
	if err := db.Channel.MarkRealCall(channels[0], newer); err != nil {
		t.Fatal(err)
	}
	if err := db.Channel.MarkRealCall(channels[0], older); err != nil {
		t.Fatal(err)
	}
	channel, err := db.Channel.GetByID(channels[0])
	if err != nil {
		t.Fatal(err)
	}
	if channel.LastRealCallAt == nil {
		t.Fatal("last_real_call_at is nil")
	}
	if !channel.LastRealCallAt.After(older.Add(time.Hour)) {
		t.Errorf("last_real_call_at = %v: an older write moved the clock back", channel.LastRealCallAt)
	}
}

// The daily cap counts successful calls only: a failed attempt must not consume
// the day's allowance, or a transient upstream error would turn one window into
// a silent multi-day gap.
func TestKeepaliveSendsTodayCountsSuccessfulCallsOnly(t *testing.T) {
	db := openTestDB(t)
	_, credentialID, channels := keepaliveFixture(t, db, "", 0)

	event := domain.KeepaliveEvent{
		CredentialID: credentialID, ChannelID: channels[0], ChannelName: "ka-a",
		Model: "model-keepalive", Form: domain.CallFormReal, Reason: "idle 19.0d >= threshold 13d",
		OK: true, StatusCode: 200,
	}
	if err := db.Channel.RecordKeepaliveEvent(event); err != nil {
		t.Fatal(err)
	}
	failed := event
	failed.OK = false
	failed.Error = "upstream status 500"
	if err := db.Channel.RecordKeepaliveEvent(failed); err != nil {
		t.Fatal(err)
	}

	targets, err := db.Channel.KeepaliveTargets(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 {
		t.Fatalf("targets = %d, want 1", len(targets))
	}
	if targets[0].SendsToday != 1 {
		t.Errorf("sends today = %d, want 1 (the failure must not count)", targets[0].SendsToday)
	}

	events, err := db.Channel.RecentKeepaliveEvents(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	if events[0].Reason == "" || events[0].Form == "" {
		t.Errorf("the footprint record lost its reason or form: %+v", events[0])
	}
}

// A channel no route points at has no dispatcher, so the console must say so
// rather than let a failed call repeat every round.
func TestKeepaliveTargetsReportAChannelWithNoRoute(t *testing.T) {
	db := openTestDB(t)
	_, _, channels := keepaliveFixture(t, db, domain.CallPolicyAllowProbe, 30)

	targets, err := db.Channel.KeepaliveTargets(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 {
		t.Fatalf("targets = %d, want 1", len(targets))
	}
	if targets[0].SkipReason != "no_route" {
		t.Fatalf("skip reason = %q, want no_route (nothing routes to this channel)", targets[0].SkipReason)
	}

	// Once a route serves one of the channel's models, the target becomes
	// callable and the model is the one routing reaches it with.
	routeID, err := db.Route.Create(&domain.Route{
		ModelPattern: "model-routed", Enabled: true, RoutingMode: domain.RoutingModeAuto,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.RouteMember.Create(&domain.RouteMember{
		RouteID: routeID, ChannelID: channels[0], Weight: 100, Enabled: true, GroupName: "default",
	}); err != nil {
		t.Fatal(err)
	}
	// keepaliveFixture makes two channels sharing a credential; both need a
	// route member for the group to be callable.
	if _, err := db.RouteMember.Create(&domain.RouteMember{
		RouteID: routeID, ChannelID: channels[1], Weight: 100, Enabled: true, GroupName: "default",
	}); err != nil {
		t.Fatal(err)
	}

	targets, err = db.Channel.KeepaliveTargets(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 {
		t.Fatalf("targets = %d, want 1", len(targets))
	}
	if targets[0].SkipReason != "" {
		t.Fatalf("skip reason = %q, want empty once a route reaches the channel", targets[0].SkipReason)
	}
	if targets[0].Model != "model-routed" {
		t.Errorf("model = %q, want the routed pattern", targets[0].Model)
	}
}

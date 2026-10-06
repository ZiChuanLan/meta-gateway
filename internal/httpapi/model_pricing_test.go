package httpapi

import (
	"encoding/json"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
	"strings"
	"testing"
)

func TestModelPricingReferenceUsesBillingLayersAndAccountGrants(t *testing.T) {
	e := newTeamTestEnv(t)
	e.enable()
	route, err := e.db.Route.Create(&domain.Route{ModelPattern: "priced-model", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	channel, err := e.db.Channel.Create(&domain.Channel{Name: "private-channel-name", BaseURL: "https://private.example", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	first, err := e.db.RouteMember.Create(&domain.RouteMember{RouteID: route, ChannelID: channel, Enabled: true, Weight: 100, PriceCompletionPer1k: 3})
	if err != nil {
		t.Fatal(err)
	}
	second, err := e.db.RouteMember.Create(&domain.RouteMember{RouteID: route, ChannelID: channel, GroupName: "other", Enabled: true, Weight: 100})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.db.ModelMetadata.Upsert(&domain.ModelMetadata{ModelName: "priced-model", PricePromptPer1k: 1, PriceCompletionPer1k: 2, PriceCachePer1k: 0.2, PricePerRequest: 0.5, ContextWindow: 128000, SupportsThinking: 1, InputModalities: "text,image", OutputModalities: "text"}); err != nil {
		t.Fatal(err)
	}
	if err := e.db.ModelRatio.SetRatio("priced-model", 2); err != nil {
		t.Fatal(err)
	}
	e.admin("PUT", "/admin/team/policies/1", TeamPolicy{ID: 1, Name: "Limited", AllModels: true, MemberIDs: []int64{first}, MaxKeys: 2, RPM: 60}, 200)
	alice := e.member("pricing-alice", "member")
	var catalogue []teamModelView
	if err := json.Unmarshal(alice.request("GET", "/me/model-catalog", nil, 200), &catalogue); err != nil {
		t.Fatal(err)
	}
	if len(catalogue) != 1 || catalogue[0].Candidates != 1 || catalogue[0].ContextWindow != 128000 || catalogue[0].SupportsThinking != 1 || catalogue[0].InputModalities != "text,image" {
		t.Fatalf("capability projection: %+v", catalogue)
	}

	var member modelPriceView
	raw := alice.request("GET", "/me/model-pricing?model=priced-model&input_tokens=1000", nil, 200)
	if err := json.Unmarshal(raw, &member); err != nil {
		t.Fatal(err)
	}
	if member.Candidates != 1 || member.Input.Min != 0 || member.Output.Min != 6 || member.Cache.Min != 0 || member.PerRequest.Min != 0 {
		t.Fatalf("whole-layer selection/grants: %+v", member)
	}
	for _, secret := range []string{"private-channel-name", "private.example", "channel_id", "member_id"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("private detail leaked: %s", secret)
		}
	}
	var admin modelPriceView
	json.Unmarshal(e.admin("GET", "/admin/model-pricing?model=priced-model", nil, 200), &admin)
	if admin.Candidates != 2 || admin.Input.Min != 0 || admin.Input.Max != 2 || admin.Output.Min != 4 || admin.Output.Max != 6 {
		t.Fatalf("admin ranges: %+v", admin)
	}
	// Only the granted member can affect a member quote, even if the other price changes.
	if _, err := e.db.Exec(`UPDATE route_members SET price_prompt_per_1k=999 WHERE id=?`, second); err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(alice.request("GET", "/me/model-pricing?model=priced-model", nil, 200), &member)
	if member.Input.Max != 0 {
		t.Fatal("ungranted price influenced quote")
	}
	alice.request("GET", "/me/model-pricing?model=hidden-model", nil, 404)
	alice.request("GET", "/me/model-pricing?model=priced-model&input_tokens=-1", nil, 400)
	alice.request("GET", "/admin/model-pricing?model=priced-model", nil, 403)
}

func TestAliasReferencePriceMatchesUpstreamMetadataFallback(t *testing.T) {
	db, err := store.OpenTest(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	channel, err := db.Channel.Create(&domain.Channel{Name: "source", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.RouteMember.SetChannelModelAlias(channel, "real-model", "public"); err != nil {
		t.Fatal(err)
	}
	if err = db.ModelMetadata.Upsert(&domain.ModelMetadata{ModelName: "real-model", PricePromptPer1k: 1.25}); err != nil {
		t.Fatal(err)
	}
	view, err := referenceModelPricing(db, "public", 1000, nil)
	if err != nil {
		t.Fatal(err)
	}
	if view.Candidates != 1 || view.Input.Min != 1.25 || view.Input.Max != 1.25 {
		t.Fatalf("alias reference became free: %+v", view)
	}
	if err = db.ModelMetadata.Upsert(&domain.ModelMetadata{ModelName: "public", PriceCompletionPer1k: 4}); err != nil {
		t.Fatal(err)
	}
	view, err = referenceModelPricing(db, "public", 1000, nil)
	if err != nil {
		t.Fatal(err)
	}
	if view.Input.Max != 0 || view.Output.Max != 4 {
		t.Fatalf("reference price mixed layers: %+v", view)
	}
}

func TestReferenceModelPricingTiersTimeCacheAndZeroRatio(t *testing.T) {
	e := newTeamTestEnv(t)
	route, err := e.db.Route.Create(&domain.Route{ModelPattern: "tier-model", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	channel, err := e.db.Channel.Create(&domain.Channel{Name: "c", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.db.RouteMember.Create(&domain.RouteMember{RouteID: route, ChannelID: channel, Enabled: true, Weight: 1,
		PriceTiers:    `[{"max_prompt_tokens":1000,"prompt":1,"completion":2},{"max_prompt_tokens":0,"prompt":3,"completion":4,"per_request":0.5}]`,
		PriceSchedule: `[{"from_hour":0,"to_hour":0,"multiplier":0.5}]`})
	if err != nil {
		t.Fatal(err)
	}
	quote, err := referenceModelPricing(e.db, "tier-model", 1001, nil)
	if err != nil {
		t.Fatal(err)
	}
	if quote.Input.Min != 1.5 || quote.Output.Min != 2 || quote.Cache.Min != 1.5 || quote.PerRequest.Min != 0.25 || !quote.Tiered || !quote.Scheduled {
		t.Fatalf("tier quote: %+v", quote)
	}
	if err := e.db.ModelRatio.SetRatio("tier-model", 0); err != nil {
		t.Fatal(err)
	}
	quote, err = referenceModelPricing(e.db, "tier-model", 1001, nil)
	if err != nil {
		t.Fatal(err)
	}
	if quote.Input.Max != 0 || quote.Output.Max != 0 || quote.PerRequest.Max != 0 {
		t.Fatalf("explicit zero ratio: %+v", quote)
	}
}

func TestMemberHistogramUsesOnlyAccountRequests(t *testing.T) {
	e := newTeamTestEnv(t)
	e.enable()
	alice := e.member("hist-alice", "member")
	bob := e.member("hist-bob", "member")
	for _, row := range []struct {
		id      string
		user    int64
		latency int
	}{{"alice-1", alice.userID, 100}, {"alice-2", alice.userID, 6000}, {"bob-1", bob.userID, 60000}} {
		if _, err := e.db.Exec(`INSERT INTO team_requests(request_id,user_id,key_id,path,status,latency_ms) VALUES(?,?,1,'chat/completions',200,?)`, row.id, row.user, row.latency); err != nil {
			t.Fatal(err)
		}
	}
	var hist store.LatencyHistogram
	if err := json.Unmarshal(alice.request("GET", "/me/requests/latency-histogram?sample=1&user_id="+"999", nil, 200), &hist); err != nil {
		t.Fatal(err)
	}
	if hist.Matched != 2 || hist.Total != 1 || hist.P50Ms != 6000 || hist.SlowCount != 1 {
		t.Fatalf("scoped sample: %+v", hist)
	}
	json.Unmarshal(bob.request("GET", "/me/requests/latency-histogram", nil, 200), &hist)
	if hist.Matched != 1 || hist.Total != 1 || hist.P50Ms != 60000 {
		t.Fatalf("bob histogram: %+v", hist)
	}
	alice.request("GET", "/me/requests/latency-histogram?since=invalid", nil, 400)
	json.Unmarshal(alice.request("GET", "/me/requests/latency-histogram?since=2099-01-01T00:00:00Z", nil, 200), &hist)
	if hist.Total != 0 || hist.Matched != 0 {
		t.Fatal("histogram ignored time range")
	}
	if _, err := e.db.Exec(`UPDATE team_settings SET branding_json=json_set(branding_json,'$.show_usage',json('false')) WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	alice.request("GET", "/me/requests/latency-histogram", nil, 403)
	if _, err := e.db.ProxyLog.MemberLatencyHistogram(0, 10, nil, nil); err == nil {
		t.Fatal("missing scope allowed")
	}
}

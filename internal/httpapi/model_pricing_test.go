package httpapi

import (
	"encoding/json"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
	"net/http"
	"strconv"
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

func pricePtr(value float64) *float64 { return &value }

// A batch must answer for every requested model without failing the whole call
// on one denied or missing name, dedupe repeats, and keep an unpriced candidate
// distinct from a layer that explicitly prices at zero.
func TestModelPricingBatchDedupesAndDistinguishesUnpricedFromFree(t *testing.T) {
	e := newTeamTestEnv(t)
	channel, err := e.db.Channel.Create(&domain.Channel{Name: "batch-private-channel", BaseURL: "https://batch-private.example", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	routeFor := func(pattern string) int64 {
		t.Helper()
		route, err := e.db.Route.Create(&domain.Route{ModelPattern: pattern, Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		return route
	}
	memberOn := func(route int64, group string) int64 {
		t.Helper()
		id, err := e.db.RouteMember.Create(&domain.RouteMember{RouteID: route, ChannelID: channel, GroupName: group, Enabled: true, Weight: 1})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}

	// One priced candidate.
	priced := memberOn(routeFor("batch-priced"), "default")
	if _, err = e.db.Exec(`UPDATE route_members SET price_prompt_per_1k=2 WHERE id=?`, priced); err != nil {
		t.Fatal(err)
	}
	// A free rule states a price of zero: priced, not unpriced.
	freeMember := memberOn(routeFor("batch-free"), "default")
	if _, err = e.db.SaveBillingRule("member", strconv.FormatInt(freeMember, 10), domain.BillingRule{State: "free", Timezone: "UTC"}, 0); err != nil {
		t.Fatal(err)
	}
	// Candidates exist but no layer prices them.
	memberOn(routeFor("batch-unpriced"), "default")
	// One priced and one unpriced candidate: the quote is partial.
	partialRoute := routeFor("batch-partial")
	partial := memberOn(partialRoute, "default")
	if _, err = e.db.Exec(`UPDATE route_members SET price_prompt_per_1k=3 WHERE id=?`, partial); err != nil {
		t.Fatal(err)
	}
	memberOn(partialRoute, "other")

	raw := e.admin("GET", "/admin/model-pricing/batch?input_tokens=1000&model=batch-priced&model=batch-priced&model=batch-free&model=batch-unpriced&model=batch-partial&model=batch-missing", nil, 200)
	var batch modelPricingBatch
	if err = json.Unmarshal(raw, &batch); err != nil {
		t.Fatal(err)
	}
	// The duplicate name is asked for once, and only the unrouted model is
	// unavailable — the batch echoes back names the caller sent.
	if len(batch.Items) != 4 {
		t.Fatalf("batch items: %+v", batch)
	}
	if len(batch.Unavailable) != 1 || batch.Unavailable[0] != "batch-missing" {
		t.Fatalf("batch unavailable: %+v", batch.Unavailable)
	}
	for _, item := range batch.Items {
		if item.Model == "batch-missing" {
			t.Fatal("unrouted model resolved")
		}
	}
	byModel := map[string]*modelPriceView{}
	for _, item := range batch.Items {
		byModel[item.Model] = item
	}
	for model, want := range map[string]struct {
		status     string
		candidates int
		priced     int
	}{
		"batch-priced":   {"priced", 1, 1},
		"batch-free":     {"priced", 1, 1},
		"batch-partial":  {"partially_priced", 2, 1},
		"batch-unpriced": {"unpriced", 1, 0},
	} {
		got, ok := byModel[model]
		if !ok {
			t.Fatalf("%s missing from batch", model)
		}
		if got.Status != want.status || got.Candidates != want.candidates || got.PricedCandidates != want.priced {
			t.Fatalf("%s status: %+v", model, got)
		}
	}
	// The unpriced view keeps its zero range (Wire compatibility) but the free
	// layer must not have invented a charge either.
	if free := byModel["batch-free"]; free.Input.Min != 0 || free.Input.Max != 0 || free.Output.Max != 0 {
		t.Fatalf("free layer invented a charge: %+v", free)
	}
	if unknown := byModel["batch-unpriced"]; unknown.Input.Min != 0 || unknown.Input.Max != 0 {
		t.Fatalf("unpriced ranges must stay zero: %+v", unknown)
	}
	for _, secret := range []string{"batch-private-channel", "batch-private.example", "channel_id", "member_id", "quote_id"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("private detail leaked: %s", secret)
		}
	}
}

func TestModelPricingCacheKnownTracksDeclarations(t *testing.T) {
	e := newTeamTestEnv(t)
	channel, err := e.db.Channel.Create(&domain.Channel{Name: "cache-channel", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	memberOn := func(pattern string) int64 {
		t.Helper()
		route, err := e.db.Route.Create(&domain.Route{ModelPattern: pattern, Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		id, err := e.db.RouteMember.Create(&domain.RouteMember{RouteID: route, ChannelID: channel, Enabled: true, Weight: 1})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	quote := func(model string) modelPriceView {
		t.Helper()
		var view modelPriceView
		if err := json.Unmarshal(e.admin("GET", "/admin/model-pricing?model="+model, nil, 200), &view); err != nil {
			t.Fatal(err)
		}
		return view
	}

	// A layer with no cache column falls back to the input price, which is not
	// a declared cache price even though the range is populated.
	fallback := memberOn("cache-fallback")
	if _, err = e.db.Exec(`UPDATE route_members SET price_prompt_per_1k=4 WHERE id=?`, fallback); err != nil {
		t.Fatal(err)
	}
	view := quote("cache-fallback")
	if view.CacheKnown || view.CacheWriteKnown {
		t.Fatalf("input-price cache fallback claimed as known: %+v", view)
	}
	if view.Cache.Min != 4 || view.Cache.Max != 4 {
		t.Fatalf("fallback range changed unexpectedly: %+v", view)
	}

	// An explicit rule that declares a cache-read price is known; cache write
	// stays unknown until it is declared too.
	declared := memberOn("cache-declared")
	if _, err = e.db.SaveBillingRule("member", strconv.FormatInt(declared, 10), domain.BillingRule{State: "explicit", Mode: "token", Input: pricePtr(1), Output: pricePtr(2), CacheRead: pricePtr(0.5), Timezone: "UTC"}, 0); err != nil {
		t.Fatal(err)
	}
	if view = quote("cache-declared"); !view.CacheKnown || view.CacheWriteKnown {
		t.Fatalf("declared cache read: %+v", view)
	}

	written := memberOn("cache-written")
	if _, err = e.db.SaveBillingRule("member", strconv.FormatInt(written, 10), domain.BillingRule{State: "explicit", Mode: "token", Input: pricePtr(1), Output: pricePtr(2), CacheRead: pricePtr(0.5), CacheWrite: pricePtr(0.25), Timezone: "UTC"}, 0); err != nil {
		t.Fatal(err)
	}
	if view = quote("cache-written"); !view.CacheKnown || !view.CacheWriteKnown {
		t.Fatalf("declared cache write: %+v", view)
	}

	// A free layer is a price of zero, not an unknown one.
	free := memberOn("cache-free")
	if _, err = e.db.SaveBillingRule("member", strconv.FormatInt(free, 10), domain.BillingRule{State: "free", Timezone: "UTC"}, 0); err != nil {
		t.Fatal(err)
	}
	if view = quote("cache-free"); !view.CacheKnown || !view.CacheWriteKnown || view.Status != "priced" {
		t.Fatalf("free layer: %+v", view)
	}
}

func TestMemberModelPricingBatchHonorsPolicyGrants(t *testing.T) {
	e := newTeamTestEnv(t)
	e.enable()
	route, err := e.db.Route.Create(&domain.Route{ModelPattern: "grant-model", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	channel, err := e.db.Channel.Create(&domain.Channel{Name: "grant-private-channel", BaseURL: "https://grant-private.example", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	granted, err := e.db.RouteMember.Create(&domain.RouteMember{RouteID: route, ChannelID: channel, Enabled: true, Weight: 100, PricePromptPer1k: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.db.RouteMember.Create(&domain.RouteMember{RouteID: route, ChannelID: channel, GroupName: "other", Enabled: true, Weight: 100, PricePromptPer1k: 9}); err != nil {
		t.Fatal(err)
	}
	e.admin("PUT", "/admin/team/policies/1", TeamPolicy{ID: 1, Name: "Limited", AllModels: true, MemberIDs: []int64{granted}, MaxKeys: 2, RPM: 60}, 200)
	alice := e.member("batch-alice", "member")

	// The operator sees both candidates; the member only the granted one, and
	// the member's batch still answers 200 when one name is denied.
	var adminBatch modelPricingBatch
	if err = json.Unmarshal(e.admin("GET", "/admin/model-pricing/batch?model=grant-model", nil, 200), &adminBatch); err != nil {
		t.Fatal(err)
	}
	if len(adminBatch.Items) != 1 || adminBatch.Items[0].Candidates != 2 || adminBatch.Items[0].Input.Min != 1 || adminBatch.Items[0].Input.Max != 9 {
		t.Fatalf("admin batch: %+v", adminBatch)
	}

	raw := alice.request("GET", "/me/model-pricing/batch?model=grant-model&model=hidden-model", nil, 200)
	var memberBatch modelPricingBatch
	if err = json.Unmarshal(raw, &memberBatch); err != nil {
		t.Fatal(err)
	}
	if len(memberBatch.Items) != 1 || memberBatch.Items[0].Candidates != 1 || memberBatch.Items[0].Input.Min != 1 || memberBatch.Items[0].Input.Max != 1 {
		t.Fatalf("member batch grants: %+v", memberBatch)
	}
	if len(memberBatch.Unavailable) != 1 || memberBatch.Unavailable[0] != "hidden-model" {
		t.Fatalf("denied model leaked or failed the batch: %+v", memberBatch.Unavailable)
	}
	for _, secret := range []string{"grant-private-channel", "grant-private.example", "channel_id", "member_id", "quote_id"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("private detail leaked: %s", secret)
		}
	}

	// Neither surface answers anonymously.
	if code, _, _ := e.call(http.DefaultClient, "GET", "/admin/model-pricing/batch?model=grant-model", nil, "", ""); code != 401 {
		t.Fatalf("anonymous admin batch: %d", code)
	}
	anon := e.browser()
	if code, _, _ := e.call(anon.client, "GET", "/me/model-pricing/batch?model=grant-model", nil, "", anon.csrf); code != 401 {
		t.Fatalf("anonymous member batch: %d", code)
	}

	// Parameter validation matches the single endpoint.
	alice.request("GET", "/me/model-pricing/batch", nil, 400)
	alice.request("GET", "/me/model-pricing/batch?model=grant-*", nil, 400)
	alice.request("GET", "/me/model-pricing/batch?model=grant-model&input_tokens=-1", nil, 400)
	over := "/me/model-pricing/batch?"
	for i := 0; i <= maxBatchModels; i++ {
		over += "model=m" + strconv.Itoa(i) + "&"
	}
	alice.request("GET", over, nil, 400)
}

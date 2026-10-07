package siteprobe

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/adapters"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

// ratioSource lets a test flip a fake status page between availability levels,
// so a member can be watched going down and coming back.
type ratioSource struct {
	up   atomic.Int64
	down atomic.Int64
}

func (r *ratioSource) set(up, down int) {
	r.up.Store(int64(up))
	r.down.Store(int64(down))
}

type fixture struct {
	db        *store.DB
	service   *Service
	siteID    int64
	channelID int64
	routeID   int64
	memberID  int64
}

// newFixture wires one site (with a probe source pointed at a fake status page),
// one channel, one route and one enabled member — the smallest arrangement in
// which a site's own probe data can move a routing decision.
func newFixture(t *testing.T, source *ratioSource, monitor, route string) *fixture {
	t.Helper()
	db, err := store.OpenTest(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	// Closed when the test ends, not when this helper returns.
	t.Cleanup(func() { _ = db.Close() })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		kumaHandler(t, int(source.up.Load()), int(source.down.Load()), map[string]string{"44": monitor})(w, r)
	}))
	t.Cleanup(server.Close)

	siteID, err := db.Site.Create(&domain.Site{
		Name: "公益站A", BaseURL: "https://a.example", Platform: "new-api", Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatalf("create site: %v", err)
	}
	if err := db.UpdateSiteProbeSource(siteID, SourceUptimeKuma, server.URL+"/status/ai", "{}", true); err != nil {
		t.Fatalf("configure probe source: %v", err)
	}
	channelID, err := db.Channel.Create(&domain.Channel{SiteID: &siteID, Name: "A-key", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	routeID, err := db.Route.Create(&domain.Route{ModelPattern: route, Enabled: true})
	if err != nil {
		t.Fatalf("create route: %v", err)
	}
	memberID, err := db.RouteMember.Create(&domain.RouteMember{
		RouteID: routeID, ChannelID: channelID, Enabled: true, Auto: true, Weight: 100,
	})
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
	return &fixture{
		db:        db,
		service:   NewService(db, nil, nil),
		siteID:    siteID,
		channelID: channelID,
		routeID:   routeID,
		memberID:  memberID,
	}
}

func (f *fixture) collect(t *testing.T) *store.SiteProbeRun {
	t.Helper()
	site, err := f.db.Site.GetByID(f.siteID)
	if err != nil || site == nil {
		t.Fatalf("load site: %v", err)
	}
	run, err := f.service.CollectSite(context.Background(), *site)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	return run
}

func (f *fixture) collectTimes(t *testing.T, rounds int) {
	t.Helper()
	for i := 0; i < rounds; i++ {
		f.collect(t)
	}
}

func (f *fixture) member(t *testing.T) *domain.RouteMember {
	t.Helper()
	member, err := f.db.RouteMember.GetByID(f.memberID)
	if err != nil || member == nil {
		t.Fatalf("load member: %v", err)
	}
	return member
}

func (f *fixture) apply(t *testing.T, request ApplyRequest) []Action {
	t.Helper()
	actions, err := f.service.Apply(context.Background(), request)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	return actions
}

// The collected data must not leak into the tables that mean "a real
// completion was sent": model_health drives the real probe's auto-disable, and
// two sources sharing one counter would fight over it.
func TestCollectStoresSamplesWithoutTouchingModelHealth(t *testing.T) {
	source := &ratioSource{}
	source.set(9, 1)
	f := newFixture(t, source, "z-ai/glm-5.2", "glm-5.2")

	run := f.collect(t)
	if run == nil || run.Status != store.SiteProbeRunOK {
		t.Fatalf("run = %+v, want an ok round", run)
	}
	samples, err := f.db.ListSiteProbeSamples(run.ID)
	if err != nil || len(samples) == 0 {
		t.Fatalf("samples = %d err = %v, want the monitors stored", len(samples), err)
	}
	var model *store.SiteProbeSample
	for index := range samples {
		if samples[index].RawModel == "z-ai/glm-5.2" {
			model = &samples[index]
		}
	}
	if model == nil {
		t.Fatalf("monitor sample missing from %+v", samples)
	}
	if model.Ratio != 0.9 || model.Samples != 10 {
		t.Fatalf("sample = %+v, want 9/10", *model)
	}
	health, err := f.db.ListModelHealth()
	if err != nil {
		t.Fatalf("list model health: %v", err)
	}
	if len(health) != 0 {
		t.Fatalf("model_health rows = %d: the site probe must not write real-probe state", len(health))
	}
}

// One bad round is noise. The verdict only escalates once the evidence is
// consecutive, which is what stops a jittery status page from parking members.
func TestVerdictNeedsConsecutiveRounds(t *testing.T) {
	source := &ratioSource{}
	source.set(2, 8)
	f := newFixture(t, source, "z-ai/glm-5.2", "glm-5.2")

	f.collectTimes(t, 1)
	report, err := f.service.Report(DefaultPolicy())
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if len(report.Rows) != 1 {
		t.Fatalf("rows = %d, want 1 (%+v)", len(report.Rows), report.Rows)
	}
	if report.Rows[0].Verdict != VerdictPending {
		t.Fatalf("verdict after one round = %q, want pending", report.Rows[0].Verdict)
	}
	if report.Rows[0].Match != MatchNormalized {
		t.Fatalf("match = %q, want normalized (vendor prefix dropped)", report.Rows[0].Match)
	}

	f.collectTimes(t, 1)
	report, err = f.service.Report(DefaultPolicy())
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if report.Rows[0].Verdict != VerdictLow {
		t.Fatalf("verdict after two low rounds = %q, want low", report.Rows[0].Verdict)
	}
	if report.Rows[0].LowStreak != 2 {
		t.Fatalf("low streak = %d, want 2", report.Rows[0].LowStreak)
	}
}

func TestApplyDisablesOnlyAfterConsecutiveLowRounds(t *testing.T) {
	source := &ratioSource{}
	source.set(3, 7)
	f := newFixture(t, source, "z-ai/glm-5.2", "glm-5.2")

	f.collectTimes(t, 1)
	if actions := f.apply(t, ApplyRequest{DryRun: false}); len(actions) != 0 {
		t.Fatalf("actions after a single round = %+v, want none", actions)
	}
	f.collectTimes(t, 1)

	actions := f.apply(t, ApplyRequest{DryRun: true})
	if len(actions) != 1 || actions[0].Kind != "disable" {
		t.Fatalf("dry-run actions = %+v, want one disable", actions)
	}
	if member := f.member(t); !member.Enabled {
		t.Fatal("a dry run changed the member")
	}

	actions = f.apply(t, ApplyRequest{DryRun: false})
	if len(actions) != 1 || actions[0].MembersMoved != 1 {
		t.Fatalf("actions = %+v, want the member moved", actions)
	}
	member := f.member(t)
	if member.Enabled || !member.AutoDisabled {
		t.Fatalf("member = enabled:%v auto_disabled:%v, want the probe-disabled state", member.Enabled, member.AutoDisabled)
	}
	if !strings.Contains(member.LastError, "site probe") {
		t.Fatalf("last_error = %q, want the site-probe reason", member.LastError)
	}
}

func TestApplyRecoversAMemberTheSiteReportsHealthyAgain(t *testing.T) {
	source := &ratioSource{}
	source.set(1, 9)
	f := newFixture(t, source, "z-ai/glm-5.2", "glm-5.2")
	f.collectTimes(t, 2)
	f.apply(t, ApplyRequest{DryRun: false})
	if member := f.member(t); member.Enabled || !member.AutoDisabled {
		t.Fatalf("setup failed: member = %+v", member)
	}

	source.set(10, 0)
	f.collectTimes(t, 2)
	actions := f.apply(t, ApplyRequest{DryRun: false})
	if len(actions) != 1 || actions[0].Kind != "recover" || actions[0].MembersMoved != 1 {
		t.Fatalf("recovery actions = %+v", actions)
	}
	member := f.member(t)
	if !member.Enabled || member.AutoDisabled {
		t.Fatalf("member = enabled:%v auto_disabled:%v, want it back in rotation", member.Enabled, member.AutoDisabled)
	}
}

// A route pinned to a single member must never be disabled by a preventive
// mechanism: that would make the model unreachable for everyone.
func TestSingleMemberRouteIsNeverDisabled(t *testing.T) {
	source := &ratioSource{}
	source.set(0, 10)
	f := newFixture(t, source, "z-ai/glm-5.2", "glm-5.2")
	if _, err := f.db.Exec(`UPDATE routes SET single_member_id = ? WHERE id = ?`, f.memberID, f.routeID); err != nil {
		t.Fatalf("pin single member: %v", err)
	}
	f.collectTimes(t, 2)

	actions := f.apply(t, ApplyRequest{DryRun: false})
	if len(actions) != 1 || actions[0].Kind != "disable" {
		t.Fatalf("actions = %+v, want the skip decision reported", actions)
	}
	if actions[0].Skipped != "single_member" {
		t.Fatalf("skipped = %q, want single_member", actions[0].Skipped)
	}
	if member := f.member(t); !member.Enabled {
		t.Fatal("the single member of a route was disabled")
	}
}

// A member an operator switched off by hand must stay off no matter how green
// the site's probe looks.
func TestManuallyDisabledMemberIsNotRecovered(t *testing.T) {
	source := &ratioSource{}
	source.set(10, 0)
	f := newFixture(t, source, "z-ai/glm-5.2", "glm-5.2")
	if _, err := f.db.Exec(`UPDATE route_members SET enabled = 0, auto_disabled = 0 WHERE id = ?`, f.memberID); err != nil {
		t.Fatalf("disable by hand: %v", err)
	}
	f.collectTimes(t, 2)

	actions := f.apply(t, ApplyRequest{DryRun: false})
	if len(actions) != 0 {
		t.Fatalf("actions = %+v, want none: a manual disable is not the probe's to undo", actions)
	}
	if member := f.member(t); member.Enabled {
		t.Fatal("a manually disabled member was recovered")
	}
}

// A source that answers with an error produces no samples, and therefore no
// verdict: fail-open, never fail-disable.
func TestFailedCollectionProducesNoVerdictAndNoAction(t *testing.T) {
	f := newFixture(t, &ratioSource{}, "z-ai/glm-5.2", "glm-5.2")
	// Point the source at a stale path so the status page 404s.
	if err := f.db.UpdateSiteProbeSource(f.siteID, SourceUptimeKuma, "https://127.0.0.1:1/status/ai", "{}", true); err != nil {
		t.Fatalf("configure probe source: %v", err)
	}
	run := f.collect(t)
	if run == nil || run.Status != store.SiteProbeRunFailed {
		t.Fatalf("run = %+v, want a failed round", run)
	}
	if run.Error == "" {
		t.Fatal("a failed round must record why")
	}
	report, err := f.service.Report(DefaultPolicy())
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	// A failed round still produces a row for what this site serves — the operator
	// should see the model and the absence of data — but never a verdict: "no data"
	// is not "down", and nothing may act on it.
	for _, row := range report.Rows {
		if row.Verdict != VerdictNoData {
			t.Fatalf("row %q = %q without samples, want no_data", row.Route, row.Verdict)
		}
		if row.AvailabilitySource != "" {
			t.Fatalf("row %q claims an evidence source %q with no samples", row.Route, row.AvailabilitySource)
		}
	}
	// The JSON must carry empty arrays, not nulls: the console renders them
	// directly, and `null.filter` is exactly how an empty state crashes a view
	// that every data-filled test passes. (sites is non-empty here — the
	// configured site is listed even when its collection failed.)
	encoded, err := json.Marshal(Report{Policy: report.Policy, GeneratedAt: report.GeneratedAt, Rows: []Row{}, Unmatched: []Unmatched{}, NameOnly: []NameOnly{}, Sites: report.Sites})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, field := range []string{"\"rows\":[]", "\"unmatched\":[]", "\"name_only\":[]"} {
		if !strings.Contains(string(encoded), field) {
			t.Fatalf("report JSON = %s, want %s (null array breaks the console)", encoded, field)
		}
	}
	if actions := f.apply(t, ApplyRequest{DryRun: false}); len(actions) != 0 {
		t.Fatalf("actions = %+v, want none", actions)
	}
	if member := f.member(t); !member.Enabled {
		t.Fatal("a failed collection disabled a member")
	}
	site, err := f.db.Site.GetByID(f.siteID)
	if err != nil || site == nil {
		t.Fatalf("load site: %v", err)
	}
	if site.ProbeLastError == "" {
		t.Fatal("the site row must surface the collection error")
	}
}

// A route whose members live on one site must not produce rows for another site
// that merely publishes a model with the same name.
//
// The match index is a name index: site B publishing "deepseek-v4-flash" matches
// route STRRX because a member of STRRX — on site A — maps that upstream name.
// That is a candidate, not a reading: site B has no member on STRRX, so there is
// nothing to judge, disable or price. Before this split the report emitted a row
// anyway, titled with the route name; in production 31 of 47 rows were like that
// and 15 of them shared one route name, so the whole table read as one model.
func TestNameOnlyMatchesAreCandidatesNotRows(t *testing.T) {
	db, err := store.OpenTest(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()

	// Site A owns the member that maps the upstream name.
	siteA, err := db.Site.Create(&domain.Site{Name: "A", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	channelA, err := db.Channel.Create(&domain.Channel{SiteID: &siteA, Name: "A-key", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	routeID, err := db.Route.Create(&domain.Route{ModelPattern: "STRRX", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	memberID, err := db.RouteMember.Create(&domain.RouteMember{
		RouteID: routeID, ChannelID: channelA, Enabled: true, MappingJSON: `{"real":"deepseek-v4-flash"}`,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Site B publishes the same upstream name and has no member on that route.
	siteB, err := db.Site.Create(&domain.Site{Name: "B", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Channel.Create(&domain.Channel{SiteID: &siteB, Name: "B-key", Status: domain.StatusEnabled}); err != nil {
		t.Fatal(err)
	}
	runB, err := db.CreateSiteProbeRun(siteB, SourceNewAPI, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := db.InsertSiteProbeSamples([]store.SiteProbeSample{{
		RunID: runB, SiteID: siteB, MonitorName: "deepseek-v4-flash", RawModel: "deepseek-v4-flash",
		ObservedAt: time.Now(), Samples: 10, UpCount: 3, Ratio: 0.3,
	}}); err != nil {
		t.Fatal(err)
	}
	// Only a finished, successful round is evidence: the report reads samples
	// through a join on the run's status, so an open round is invisible.
	if err := db.FinishSiteProbeRun(runB, store.SiteProbeRunOK, 1, ""); err != nil {
		t.Fatal(err)
	}

	report, err := NewService(db, nil, nil).Report(DefaultPolicy())
	if err != nil {
		t.Fatalf("report: %v", err)
	}

	for _, row := range report.Rows {
		if row.SiteID == siteB {
			t.Fatalf("site B got a row for %q with %d members; a site that serves nothing must not be judged", row.Route, len(row.Members))
		}
	}
	attached := false
	for _, row := range report.Rows {
		if row.SiteID == siteA && row.Route == "STRRX" {
			attached = true
			if len(row.Members) != 1 || row.Members[0].MemberID != memberID {
				t.Fatalf("site A row members = %+v, want the one member", row.Members)
			}
		}
	}
	if !attached {
		t.Fatal("site A lost the row for its own member")
	}

	if len(report.NameOnly) != 1 {
		t.Fatalf("name-only candidates = %+v, want exactly the site B collision", report.NameOnly)
	}
	candidate := report.NameOnly[0]
	if candidate.SiteID != siteB || candidate.Route != "STRRX" || candidate.RawModel != "deepseek-v4-flash" {
		t.Fatalf("candidate = %+v, want site B / deepseek-v4-flash -> STRRX", candidate)
	}
	if candidate.Match != MatchMemberReal {
		t.Fatalf("candidate match = %q, want %q", candidate.Match, MatchMemberReal)
	}
	// The site's own reading travels with the candidate: it is why an operator
	// might attach a member at all.
	if candidate.Samples != 10 || candidate.Ratio != 0.3 {
		t.Fatalf("candidate reading = %d samples at %v", candidate.Samples, candidate.Ratio)
	}

	// And a candidate is never acted on: applying must leave the member alone.
	actions, err := NewService(db, nil, nil).Apply(context.Background(), ApplyRequest{DryRun: false})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(actions) != 0 {
		t.Fatalf("actions = %+v, want none for a name-only candidate", actions)
	}
	member, err := db.RouteMember.GetByID(memberID)
	if err != nil || member == nil {
		t.Fatalf("load member: %v", err)
	}
	if member.AutoDisabled {
		t.Fatal("a candidate disabled the member on another site")
	}
}

func newMatchResolver(t *testing.T) *Resolver {
	t.Helper()
	db, err := store.OpenTest(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	siteID, _ := db.Site.Create(&domain.Site{Name: "s", Status: domain.StatusEnabled})
	channelID, _ := db.Channel.Create(&domain.Channel{SiteID: &siteID, Name: "c", Status: domain.StatusEnabled})
	exact, _ := db.Route.Create(&domain.Route{ModelPattern: "glm-5.2", Enabled: true})
	if _, err := db.RouteMember.Create(&domain.RouteMember{RouteID: exact, ChannelID: channelID, Enabled: true, MappingJSON: `{"real":"z-ai/glm-5.2"}`}); err != nil {
		t.Fatalf("member: %v", err)
	}
	wildcard, _ := db.Route.Create(&domain.Route{ModelPattern: "gpt-*", Enabled: true})
	if _, err := db.RouteMember.Create(&domain.RouteMember{RouteID: wildcard, ChannelID: channelID, Enabled: true}); err != nil {
		t.Fatalf("member: %v", err)
	}
	// The catalog's own naming: every alias carries a namespace, while a site's
	// price table lists the bare upstream name once.
	for _, pattern := range []string{"cn:glm-5.2", "global:glm-5.2"} {
		route, _ := db.Route.Create(&domain.Route{ModelPattern: pattern, Enabled: true})
		if _, err := db.RouteMember.Create(&domain.RouteMember{RouteID: route, ChannelID: channelID, Enabled: true}); err != nil {
			t.Fatalf("member: %v", err)
		}
	}
	resolver, err := BuildResolver(db)
	if err != nil {
		t.Fatalf("resolver: %v", err)
	}
	return resolver
}

func TestMatchKinds(t *testing.T) {
	resolver := newMatchResolver(t)
	cases := []struct {
		raw   string
		route string
		kind  string
	}{
		{"glm-5.2", "glm-5.2", MatchExact},
		{"z-ai/glm-5.2", "glm-5.2", MatchMemberReal},
		{"Z-AI/GLM-5.2", "glm-5.2", MatchNormalized},
		{"gpt-5.5", "gpt-*", MatchPattern},
		{"kimi-k3", "", ""},
	}
	for _, testCase := range cases {
		route, kind, ok := resolver.Match(testCase.raw)
		if testCase.route == "" {
			if ok {
				t.Errorf("Match(%q) = %q/%q, want no match", testCase.raw, route, kind)
			}
			continue
		}
		if !ok || route != testCase.route || kind != testCase.kind {
			t.Errorf("Match(%q) = %q/%q ok=%v, want %q/%q", testCase.raw, route, kind, ok, testCase.route, testCase.kind)
		}
	}
}

// A bare upstream name from a site's price table lines up with every catalog
// alias of that model, and each alias is a route with its own members — so the
// resolver has to answer with all of them, not with an arbitrary winner.
// Our own relay logs are the availability evidence for a site that publishes
// none — which is every New-API price source. Without this the operator sees a
// table full of "no samples" for sites their gateway demonstrably serves.
func TestTrafficAvailabilityDrivesTheVerdictWhenTheSitePublishesNone(t *testing.T) {
	db, err := store.OpenTest(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// A price-only source: /api/pricing answers, and it carries no health at all.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/pricing":
			_, _ = w.Write([]byte(`{"data":[{"model_name":"glm-5.3-flash","model_ratio":0.4,"completion_ratio":3.5}],"group_ratio":{"default":1},"success":true}`))
		case "/api/status":
			_, _ = w.Write([]byte(`{"success":true,"data":{"quota_per_unit":500000}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer upstream.Close()

	siteID, _ := db.Site.Create(&domain.Site{Name: "价格站", BaseURL: "https://price.example", Platform: "new-api", Status: domain.StatusEnabled})
	if err := db.UpdateSiteProbeSourceWithAuto(siteID, "", "", "{}", true, true); err != nil {
		t.Fatalf("configure source: %v", err)
	}
	channelID, _ := db.Channel.Create(&domain.Channel{SiteID: &siteID, Name: "price-key", Status: domain.StatusEnabled})
	routeID, _ := db.Route.Create(&domain.Route{ModelPattern: "glm-5.3-flash", Enabled: true})
	memberID, _ := db.RouteMember.Create(&domain.RouteMember{RouteID: routeID, ChannelID: channelID, Enabled: true, Auto: true, Weight: 100})

	service := NewService(db, nil, nil)
	// EnabledSites resolves an auto source into (kind, url) before collecting;
	// this test calls the collector directly, so it passes what resolution
	// produces.
	if _, err := service.CollectSite(context.Background(), domain.Site{
		ID: siteID, Name: "价格站", BaseURL: upstream.URL, Platform: "new-api",
		ProbeSourceEnabled: true, ProbeAuto: true,
		ProbeSourceKind: SourceNewAPI, ProbeSourceURL: upstream.URL,
	}); err != nil {
		t.Fatalf("collect: %v", err)
	}

	// No traffic yet: the price row is informational and must not reach a low
	// verdict (that is what the missing availability means).
	report, err := service.Report(DefaultPolicy())
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if len(report.Rows) != 1 {
		t.Fatalf("rows = %+v, want the priced model", report.Rows)
	}
	if report.Rows[0].Verdict == VerdictLow || report.Rows[0].Traffic != nil {
		t.Fatalf("row without traffic = %+v, want no verdict from nothing", report.Rows[0])
	}

	// Now the gateway has relayed a handful of requests through that channel.
	insertTraffic(t, db, channelID, "glm-5.3-flash", 5, 4)
	report, err = service.Report(DefaultPolicy())
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	row := report.Rows[0]
	if row.Traffic == nil || row.Traffic.Samples != 5 || row.Traffic.Failures != 4 {
		t.Fatalf("traffic = %+v, want 4 failures out of 5", row.Traffic)
	}
	if row.AvailabilitySource != SourceTraffic || row.Verdict != VerdictLow {
		t.Fatalf("verdict/source = %q/%q, want low from traffic", row.Verdict, row.AvailabilitySource)
	}

	actions, err := service.Apply(context.Background(), ApplyRequest{DryRun: true})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(actions) != 1 || actions[0].Kind != "disable" {
		t.Fatalf("actions = %+v, want one disable", actions)
	}
	if !strings.Contains(actions[0].Reason, "traffic") && !strings.Contains(actions[0].Reason, "本站") {
		t.Fatalf("reason = %q, want it to say the evidence was our own traffic", actions[0].Reason)
	}

	// Healthy traffic floors at ok, and applying nothing leaves the member alone.
	// The window is cleared first: availability is an aggregate over everything
	// logged in it, so yesterday's failures are not "replaced" by new rows.
	if _, err := db.Exec(`DELETE FROM proxy_logs WHERE channel_id = ?`, channelID); err != nil {
		t.Fatalf("clear traffic: %v", err)
	}
	insertTraffic(t, db, channelID, "glm-5.3-flash", 8, 0)
	report, err = service.Report(DefaultPolicy())
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if report.Rows[0].Verdict != VerdictOK || report.Rows[0].AvailabilitySource != SourceTraffic {
		t.Fatalf("healthy traffic verdict = %q/%q, want ok from traffic", report.Rows[0].Verdict, report.Rows[0].AvailabilitySource)
	}
	if member, _ := db.RouteMember.GetByID(memberID); member == nil || !member.Enabled {
		t.Fatal("a dry run moved the member")
	}
}

// insertTraffic logs `failures` failed and `samples-failures` successful relay
// attempts for one (channel, model) pair, dated now.
func insertTraffic(t *testing.T, db *store.DB, channelID int64, model string, samples, failures int) {
	t.Helper()
	for index := 0; index < samples; index++ {
		status, brief := 200, ""
		if index < failures {
			status, brief = 502, "upstream_error"
		}
		if _, err := db.Exec(`INSERT INTO proxy_logs (request_id, channel_id, model, status, error_brief, first_byte_ms, created_at) VALUES (?, ?, ?, ?, ?, 120, datetime('now'))`,
			fmt.Sprintf("traffic-%d-%d", channelID, index), channelID, model, status, brief); err != nil {
			t.Fatalf("insert traffic: %v", err)
		}
	}
}

func TestMatchAllFoldsCatalogNamespaces(t *testing.T) {
	resolver := newMatchResolver(t)
	matches := resolver.MatchAll("glm-5.2")
	if len(matches) < 3 {
		t.Fatalf("matches = %+v, want the literal route plus both namespaced aliases", matches)
	}
	if matches[0].Route != "glm-5.2" || matches[0].Kind != MatchExact {
		t.Fatalf("first match = %+v, want the literal one first", matches[0])
	}
	seen := map[string]string{}
	for _, match := range matches {
		seen[match.Route] = match.Kind
	}
	if seen["cn:glm-5.2"] != MatchNamespace || seen["global:glm-5.2"] != MatchNamespace {
		t.Fatalf("namespaced aliases = %+v, want both matched as namespace hits", seen)
	}
	// A vendor-prefixed name still resolves through the member mapping first.
	vendor := resolver.MatchAll("z-ai/glm-5.2")
	if len(vendor) == 0 || vendor[0].Kind != MatchMemberReal {
		t.Fatalf("vendor matches = %+v, want the member mapping first", vendor)
	}
	// A name we do not serve stays unmatched, aliases or not.
	if got := resolver.MatchAll("kimi-k3"); len(got) != 0 {
		t.Fatalf("MatchAll(kimi-k3) = %+v, want none", got)
	}
}

// The unit trap this guards: published prices are USD per 1M tokens, the
// billing columns are USD per 1k.
// addSiteMember wires a second site (with its own fake status page) onto an
// existing route, so a test can compare a site that opted into auto-apply with
// one that did not.
func addSiteMember(t *testing.T, f *fixture, source *ratioSource, siteName, monitor, config string) (siteID, memberID int64) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		kumaHandler(t, int(source.up.Load()), int(source.down.Load()), map[string]string{"44": monitor})(w, r)
	}))
	t.Cleanup(server.Close)

	siteID, err := f.db.Site.Create(&domain.Site{Name: siteName, BaseURL: "https://b.example", Platform: "new-api", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatalf("create site: %v", err)
	}
	if err := f.db.UpdateSiteProbeSource(siteID, SourceUptimeKuma, server.URL+"/status/ai", config, true); err != nil {
		t.Fatalf("configure probe source: %v", err)
	}
	channelID, err := f.db.Channel.Create(&domain.Channel{SiteID: &siteID, Name: siteName + "-key", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	memberID, err = f.db.RouteMember.Create(&domain.RouteMember{
		RouteID: f.routeID, ChannelID: channelID, Enabled: true, Auto: true, Weight: 100,
	})
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
	return siteID, memberID
}

func (f *fixture) memberByID(t *testing.T, id int64) *domain.RouteMember {
	t.Helper()
	member, err := f.db.RouteMember.GetByID(id)
	if err != nil || member == nil {
		t.Fatalf("load member %d: %v", id, err)
	}
	return member
}

// Auto-apply is per site: the site that opted in acts on its own verdicts, the
// site that only asked for evidence stays read-only.
func TestAutoApplyRoundActsOnlyForSitesThatOptedIn(t *testing.T) {
	sourceA := &ratioSource{}
	sourceA.set(2, 8)
	f := newFixture(t, sourceA, "z-ai/glm-5.2", "glm-5.2")
	sourceB := &ratioSource{}
	sourceB.set(1, 9)
	_, memberB := addSiteMember(t, f, sourceB, "公益站B", "z-ai/glm-5.2", `{"auto_apply":true}`)

	for round := 0; round < 2; round++ {
		if ok, failed, err := f.service.CollectEnabledSites(context.Background()); err != nil || failed != 0 || ok != 2 {
			t.Fatalf("collect round %d: ok=%d failed=%d err=%v", round, ok, failed, err)
		}
	}
	actions, err := f.service.AutoApplyRound(context.Background())
	if err != nil {
		t.Fatalf("auto apply: %v", err)
	}
	if len(actions) != 1 || actions[0].ChannelName != "公益站B-key" || actions[0].Kind != "disable" {
		t.Fatalf("auto actions = %+v, want only the opted-in site's channel", actions)
	}
	optedIn := f.memberByID(t, memberB)
	if optedIn.Enabled || !optedIn.AutoDisabled {
		t.Fatalf("opted-in member = enabled:%v auto_disabled:%v, want parked", optedIn.Enabled, optedIn.AutoDisabled)
	}
	if memberA := f.member(t); !memberA.Enabled || memberA.AutoDisabled {
		t.Fatalf("read-only site's member = enabled:%v auto_disabled:%v, want untouched", memberA.Enabled, memberA.AutoDisabled)
	}
}

// A config blob the reader cannot parse must never be read as "act with
// defaults": the write path rejects one, but a restored backup or an older
// build can still leave one behind.
func TestAutoApplyRoundIgnoresUnreadableConfig(t *testing.T) {
	source := &ratioSource{}
	source.set(0, 10)
	f := newFixture(t, source, "z-ai/glm-5.2", "glm-5.2")
	site, err := f.db.Site.GetByID(f.siteID)
	if err != nil || site == nil {
		t.Fatalf("load site: %v", err)
	}
	// Keep the page the collector reads; only the config is unreadable.
	if err := f.db.UpdateSiteProbeSource(f.siteID, SourceUptimeKuma, site.ProbeSourceURL, "not json", true); err != nil {
		t.Fatalf("store malformed config: %v", err)
	}
	f.collectTimes(t, 2)

	actions, err := f.service.AutoApplyRound(context.Background())
	if err != nil {
		t.Fatalf("auto apply: %v", err)
	}
	if len(actions) != 0 {
		t.Fatalf("actions = %+v, want none for an unreadable config", actions)
	}
	if member := f.member(t); !member.Enabled {
		t.Fatal("an unreadable config disabled a member")
	}
}

func TestParseAndValidateSourceConfig(t *testing.T) {
	// Empty means "collect only", which is the default posture.
	config, ok := ParseSourceConfig("")
	if !ok || config.AutoApply {
		t.Fatalf("empty config = %+v ok=%v, want collect-only", config, ok)
	}
	if config.Policy != DefaultPolicy() {
		t.Fatalf("empty config policy = %+v, want defaults", config.Policy)
	}
	// A partial policy fills the rest from the defaults.
	config, ok = ParseSourceConfig(`{"auto_apply":true,"policy":{"ratio_threshold":0.5}}`)
	if !ok || !config.AutoApply || config.Policy.RatioThreshold != 0.5 || config.Policy.MinSamples != DefaultPolicy().MinSamples {
		t.Fatalf("partial config = %+v ok=%v", config, ok)
	}
	if _, ok := ParseSourceConfig("not json"); ok {
		t.Fatal("a malformed config must not read as ok")
	}
	if err := ValidateSourceConfig(`{"auto_apply":true}`); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	if err := ValidateSourceConfig(`{"autoapply":true}`); err == nil {
		t.Fatal("an unknown key must be rejected instead of silently ignored")
	}
	if err := ValidateSourceConfig(`{"policy":{"ratio_threshold":5}}`); err == nil {
		t.Fatal("an out-of-range threshold must be rejected")
	}
}

func TestQuoteToMemberPricesConvertsUnits(t *testing.T) {
	quote := adapters.PriceQuote{Mode: "token", InputPerMillion: 0.15, OutputPerMillion: 0.5, CacheReadPerMillion: 0.03}
	prompt, completion, cache, perRequest, ok := QuoteToMemberPrices(quote)
	if !ok || !closeEnough(prompt, 0.00015) || !closeEnough(completion, 0.0005) || !closeEnough(cache, 0.00003) || perRequest != 0 {
		t.Fatalf("token quote -> %v/%v/%v/%v ok=%v", prompt, completion, cache, perRequest, ok)
	}
	fixed := adapters.PriceQuote{Mode: "fixed", PerRequest: 0.02}
	if _, _, _, perRequest, ok := QuoteToMemberPrices(fixed); !ok || perRequest != 0.02 {
		t.Fatalf("fixed quote -> %v ok=%v", perRequest, ok)
	}
	if _, _, _, _, ok := QuoteToMemberPrices(adapters.PriceQuote{Mode: "token", Unparsed: true, InputPerMillion: 75}); ok {
		t.Fatal("an unparsed quote must not be adopted into the billing layer")
	}
}

func closeEnough(got, want float64) bool {
	diff := got - want
	if diff < 0 {
		diff = -diff
	}
	return diff < 1e-12
}

// A price source has no availability data at all: its samples must carry no
// sample count, so they can inform the operator without ever parking a member.
func TestPriceSourceStoresPricesWithoutAvailability(t *testing.T) {
	db, err := store.OpenTest(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/pricing":
			// Captured from a live New-API site: the real prices live in
			// billing_expr while model_ratio is a placeholder.
			_, _ = w.Write([]byte(`{"data":[
				{"model_name":"glm-5.3-flash","quota_type":0,"model_ratio":37.5,"completion_ratio":1,
				 "billing_mode":"tiered_expr","billing_expr":"tier(\"base\", p * 0.15 + c * 0.5 + cr * 0.03)"},
				{"model_name":"kimi-k3","quota_type":0,"model_ratio":37.5,"completion_ratio":1,
				 "billing_mode":"tiered_expr","billing_expr":"tier(\"base\", p * 3 + c * 15 + cr * 0.3)"}],
				"group_ratio":{"default":1},"success":true}`))
		case "/api/status":
			_, _ = w.Write([]byte(`{"success":true,"data":{"quota_per_unit":500000}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	siteID, _ := db.Site.Create(&domain.Site{Name: "价格站", Status: domain.StatusEnabled})
	if err := db.UpdateSiteProbeSource(siteID, SourceNewAPI, server.URL, "{}", true); err != nil {
		t.Fatalf("configure probe source: %v", err)
	}
	channelID, _ := db.Channel.Create(&domain.Channel{SiteID: &siteID, Name: "key", Status: domain.StatusEnabled})
	routeID, _ := db.Route.Create(&domain.Route{ModelPattern: "glm-5.3-flash", Enabled: true})
	memberID, _ := db.RouteMember.Create(&domain.RouteMember{RouteID: routeID, ChannelID: channelID, Enabled: true, Auto: true})

	service := NewService(db, nil, nil)
	site, _ := db.Site.GetByID(siteID)
	if _, err := service.CollectSite(context.Background(), *site); err != nil {
		t.Fatalf("collect: %v", err)
	}
	report, err := service.Report(DefaultPolicy())
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	// The site sells two models; we only serve one of them, so the other is
	// reported as unmatched (with its price) instead of being dropped.
	if len(report.Rows) != 1 {
		t.Fatalf("rows = %+v, want only the route we serve", report.Rows)
	}
	if len(report.Unmatched) != 1 || report.Unmatched[0].RawModel != "kimi-k3" {
		t.Fatalf("unmatched = %+v, want the model we do not serve", report.Unmatched)
	}
	if report.Unmatched[0].Price == nil || report.Unmatched[0].Price.InputPerMillion != 3 {
		t.Fatalf("unmatched price = %+v, want 3 USD/1M from the expression", report.Unmatched[0].Price)
	}
	for _, row := range append(report.Rows, Row{Route: report.Unmatched[0].RawModel, PriceOnly: true}) {
		if !row.PriceOnly {
			t.Fatalf("row %q is not marked price-only", row.Route)
		}
		if row.Verdict == VerdictLow {
			t.Fatalf("row %q reached a low verdict without availability data", row.Route)
		}
	}
	flash := report.Rows[0]
	if flash.Route != "glm-5.3-flash" || flash.Rounds[0].Price == nil {
		t.Fatalf("row = %+v, want the price attached", flash)
	}
	if flash.Rounds[0].Price.InputPerMillion != 0.15 {
		t.Fatalf("input price = %v, want 0.15 from the billing expression", flash.Rounds[0].Price.InputPerMillion)
	}
	if member, _ := db.RouteMember.GetByID(memberID); member == nil || !member.Enabled {
		t.Fatal("a price-only source moved a member")
	}
}

func TestAutoApplySitesDoesNotActOutsideManualSelection(t *testing.T) {
	source := &ratioSource{}
	source.set(1, 9)
	f := newFixture(t, source, "glm-5.2", "glm-5.2")
	other := &ratioSource{}
	other.set(1, 9)
	siteB, memberB := addSiteMember(t, f, other, "other", "glm-5.2", `{"auto_apply":true}`)
	for round := 0; round < 2; round++ {
		if _, _, err := f.service.CollectEnabledSites(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	for _, ids := range [][]int64{nil, {}, {f.siteID}, {-1}} {
		actions, err := f.service.AutoApplySites(context.Background(), ids)
		if err != nil || len(actions) != 0 {
			t.Fatalf("selection %v: actions=%v err=%v", ids, actions, err)
		}
		if !f.memberByID(t, memberB).Enabled {
			t.Fatal("unselected site changed")
		}
	}
	actions, err := f.service.AutoApplySites(context.Background(), []int64{siteB})
	if err != nil || len(actions) != 1 || f.memberByID(t, memberB).Enabled {
		t.Fatalf("selected site not applied: %v %v", actions, err)
	}
}

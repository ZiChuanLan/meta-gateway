package siteprobe

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/adapters"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

// watchbotRow is the shape a monitoring directory publishes: one row per
// (site × model × group), with the health it measured itself.
func directoryHandler(t *testing.T, rows []map[string]any) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body, err := json.Marshal(map[string]any{"rows": rows})
		if err != nil {
			t.Fatalf("marshal rows: %v", err)
		}
		_, _ = w.Write(body)
	}
}

// The directory reports a status-page subdomain for a site whose base URL is the
// bare domain, and carries entries for sites we do not route through at all.
func TestFetchExternalCatalogMatchesSitesByHostAndDropsUnknownOnes(t *testing.T) {
	directory := httptest.NewServer(directoryHandler(t, []map[string]any{
		{
			"siteName": "探针站", "siteUrl": "https://stat.p2.example/status/ai",
			"rawModelName": "glm-5.2", "groupName": "GLM", "successRatio": 0.87,
			"averageLatencyMs": 1200.75, "firstTokenMs": 400.25, "tokensPerSecond": 42.5,
			"serviceState": "degraded", "acquisitionState": "fresh", "observedAt": "2026-10-04T00:00:00Z",
		},
		{
			"siteName": "没见过", "siteUrl": "https://nobody.example/pricing",
			"rawModelName": "glm-5.2", "successRatio": 0.1,
		},
	}))
	defer directory.Close()

	sites := []domain.Site{
		{ID: 1, Name: "裸域站", BaseURL: "https://p2.example"},
		{ID: 2, Name: "另一个站", BaseURL: "https://other.example"},
	}
	readings, err := FetchExternalCatalog(context.Background(), directory.Client(), directory.URL, sites)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(readings) != 1 {
		t.Fatalf("readings = %+v, want only the site we route through", readings)
	}
	reading := readings[0]
	if reading.SiteID != 1 || reading.Source != SourceWatchbot {
		t.Fatalf("reading = %+v, want it tied to site 1 as third-party", reading)
	}
	// The subdomain matched the bare domain, and the health numbers survived.
	if reading.SiteHost != "stat.p2.example" || reading.RawModel != "glm-5.2" {
		t.Fatalf("reading = %+v", reading)
	}
	if reading.Ratio != 0.87 || reading.FirstTokenMS != 400 || reading.TokensPerSecond != 42.5 {
		t.Fatalf("health fields = %+v", reading)
	}
}

// Third-party evidence is the last resort: a site that reports its own numbers
// must not be overridden by a directory, and neither may our own traffic.
func TestVerdictPrefersFirstHandEvidenceOverTheDirectory(t *testing.T) {
	low := &ExternalReading{Source: SourceWatchbot, Ratio: 0.1}
	now := time.Now().UTC()
	badRound := []Round{{Ratio: 0.95, Samples: 10, UpCount: 10, ObservedAt: now}}

	verdict, _, _, source := evaluate(DefaultPolicy(), badRound, nil, low, now)
	if source != SourceSite || verdict == VerdictLow {
		t.Fatalf("site evidence lost to the directory: %q/%q", verdict, source)
	}
	traffic := &TrafficReading{Samples: 20, Failures: 1, Ratio: 0.95, WindowHours: 24}
	verdict, _, _, source = evaluate(DefaultPolicy(), nil, traffic, low, now)
	if source != SourceTraffic || verdict == VerdictLow {
		t.Fatalf("our own traffic lost to the directory: %q/%q", verdict, source)
	}
	// With nothing first-hand, the directory decides, and says so.
	verdict, _, _, source = evaluate(DefaultPolicy(), nil, nil, low, now)
	if source != SourceExternal || verdict != VerdictLow {
		t.Fatalf("directory evidence ignored: %q/%q", verdict, source)
	}
}

// The whole point of storing a third-party snapshot: a New-API price source has
// no health of its own, so without it every row reads "no samples".
func TestReportUsesDirectoryReadingsWhenNothingElseExists(t *testing.T) {
	db, err := store.OpenTest(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/pricing":
			_, _ = w.Write([]byte(`{"data":[{"model_name":"glm-5.2","model_ratio":0.4,"completion_ratio":2,"billing_expr":"tier(\"base\", p * 0.15 + c * 0.5)"}],"group_ratio":{"default":1},"success":true}`))
		case "/api/status":
			_, _ = w.Write([]byte(`{"success":true,"data":{"quota_per_unit":500000}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer upstream.Close()

	siteID, _ := db.Site.Create(&domain.Site{Name: "价格站", BaseURL: upstream.URL, Platform: "new-api", Status: domain.StatusEnabled})
	if err := db.UpdateSiteProbeSourceWithAuto(siteID, "", "", "{}", true, true); err != nil {
		t.Fatalf("configure source: %v", err)
	}
	channelID, _ := db.Channel.Create(&domain.Channel{SiteID: &siteID, Name: "price-key", Status: domain.StatusEnabled})
	routeID, _ := db.Route.Create(&domain.Route{ModelPattern: "cn:glm-5.2", Enabled: true})
	memberID, _ := db.RouteMember.Create(&domain.RouteMember{RouteID: routeID, ChannelID: channelID, Enabled: true, Auto: true, Weight: 100})

	service := NewService(db, nil, nil)
	if _, err := service.CollectSite(context.Background(), domain.Site{
		ID: siteID, Name: "价格站", BaseURL: upstream.URL, Platform: "new-api",
		ProbeSourceEnabled: true, ProbeAuto: true,
		ProbeSourceKind: SourceNewAPI, ProbeSourceURL: upstream.URL,
	}); err != nil {
		t.Fatalf("collect: %v", err)
	}
	if err := db.ReplaceSiteProbeExternal([]store.SiteProbeExternal{{
		Source: SourceWatchbot, SiteID: siteID, RawModel: "glm-5.2", Ratio: 0.31,
		ServiceState: "failed", AcquisitionState: "fresh", ObservedAt: "2026-10-04T00:00:00Z",
	}}); err != nil {
		t.Fatalf("store external: %v", err)
	}

	report, err := service.Report(DefaultPolicy())
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if len(report.Rows) != 1 {
		t.Fatalf("rows = %+v, want the priced model", report.Rows)
	}
	row := report.Rows[0]
	if row.External == nil || row.External.Ratio != 0.31 {
		t.Fatalf("row external = %+v, want the directory reading attached", row.External)
	}
	if row.AvailabilitySource != SourceExternal || row.Verdict != VerdictLow {
		t.Fatalf("verdict/source = %q/%q, want low from the directory", row.Verdict, row.AvailabilitySource)
	}
	// The model matched through its catalog namespace, so the reading reached
	// the row that actually serves it.
	if row.Match != MatchNamespace {
		t.Fatalf("match = %q, want namespace", row.Match)
	}

	actions, err := service.Apply(context.Background(), ApplyRequest{DryRun: true})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(actions) != 1 {
		t.Fatalf("actions = %+v, want one disable", actions)
	}
	if !strings.Contains(actions[0].Reason, "third-party") {
		t.Fatalf("reason = %q, want it to name the evidence as third-party", actions[0].Reason)
	}
	if member := service.memberFor(t, memberID); member == nil || !member.Enabled {
		t.Fatal("a dry run moved the member")
	}
}

// The directory's own freshness flag decides: a pair it is not collecting — or
// has never sampled — is not evidence, whatever its stored ratio says.
func TestDirectoryReadingsMustBeFresh(t *testing.T) {
	unseen := store.SiteProbeExternal{Source: SourceWatchbot, SiteID: 1, RawModel: "glm-5.2", Ratio: 0, ServiceState: "no_samples", AcquisitionState: "stale"}
	if externalReadingIsUsable(unseen) {
		t.Fatal("a reading the directory is not collecting was accepted as evidence")
	}
	blocked := store.SiteProbeExternal{Source: SourceWatchbot, SiteID: 1, RawModel: "glm-5.2", Ratio: 0.2, ServiceState: "failed", AcquisitionState: "challenge_pending"}
	if externalReadingIsUsable(blocked) {
		t.Fatal("a challenge-blocked reading was accepted as evidence")
	}
	neverSampled := store.SiteProbeExternal{Source: SourceWatchbot, SiteID: 1, RawModel: "glm-5.2", AcquisitionState: "fresh"}
	if externalReadingIsUsable(neverSampled) {
		t.Fatal("a pair with no measurement was accepted as evidence")
	}
	fresh := store.SiteProbeExternal{Source: SourceWatchbot, SiteID: 1, RawModel: "glm-5.2", Ratio: 0.2, ServiceState: "failed", AcquisitionState: "fresh"}
	if !externalReadingIsUsable(fresh) {
		t.Fatal("a fresh reading was rejected")
	}
	// The console regression: the directory's current bucket is empty, so
	// serviceState reads no_samples — while the directory's own dashboard still
	// headlines the window aggregate (measured: 96.87% over 58,466 requests) next
	// to a 「暂无样本」 badge. Judging that out hid a reading the operator could read
	// on the directory's page.
	betweenBuckets := store.SiteProbeExternal{Source: SourceWatchbot, SiteID: 19, RawModel: "deepseek-v4.1-flash",
		Ratio: 0.9687, ServiceState: "no_samples", AcquisitionState: "fresh", ObservedAt: "2026-10-14T09:00:00Z",
		CheckedAt: "2026-10-14T10:53:03.421Z", RequestCount: 58466, SuccessCount: 56635, FailureCount: 1807}
	if !externalReadingIsUsable(betweenBuckets) {
		t.Fatal("a fresh no-samples reading was rejected: the window aggregate is still the directory's own number")
	}
}

// The console prints the directory's own numbers — its ratio, its counts, the
// last sample it saw — and the only age this gateway judges is its own copy.
func TestThirdPartySeriesPrintsTheDirectoryNumbers(t *testing.T) {
	now := time.Now().UTC()
	reading := ExternalReading{
		Source: SourceWatchbot, Ratio: 0.9687,
		RequestCount: 58466, SuccessCount: 56635, FailureCount: 1807,
		ServiceState: "no_samples", AcquisitionState: "fresh",
		ObservedAt: now.Add(-3 * time.Hour).Format(time.RFC3339),
		SnapshotAt: now.Add(-2 * time.Minute),
	}
	series := thirdPartySeries(reading, nil, now.Add(-24*time.Hour), now)
	if series.Stale {
		t.Fatal("a reading whose snapshot was refreshed two minutes ago was called expired")
	}
	if series.Source != SourceWatchbot || series.Scope != "site_model" {
		t.Fatalf("series = %s/%s, want the third-party source", series.Source, series.Scope)
	}
	if series.Ratio == nil || *series.Ratio != 0.9687 {
		t.Fatalf("ratio = %v, want the directory's own", series.Ratio)
	}
	if series.Samples != 58466 || series.OK != 56635 || series.Failed != 1807 {
		t.Fatalf("counts = %d/%d/%d, want the window's totals", series.Samples, series.OK, series.Failed)
	}
	// last_at is the directory's own last sample: hours old for a pair whose
	// current bucket is empty, and shown as a time rather than as an expiry.
	if series.LastAt == nil || now.Sub(*series.LastAt) < 2*time.Hour {
		t.Fatalf("last_at = %v, want the directory's own last sample", series.LastAt)
	}
	// What does go stale is our copy: a snapshot nobody could refresh for longer
	// than a probe round is allowed to go stale.
	reading.SnapshotAt = now.Add(-3 * time.Hour)
	if !thirdPartySeries(reading, nil, now.Add(-24*time.Hour), now).Stale {
		t.Fatal("a snapshot three hours old was treated as current")
	}
}

// A verdict waits for a copy this gateway could renew; the row still shows the
// reading, so the operator sees what the directory says and that our copy of it
// stopped moving.
func TestVerdictsWaitForASnapshotWeCanRefresh(t *testing.T) {
	now := time.Now().UTC()
	if actionableExternal(&ExternalReading{Ratio: 0.1, SnapshotAt: now.Add(-3 * time.Hour)}, now) != nil {
		t.Fatal("a verdict acted on a snapshot that had not been refreshed for hours")
	}
	if actionableExternal(&ExternalReading{Ratio: 0.1}, now) != nil {
		t.Fatal("a reading with no snapshot time at all was treated as current")
	}
	if actionableExternal(&ExternalReading{Ratio: 0.1, SnapshotAt: now.Add(-time.Minute)}, now) == nil {
		t.Fatal("a reading from a fresh snapshot was dropped")
	}
	if actionableExternal(nil, now) != nil {
		t.Fatal("a missing reading became evidence")
	}
}

// The console has to be able to say when the snapshot last moved and why it
// stopped moving: a directory nobody can reach used to leave its readings ageing
// in silence, every rule that read them blaming the data instead.
func TestSyncExternalRecordsItsOutcome(t *testing.T) {
	db, err := store.OpenTest(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	siteID, err := db.Site.Create(&domain.Site{Name: "薄荷 API", BaseURL: "https://x666.me/", Platform: "new-api", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatalf("site: %v", err)
	}

	directory := httptest.NewServer(directoryHandler(t, []map[string]any{{
		"siteName": "薄荷 API", "siteUrl": "https://x666.me/", "rawModelName": "deepseek-v4.1-flash",
		"groupName": "coding", "serviceState": "no_samples", "acquisitionState": "fresh",
		"observedAt": "2026-10-14T09:00:00Z", "collectedAt": "2026-10-14T10:53:03.421Z",
		"successRatio": 0.9687, "requestCount": 58466, "successCount": 56635, "failureCount": 1807,
	}}))
	defer directory.Close()

	service := NewService(db, nil, nil)
	count, err := service.SyncExternal(context.Background(), directory.URL)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if count != 1 {
		t.Fatalf("readings = %d, want the one matched site", count)
	}
	state := service.ExternalSyncState()
	if state.SyncedAt.IsZero() || state.Readings != 1 || state.Error != "" || state.CatalogURL != directory.URL {
		t.Fatalf("state after a good sync = %+v", state)
	}
	// The counts and the directory's own check time survive the round trip, and
	// the snapshot carries the time this gateway wrote it.
	rows, err := db.ListSiteProbeExternal()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want the one reading", rows)
	}
	if rows[0].SiteID != siteID || rows[0].RequestCount != 58466 || rows[0].FailureCount != 1807 {
		t.Fatalf("stored reading = %+v", rows[0])
	}
	if rows[0].CheckedAt != "2026-10-14T10:53:03.421Z" || rows[0].SnapshotAt.IsZero() {
		t.Fatalf("stored times = checked %q snapshot %v", rows[0].CheckedAt, rows[0].SnapshotAt)
	}

	// A directory that answers 502 keeps the snapshot we have, and says so.
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer dead.Close()
	if _, err := service.SyncExternal(context.Background(), dead.URL); err == nil {
		t.Fatal("a directory answering 502 was accepted")
	}
	state = service.ExternalSyncState()
	if state.Error == "" || state.AttemptedAt.IsZero() {
		t.Fatalf("state after a failed sync = %+v, want the failure visible", state)
	}
	if state.SyncedAt.IsZero() || state.Readings != 1 {
		t.Fatalf("a failed sync erased the last good snapshot: %+v", state)
	}
	if rows, err = db.ListSiteProbeExternal(); err != nil || len(rows) != 1 {
		t.Fatalf("a failed sync replaced the snapshot: rows=%d err=%v", len(rows), err)
	}
}

// The directory reports one row per group, and the aggregate with the most
// requests behind it is the one that speaks for the pair.
func TestExternalIndexKeepsTheGroupWithTheMostEvidence(t *testing.T) {
	db, err := store.OpenTest(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	siteID, err := db.Site.Create(&domain.Site{Name: "薄荷 API", BaseURL: "https://x666.me/", Platform: "new-api", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatalf("site: %v", err)
	}
	if err := db.ReplaceSiteProbeExternal([]store.SiteProbeExternal{
		{Source: SourceWatchbot, SiteID: siteID, RawModel: "deepseek-v4.1-flash", GroupName: "codex",
			Ratio: 1, RequestCount: 3, SuccessCount: 3, ServiceState: "healthy", AcquisitionState: "fresh",
			ObservedAt: "2026-10-14T10:00:00Z", CheckedAt: "2026-10-14T10:53:03.421Z"},
		{Source: SourceWatchbot, SiteID: siteID, RawModel: "deepseek-v4.1-flash", GroupName: "coding",
			Ratio: 0.9687, RequestCount: 58466, SuccessCount: 56635, FailureCount: 1807,
			ServiceState: "no_samples", AcquisitionState: "fresh",
			ObservedAt: "2026-10-14T09:00:00Z", CheckedAt: "2026-10-14T10:53:03.421Z"},
		{Source: SourceWatchbot, SiteID: siteID, RawModel: "glm-5.2", Ratio: 0.9, RequestCount: 100,
			ServiceState: "degraded", AcquisitionState: "stale"},
	}); err != nil {
		t.Fatalf("store external: %v", err)
	}

	service := NewService(db, nil, nil)
	index, err := service.externalIndex()
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	reading, found := index[externalKey(siteID, "deepseek-v4.1-flash")]
	if !found {
		t.Fatal("the pair the directory is collecting is missing from the index")
	}
	if reading.RequestCount != 58466 || reading.SuccessCount != 56635 || reading.FailureCount != 1807 {
		t.Fatalf("reading = %+v, want the group carrying the window's traffic", reading)
	}
	if reading.Ratio != 0.9687 || reading.ServiceState != "no_samples" {
		t.Fatalf("reading = %+v, want the directory's own aggregate", reading)
	}
	if reading.SnapshotAt.IsZero() {
		t.Fatal("the reading carries no snapshot time: nothing would ever be judged current")
	}
	if _, found := index[externalKey(siteID, "glm-5.2")]; found {
		t.Fatal("a pair the directory is not collecting reached the index")
	}
}

// The price chain: the site's own published price, then the model catalog's list
// price when the site has none. The catalog number is what the billing layer
// would charge anyway, so it is shown — and never adopted per member, because
// that would only duplicate the same global value.
func TestCatalogPriceFillsTheGapTheSiteLeaves(t *testing.T) {
	db, err := store.OpenTest(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// A price source that publishes exactly one model's price.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/pricing":
			_, _ = w.Write([]byte(`{"data":[{"model_name":"glm-5.2","model_ratio":0.4,"completion_ratio":2}],"group_ratio":{"default":1},"success":true}`))
		case "/api/status":
			_, _ = w.Write([]byte(`{"success":true,"data":{"quota_per_unit":500000}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer upstream.Close()

	siteID, _ := db.Site.Create(&domain.Site{Name: "价格站", BaseURL: upstream.URL, Platform: "new-api", Status: domain.StatusEnabled})
	if err := db.UpdateSiteProbeSourceWithAuto(siteID, "", "", "{}", true, true); err != nil {
		t.Fatalf("configure source: %v", err)
	}
	channelID, _ := db.Channel.Create(&domain.Channel{SiteID: &siteID, Name: "price-key", Status: domain.StatusEnabled})
	for _, pattern := range []string{"glm-5.2", "kimi-k3"} {
		routeID, _ := db.Route.Create(&domain.Route{ModelPattern: pattern, Enabled: true})
		if _, err := db.RouteMember.Create(&domain.RouteMember{RouteID: routeID, ChannelID: channelID, Enabled: true, Auto: true, Weight: 100}); err != nil {
			t.Fatalf("member: %v", err)
		}
	}
	input, output := .3, 1.2
	if _, err := db.SavePriceQuote(domain.PriceQuoteSnapshot{ScopeKey: "catalog:kimi-k3", Source: "litellm", Purpose: "reference", Model: "kimi-k3", Status: "calculable", Unit: domain.QuoteUnit{Kind: "iso", Code: "USD"}, Values: domain.QuoteValues{Mode: "token", Input: &input, Output: &output}}); err != nil {
		t.Fatalf("catalog row: %v", err)
	}

	service := NewService(db, nil, nil)
	if _, err := service.CollectSite(context.Background(), domain.Site{
		ID: siteID, Name: "价格站", BaseURL: upstream.URL, Platform: "new-api",
		ProbeSourceEnabled: true, ProbeAuto: true,
		ProbeSourceKind: SourceNewAPI, ProbeSourceURL: upstream.URL,
	}); err != nil {
		t.Fatalf("collect: %v", err)
	}
	report, err := service.Report(DefaultPolicy())
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if len(report.Rows) != 2 {
		t.Fatalf("rows = %d, want both models routed through this site", len(report.Rows))
	}
	byRoute := map[string]Row{}
	for _, row := range report.Rows {
		byRoute[row.Route] = row
	}
	// The site published glm-5.2: its own price wins and the catalog stays out.
	glm := byRoute["glm-5.2"]
	if glm.ObservedPrice == nil || glm.CatalogPrice != nil {
		t.Fatalf("glm-5.2 price = observed:%+v catalog:%+v, want only the site's", glm.ObservedPrice, glm.CatalogPrice)
	}
	// The site published nothing for kimi-k3: the catalog fills the hole, in the
	// same per-1M USD shape, and marked as the catalog's number.
	kimi := byRoute["kimi-k3"]
	if kimi.ObservedPrice != nil {
		t.Fatalf("kimi-k3 observed = %+v, want none from the site", kimi.ObservedPrice)
	}
	if kimi.CatalogPrice == nil {
		t.Fatal("kimi-k3 has no catalog price: the fallback did not run")
	}
	if kimi.CatalogPrice.InputPerMillion != 0.3 || kimi.CatalogPrice.OutputPerMillion != 1.2 {
		t.Fatalf("catalog price = %+v, want per-1M 0.3/1.2 (stored per 1k)", kimi.CatalogPrice)
	}
	if kimi.CatalogPrice.Currency != "USD" {
		t.Fatalf("catalog currency = %q, want USD", kimi.CatalogPrice.Currency)
	}
}

// A wildcard route has no single catalog entry, so it must not borrow one.
func TestCatalogPriceSkipsWildcardRoutes(t *testing.T) {
	catalog := map[string]adapters.PriceQuote{
		"gpt-*": {Model: "gpt-*", InputPerMillion: 1},
	}
	if quote := catalogPriceFor("gpt-*", catalog); quote != nil {
		t.Fatalf("wildcard route borrowed a price: %+v", quote)
	}
	if quote := catalogPriceFor("", catalog); quote != nil {
		t.Fatalf("empty route borrowed a price: %+v", quote)
	}
	if quote := catalogPriceFor("glm-5.2", catalog); quote != nil {
		t.Fatalf("unknown model borrowed a price: %+v", quote)
	}
}

// A price in a currency we cannot convert must never reach the billing columns:
// they are dollars per 1k tokens.
func TestQuoteToMemberPricesRefusesNonUSD(t *testing.T) {
	cny := adapters.PriceQuote{Mode: "token", Currency: "CNY", InputPerMillion: 1.35, OutputPerMillion: 4.05}
	if _, _, _, _, ok := QuoteToMemberPrices(cny); ok {
		t.Fatal("a CNY quote was accepted into the USD billing columns")
	}
	usd := adapters.PriceQuote{Mode: "token", Currency: "USD", InputPerMillion: 0.15}
	if _, _, _, _, ok := QuoteToMemberPrices(usd); !ok {
		t.Fatal("a USD quote was refused")
	}
	// An undeclared currency is treated as USD: every measured site that omits
	// the field is a New-API deployment whose numbers are dollars.
	if _, _, _, _, ok := QuoteToMemberPrices(adapters.PriceQuote{Mode: "token", InputPerMillion: 0.15}); !ok {
		t.Fatal("an undeclared currency must still be adoptable")
	}
}

// A site declares the currency its own table is denominated in; that declaration
// is what the console prints, and it is never converted.
func TestPublicPricesSeparateNativeCreditFromDisplayCurrency(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/pricing":
			_, _ = w.Write([]byte(`{"data":[{"model_name":"glm-5.2","model_ratio":0.4,"completion_ratio":2}],"group_ratio":{"default":1},"success":true}`))
		case "/api/status":
			_, _ = w.Write([]byte(`{"success":true,"data":{"quota_per_unit":500000,"quota_display_type":"CNY","usd_exchange_rate":7.3,"custom_currency_symbol":"¥","display_in_currency":true}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer upstream.Close()

	snapshot, err := FetchPublicPrices(context.Background(), upstream.Client(), upstream.URL)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	quote, found := snapshot.Quotes["glm-5.2"]
	if !found {
		t.Fatalf("quotes = %+v", snapshot.Quotes)
	}
	if quote.Currency != "" || quote.Unit.Kind != "site_credit" || quote.Unit.DisplayCurrency != "CNY" || quote.Unit.DisplaySymbol != "¥" || quote.Unit.DisplayRate == nil || *quote.Unit.DisplayRate != 7.3 {
		t.Fatalf("native unit/display conflated: %+v", quote)
	}
	if _, _, _, _, ok := QuoteToMemberPrices(quote); ok {
		t.Fatal("native credit was silently copied as USD")
	}
	// Protocol amount is native credit; the cosmetic CNY view is 0.8×7.3.
	if quote.InputPerMillion != 0.8 {
		t.Fatalf("input = %v, want 0.8 in the site's own currency", quote.InputPerMillion)
	}
}

func (s *Service) memberFor(t *testing.T, id int64) *domain.RouteMember {
	t.Helper()
	member, err := s.db.RouteMember.GetByID(id)
	if err != nil {
		t.Fatalf("load member: %v", err)
	}
	return member
}

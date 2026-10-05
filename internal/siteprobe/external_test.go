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

	siteID, _ := db.Site.Create(&domain.Site{Name: "价格站", BaseURL: "https://price.example", Platform: "new-api", Status: domain.StatusEnabled})
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

// A directory that stopped seeing a pair must not park a channel: measured, its
// snapshot carries "no_samples" pairs with an observedAt days old and a ratio of
// 0 — the absence of a measurement, not a failed one.
func TestDirectoryReadingsMustBeFresh(t *testing.T) {
	stale := store.SiteProbeExternal{Source: SourceWatchbot, SiteID: 1, RawModel: "glm-5.2", Ratio: 0, ServiceState: "no_samples", AcquisitionState: "stale"}
	if externalReadingIsUsable(stale) {
		t.Fatal("a stale no-samples reading was accepted as evidence")
	}
	failed := store.SiteProbeExternal{Source: SourceWatchbot, SiteID: 1, RawModel: "glm-5.2", Ratio: 0, ServiceState: "failed", AcquisitionState: "challenge_pending"}
	if externalReadingIsUsable(failed) {
		t.Fatal("a challenge-blocked reading was accepted as evidence")
	}
	fresh := store.SiteProbeExternal{Source: SourceWatchbot, SiteID: 1, RawModel: "glm-5.2", Ratio: 0.2, ServiceState: "failed", AcquisitionState: "fresh"}
	if !externalReadingIsUsable(fresh) {
		t.Fatal("a fresh reading was rejected")
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

	siteID, _ := db.Site.Create(&domain.Site{Name: "价格站", BaseURL: "https://price.example", Platform: "new-api", Status: domain.StatusEnabled})
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
	if err := db.ModelMetadata.Upsert(&domain.ModelMetadata{
		ModelName: "kimi-k3", PricePromptPer1k: 0.0003, PriceCompletionPer1k: 0.0012,
	}); err != nil {
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
func TestPublicPricesCarryTheDeclaredCurrency(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/pricing":
			_, _ = w.Write([]byte(`{"data":[{"model_name":"glm-5.2","model_ratio":0.4,"completion_ratio":2}],"group_ratio":{"default":1},"success":true}`))
		case "/api/status":
			_, _ = w.Write([]byte(`{"success":true,"data":{"quota_per_unit":500000,"quota_display_type":"CNY","custom_currency_symbol":"¥","display_in_currency":true}}`))
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
	if quote.Currency != "CNY" || quote.CurrencySymbol != "¥" {
		t.Fatalf("quote currency = %q/%q, want CNY/¥", quote.Currency, quote.CurrencySymbol)
	}
	// The amount is in the declared currency: 0.4 × 1e6/500000 × 1 = ¥0.8.
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

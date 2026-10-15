package siteprobe

import (
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

// The console's observation row showed the site's own word and nothing else while
// the model page showed four readings for the same model, which is what made the
// panel look like it had no availability at all. This is the contract: one row
// carries our traffic, our probes, the site's own collection and the directory's
// reading, in that order, each as a series the panel can draw.
func TestObservationCarriesEveryReading(t *testing.T) {
	db, service, siteID, channelID := newEvidenceFixture(t)
	now := time.Now().UTC()
	model := "deepseek-v4.1-flash"

	// Two collections an hour apart, so the site's own series has a history to
	// draw instead of a single number.
	createProbeRun(t, db, siteID, now.Add(-2*time.Hour), store.SiteProbeSample{
		MonitorID: "m1", MonitorName: model, RawModel: model, GroupName: "default",
		ObservedAt: now.Add(-2 * time.Hour), Samples: 100, UpCount: 100, Ratio: 1, RatioKnown: true,
	})
	createProbeRun(t, db, siteID, now.Add(-time.Hour), store.SiteProbeSample{
		MonitorID: "m1", MonitorName: model, RawModel: model, GroupName: "default",
		ObservedAt: now.Add(-time.Hour), Samples: 50, UpCount: 45, Ratio: 0.9, RatioKnown: true,
	})
	insertSiteTraffic(t, db, channelID, model, 4)
	if err := db.InsertProbeResult(&store.ProbeResult{
		ChannelID: channelID, Model: model, UpstreamModel: model, OK: true, ProbedAt: now.Add(-30 * time.Minute),
	}); err != nil {
		t.Fatalf("insert probe result: %v", err)
	}
	if err := db.ReplaceSiteProbeExternal([]store.SiteProbeExternal{{
		Source: SourceWatchbot, SiteID: siteID, RawModel: model, GroupName: "default",
		Ratio: 0.971, RequestCount: 59391, SuccessCount: 57677, FailureCount: 1689,
		AcquisitionState: "fresh", ObservedAt: now.Add(-time.Hour).Format(time.RFC3339),
		CheckedAt: now.Format(time.RFC3339),
	}}); err != nil {
		t.Fatalf("replace external snapshot: %v", err)
	}
	if err := db.AppendSiteProbeExternalHistory([]store.ExternalHistoryPoint{
		{SiteID: siteID, Source: SourceWatchbot, RawModel: model, Ratio: 0.99,
			RequestCount: 40000, SuccessCount: 39600, FailureCount: 400, At: now.Add(-2 * time.Hour)},
		{SiteID: siteID, Source: SourceWatchbot, RawModel: model, Ratio: 0.971,
			RequestCount: 59391, SuccessCount: 57677, FailureCount: 1689, At: now.Add(-time.Hour)},
	}); err != nil {
		t.Fatalf("append external history: %v", err)
	}

	result, err := service.Observations(24)
	if err != nil {
		t.Fatalf("observations: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("items = %d, want the one collected model", len(result.Items))
	}
	item := result.Items[0]
	sources := make([]string, 0, len(item.Evidence))
	bySource := map[string]*store.AvailabilitySeries{}
	for _, series := range item.Evidence {
		sources = append(sources, series.Source)
		bySource[series.Source] = series
	}
	for _, want := range []string{SourceTraffic, probeSource, SourceSite, SourceWatchbot} {
		if bySource[want] == nil {
			t.Fatalf("sources = %v, missing %s", sources, want)
		}
	}
	if sources[0] != SourceTraffic || sources[1] != probeSource || sources[2] != SourceSite || sources[3] != SourceWatchbot {
		t.Fatalf("sources = %v, want traffic, probe, site, watchbot in that order", sources)
	}
	// The cell quotes our own traffic: it is the reading with the most behind it
	// and the one the operator can act on. The site's own word stays in evidence.
	if item.Primary == nil || item.Primary.Source != SourceTraffic {
		t.Fatalf("primary = %v, want the traffic reading", item.Primary)
	}

	// The site's series draws one cell per collection, and its totals stay the
	// newest reading's own numbers: adding up a source's published window across
	// collections would print a total no site ever reported.
	site := bySource[SourceSite]
	if filled := filledBuckets(site); filled != 2 {
		t.Fatalf("site buckets with data = %d, want one per collection", filled)
	}
	if site.Samples != 50 || site.OK != 45 {
		t.Fatalf("site totals = %d/%d, want the newest collection's 45/50", site.OK, site.Samples)
	}
	if site.Ratio == nil || *site.Ratio != 0.9 {
		t.Fatalf("site ratio = %v, want the newest collection's 0.9", site.Ratio)
	}

	// The directory's series is our own log of what it said: one cell per pull,
	// with the reading's own counts behind it.
	third := bySource[SourceWatchbot]
	if filled := filledBuckets(third); filled != 2 {
		t.Fatalf("third-party buckets with data = %d, want one per pull", filled)
	}
	if third.Samples != 59391 || third.OK != 57677 {
		t.Fatalf("third-party totals = %d/%d, want the newest reading's own numbers", third.OK, third.Samples)
	}
}

// A reading that publishes only a percentage still gets a cell: the cell is drawn
// from ok over samples, and one reading is exactly one sample. It is green when the
// directory's own number is above the band this console treats as broken, red
// below — never a count nobody published.
func TestRatioOnlyReadingStillGetsACell(t *testing.T) {
	db, service, siteID, channelID := newEvidenceFixture(t)
	now := time.Now().UTC()
	model := "glm-5.3-flash"
	createProbeRun(t, db, siteID, now.Add(-time.Hour), store.SiteProbeSample{
		MonitorID: "m1", MonitorName: model, RawModel: model, GroupName: "default",
		ObservedAt: now.Add(-time.Hour), Samples: 10, UpCount: 10, Ratio: 1, RatioKnown: true,
	})
	insertSiteTraffic(t, db, channelID, model, 2)
	if err := db.ReplaceSiteProbeExternal([]store.SiteProbeExternal{{
		Source: SourceWatchbot, SiteID: siteID, RawModel: model, Ratio: 0.97,
		AcquisitionState: "fresh", ObservedAt: now.Format(time.RFC3339), CheckedAt: now.Format(time.RFC3339),
	}}); err != nil {
		t.Fatalf("replace external snapshot: %v", err)
	}
	if err := db.AppendSiteProbeExternalHistory([]store.ExternalHistoryPoint{
		{SiteID: siteID, RawModel: model, Ratio: 0.97, At: now.Add(-2 * time.Hour)},
		{SiteID: siteID, RawModel: model, Ratio: 0.4, At: now.Add(-time.Hour)},
	}); err != nil {
		t.Fatalf("append external history: %v", err)
	}
	result, err := service.Observations(24)
	if err != nil {
		t.Fatalf("observations: %v", err)
	}
	var third *store.AvailabilitySeries
	for _, series := range result.Items[0].Evidence {
		if series.Source == SourceWatchbot {
			third = series
		}
	}
	if third == nil {
		t.Fatal("the directory's reading is missing from the row")
	}
	states := cellStates(third)
	if len(states) != 2 || states[0] != "ok" || states[1] != "failed" {
		t.Fatalf("cell states = %v, want a green cell for 97%% and a red one for 40%%", states)
	}
	// The counts behind those cells are the readings themselves, not invented
	// request totals.
	for _, bucket := range third.Buckets {
		if bucket.Samples > 1 {
			t.Fatalf("bucket counts = %d samples, want one reading per cell", bucket.Samples)
		}
	}
}

// newEvidenceFixture opens a store with one site and one channel on it — the two
// things every reading the panel shows is keyed by.
func newEvidenceFixture(t *testing.T) (*store.DB, *Service, int64, int64) {
	t.Helper()
	db, err := store.OpenTest(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	siteID, err := db.Site.Create(&domain.Site{
		Name: "A", BaseURL: "https://a.example", Platform: "new-api", Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatalf("create site: %v", err)
	}
	if err := db.UpdateSiteProbeSource(siteID, SourceNewAPI, "https://a.example", "{}", true); err != nil {
		t.Fatalf("configure probe source: %v", err)
	}
	channelID, err := db.Channel.Create(&domain.Channel{SiteID: &siteID, Name: "A-key", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	service := NewService(db, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	return db, service, siteID, channelID
}

// createProbeRun stores one collection: a finished round holding a single sample.
// A round is what the panel's "latest observation" comes from, so the fixture
// needs one per collection, exactly as the collector writes them.
func createProbeRun(t *testing.T, db *store.DB, siteID int64, at time.Time, sample store.SiteProbeSample) int64 {
	t.Helper()
	runID, err := db.CreateSiteProbeRun(siteID, SourceNewAPI, at)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	sample.RunID = runID
	sample.SiteID = siteID
	if err := db.InsertSiteProbeSamples([]store.SiteProbeSample{sample}); err != nil {
		t.Fatalf("insert sample: %v", err)
	}
	if err := db.FinishSiteProbeRun(runID, store.SiteProbeRunOK, 1, ""); err != nil {
		t.Fatalf("finish run: %v", err)
	}
	return runID
}

// insertSiteTraffic logs successful relay attempts for one (channel, model) pair,
// with the upstream name the proxy records — the series is keyed by what we
// actually sent, not by the name the client asked for.
func insertSiteTraffic(t *testing.T, db *store.DB, channelID int64, model string, samples int) {
	t.Helper()
	for index := 0; index < samples; index++ {
		if _, err := db.Exec(`INSERT INTO proxy_logs (request_id, channel_id, model, upstream_model, status,
			completion_outcome, request_kind, path, created_at)
			VALUES (?, ?, ?, ?, 200, 'ok', 'traffic', 'chat/completions', datetime('now'))`,
			fmt.Sprintf("evidence-%s-%d", model, index), channelID, model, model); err != nil {
			t.Fatalf("insert traffic: %v", err)
		}
	}
}

func filledBuckets(series *store.AvailabilitySeries) int {
	count := 0
	for _, bucket := range series.Buckets {
		if bucket.Samples > 0 {
			count++
		}
	}
	return count
}

// cellStates renders what the console would colour each filled cell: the
// frontend's own bands, so a test asserting "green" and a panel drawing green
// cannot drift apart.
func cellStates(series *store.AvailabilitySeries) []string {
	out := []string{}
	for _, bucket := range series.Buckets {
		if bucket.Samples == 0 {
			continue
		}
		rate := float64(bucket.OK) / float64(bucket.Samples)
		switch {
		case bucket.Failed == 0 || rate >= 0.95:
			out = append(out, "ok")
		case rate >= 0.8:
			out = append(out, "mixed")
		default:
			out = append(out, "failed")
		}
	}
	return out
}

// probeSource is the source name the store writes for our own probes; the console
// labels it with its own translation key.
const probeSource = "probe"

// A site's price table lists models it publishes no health for (its own monitors
// have no samples for them). That absence must not be drawn as a red cell: the
// panel would be reporting a failure the source never reported.
func TestPriceOnlyCollectionDrawsNoCell(t *testing.T) {
	db, service, siteID, channelID := newEvidenceFixture(t)
	now := time.Now().UTC()
	model := "gpt-5.5"
	createProbeRun(t, db, siteID, now.Add(-time.Hour), store.SiteProbeSample{
		MonitorID: "m9", MonitorName: model, RawModel: model, GroupName: "default",
		ObservedAt: now.Add(-time.Hour),
		Price:      store.SiteProbePrice{Mode: "fixed", PerRequest: 0.01},
	})
	insertSiteTraffic(t, db, channelID, model, 3)

	result, err := service.Observations(24)
	if err != nil {
		t.Fatalf("observations: %v", err)
	}
	item := result.Items[0]
	if item.Series.Samples != 0 || item.Series.Ratio != nil {
		t.Fatalf("site series = %d samples ratio %v, want no reading at all", item.Series.Samples, item.Series.Ratio)
	}
	if filled := filledBuckets(item.Series); filled != 0 {
		t.Fatalf("site buckets with data = %d, want none for a model the site reports no health for", filled)
	}
	// The traffic reading is still the one the row quotes: something was measured.
	if item.Primary == nil || item.Primary.Source != SourceTraffic {
		t.Fatalf("primary = %v, want the traffic reading", item.Primary)
	}
}

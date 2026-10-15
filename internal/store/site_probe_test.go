package store_test

import (
	"strings"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

// TestPruneSiteProbeDropsOldRoundsAndSamples pins the retention contract: a
// round past the window goes together with its samples (the two tables share no
// foreign key, so an orphaned sample would otherwise stay countable forever),
// while a round inside the window is untouched.
func TestPruneSiteProbeDropsOldRoundsAndSamples(t *testing.T) {
	db := openTestDB(t)
	site, err := db.Site.Create(&domain.Site{Name: "probe-site", BaseURL: "https://a.example", Platform: "new-api", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	// One round well past the window, one fresh round, each with a sample.
	oldStarted := time.Now().UTC().AddDate(0, 0, -30).Format("2006-01-02 15:04:05")
	oldRun, err := db.Exec(`INSERT INTO site_probe_runs (site_id, source_kind, started_at, status, monitor_count) VALUES (?, 'uptime_kuma', ?, 'ok', 1)`, site, oldStarted)
	if err != nil {
		t.Fatal(err)
	}
	oldRunID, _ := oldRun.LastInsertId()
	if _, err := db.Exec(`INSERT INTO site_probe_samples (run_id, site_id, monitor_name, raw_model, observed_at, samples, up_count, ratio) VALUES (?, ?, 'glm-5.2', 'glm-5.2', ?, 10, 9, 0.9)`,
		oldRunID, site, oldStarted); err != nil {
		t.Fatal(err)
	}
	freshRunID, err := db.CreateSiteProbeRun(site, "uptime_kuma", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := db.InsertSiteProbeSamples([]store.SiteProbeSample{{
		RunID: freshRunID, SiteID: site, MonitorID: "44", MonitorName: "glm-5.2",
		RawModel: "glm-5.2", ObservedAt: time.Now().UTC(), Samples: 10, UpCount: 10, Ratio: 1,
	}}); err != nil {
		t.Fatal(err)
	}

	runs, samples, err := db.PruneSiteProbe(7)
	if err != nil {
		t.Fatal(err)
	}
	if runs != 1 || samples != 1 {
		t.Fatalf("pruned runs=%d samples=%d, want 1 and 1", runs, samples)
	}
	remainingRuns, err := db.ListSiteProbeRuns(site, 10)
	if err != nil || len(remainingRuns) != 1 || remainingRuns[0].ID != freshRunID {
		t.Fatalf("remaining runs = %+v (err %v), want only the fresh round %d", remainingRuns, err, freshRunID)
	}
	remainingSamples, err := db.ListSiteProbeSamples(freshRunID)
	if err != nil || len(remainingSamples) != 1 {
		t.Fatalf("fresh samples = %+v (err %v), want the one sample kept", remainingSamples, err)
	}
	// 0 disables the pruner rather than deleting everything.
	if runs, samples, err := db.PruneSiteProbe(0); err != nil || runs != 0 || samples != 0 {
		t.Fatalf("PruneSiteProbe(0) = %d/%d err=%v, want a no-op", runs, samples, err)
	}
	// A second pass has nothing left to do.
	if runs, samples, err := db.PruneSiteProbe(7); err != nil || runs != 0 || samples != 0 {
		t.Fatalf("second pass = %d/%d err=%v, want nothing", runs, samples, err)
	}
}

// TestGCDeletesOrphanedProbeRows covers the other direction: a deleted site
// takes its probe history with it through the regular orphan sweep.
func TestGCDeletesOrphanedProbeRows(t *testing.T) {
	db := openTestDB(t)
	// FK off so an orphan can exist; the sweep exists because rows like this
	// arrive from restores and from older builds that did not enforce FKs.
	if _, err := db.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO site_probe_runs (site_id, source_kind, started_at, status) VALUES (9999, 'newapi', datetime('now'), 'ok')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO site_probe_samples (run_id, site_id, monitor_name, raw_model, observed_at) VALUES (9999, 9999, 'x', 'x', datetime('now'))`); err != nil {
		t.Fatal(err)
	}
	// A live site's round must survive the sweep.
	site, err := db.Site.Create(&domain.Site{Name: "live-site", BaseURL: "https://b.example", Platform: "new-api", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	liveRunID, err := db.CreateSiteProbeRun(site, "newapi", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := db.InsertSiteProbeSamples([]store.SiteProbeSample{{
		RunID: liveRunID, SiteID: site, MonitorName: "glm-5.2", RawModel: "glm-5.2",
		ObservedAt: time.Now().UTC(), Price: store.SiteProbePrice{Mode: "token", InputPerMillion: 0.15},
	}}); err != nil {
		t.Fatal(err)
	}

	res, err := db.GC()
	if err != nil {
		t.Fatal(err)
	}
	if res.SiteProbeRuns != 1 || res.SiteProbeSamples != 1 {
		t.Fatalf("gc deleted runs=%d samples=%d, want 1 and 1", res.SiteProbeRuns, res.SiteProbeSamples)
	}
	kept, err := db.ListSiteProbeSamples(liveRunID)
	if err != nil || len(kept) != 1 {
		t.Fatalf("live samples = %+v (err %v), want the row kept", kept, err)
	}
}

// TestRecentSamplesReadIsSiteScopedAndIndexOnly pins the two properties the
// console's observation panel depends on.
//
// Scoped: building the report rows walks every collected sample of the sites it
// was asked about, so the panel — which shows one site at a time — asks for one
// site, and the read has to narrow.
//
// Index-only: the per-sample partition probe is what made that read cost 9.9s
// of CPU on a 214k-sample production database (the probe visited the wide
// sample row for every candidate). idx_site_probe_samples_partition answers it
// from the index; without it the panel goes back to burning seconds per page
// load, silently.
func TestRecentSamplesReadIsSiteScopedAndIndexOnly(t *testing.T) {
	db := openTestDB(t)
	first, err := db.Site.Create(&domain.Site{Name: "first", BaseURL: "https://first.example", Platform: "new-api", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	second, err := db.Site.Create(&domain.Site{Name: "second", BaseURL: "https://second.example", Platform: "new-api", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	for _, site := range []int64{first, second} {
		runID, err := db.CreateSiteProbeRun(site, "newapi", time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		if err := db.InsertSiteProbeSamples([]store.SiteProbeSample{{
			RunID: runID, SiteID: site, MonitorID: "1", MonitorName: "glm-5.2",
			RawModel: "glm-5.2", GroupName: "default", ObservedAt: time.Now().UTC(), Samples: 5, UpCount: 5, Ratio: 1,
		}}); err != nil {
			t.Fatal(err)
		}
		if err := db.FinishSiteProbeRun(runID, store.SiteProbeRunOK, 1, ""); err != nil {
			t.Fatal(err)
		}
	}

	all, err := db.ListRecentSamplesBySite(3, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("unscoped read returned %d sites, want both", len(all))
	}
	scoped, err := db.ListRecentSamplesBySite(3, []int64{first})
	if err != nil {
		t.Fatal(err)
	}
	if len(scoped) != 1 || len(scoped[first]) != 1 {
		t.Fatalf("scoped read = %v, want only site %d", scoped, first)
	}
	if len(scoped[second]) != 0 {
		t.Fatalf("scoped read still carries site %d", second)
	}

	rows, err := db.Query(`EXPLAIN QUERY PLAN SELECT 1 FROM site_probe_samples s
		WHERE s.site_id=? AND s.raw_model=? AND s.monitor_id=? AND s.group_name=? AND s.source_key=? AND s.run_id=?`,
		first, "glm-5.2", "1", "default", "key", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	detail := ""
	for rows.Next() {
		var id, parent, notUsed int
		var line string
		if err := rows.Scan(&id, &parent, &notUsed, &line); err != nil {
			t.Fatal(err)
		}
		detail += line
	}
	if !strings.Contains(detail, "COVERING INDEX idx_site_probe_samples_partition") {
		t.Fatalf("partition lookup plan = %q, want the covering index", detail)
	}
}

// TestExternalHistoryKeepsOneReadingPerHour pins the shape of the directory's
// history: one row per (pair, hour), the hour's newest reading winning it, because
// the directory publishes a rolling-window total that must not be multiplied by
// the number of pulls it took to see it.
func TestExternalHistoryKeepsOneReadingPerHour(t *testing.T) {
	db := openTestDB(t)
	hour := time.Now().UTC().Truncate(time.Hour)
	points := []store.ExternalHistoryPoint{
		{SiteID: 3, RawModel: "glm-5.2", Ratio: 0.9, RequestCount: 100, SuccessCount: 90,
			At: hour.Add(5 * time.Minute)},
		{SiteID: 3, RawModel: "glm-5.2", Ratio: 0.95, RequestCount: 140, SuccessCount: 133,
			At: hour.Add(20 * time.Minute)},
		{SiteID: 3, RawModel: "glm-5.2", Ratio: 0.5, RequestCount: 4, SuccessCount: 2,
			At: hour.Add(-90 * time.Minute)},
	}
	if err := db.AppendSiteProbeExternalHistory(points); err != nil {
		t.Fatal(err)
	}
	history, err := db.ListSiteProbeExternalHistory(time.Now().UTC().Add(-24 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 {
		t.Fatalf("history = %d rows (%+v), want one per hour", len(history), history)
	}
	newest := history[len(history)-1]
	if newest.Ratio != 0.95 || newest.RequestCount != 140 {
		t.Fatalf("newest = %+v, want the hour's newest reading", newest)
	}

	// A reading past the retention window is the pruner's business, like the rest
	// of the probe history.
	if err := db.AppendSiteProbeExternalHistory([]store.ExternalHistoryPoint{
		{SiteID: 3, RawModel: "ancient", Ratio: 0.1, At: time.Now().UTC().AddDate(0, 0, -30)},
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.PruneSiteProbe(7); err != nil {
		t.Fatal(err)
	}
	kept, err := db.ListSiteProbeExternalHistory(time.Now().UTC().AddDate(0, 0, -365))
	if err != nil {
		t.Fatal(err)
	}
	for _, point := range kept {
		if point.RawModel == "ancient" {
			t.Fatal("a reading past the retention window survived the prune")
		}
	}
}

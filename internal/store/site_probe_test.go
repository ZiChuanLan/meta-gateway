package store_test

import (
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

package siteprobe

import (
	"context"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
	"testing"
	"time"
)

func TestObservationsUseLatestSnapshotAndDeduplicateHeartbeat(t *testing.T) {
	source := &ratioSource{}
	source.set(5, 0)
	f := newFixture(t, source, "glm", "glm")
	f.collect(t)
	var oldMonitor, oldTime string
	if err := f.db.QueryRow(`SELECT monitor_id,probed_at FROM site_probe_heartbeats ORDER BY probed_at LIMIT 1`).Scan(&oldMonitor, &oldTime); err != nil {
		t.Fatal(err)
	}
	var before int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM site_probe_heartbeats`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := f.db.InsertSiteHeartbeats([]store.SiteHeartbeat{{SiteID: f.siteID, SourceKey: sourceIdentity(*mustSite(t, f)), MonitorID: oldMonitor, RawModel: "glm", At: mustTime(t, oldTime), Status: 1}}); err != nil {
		t.Fatal(err)
	}
	var after int
	f.db.QueryRow(`SELECT COUNT(*) FROM site_probe_heartbeats`).Scan(&after)
	if before != after {
		t.Fatalf("duplicate heartbeat changed %d to %d", before, after)
	}
	run, err := f.db.CreateSiteProbeRun(f.siteID, SourceUptimeKuma, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.db.InsertSiteProbeSamples([]store.SiteProbeSample{{RunID: run, SiteID: f.siteID, RawModel: "replacement", MonitorID: "new", ObservedAt: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	f.db.FinishSiteProbeRun(run, store.SiteProbeRunOK, 1, "")
	result, err := f.service.Observations(24)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || result.Items[0].RawModel != "replacement" {
		t.Fatalf("snapshot=%+v", result.Items)
	}
	failed, _ := f.db.CreateSiteProbeRun(f.siteID, SourceUptimeKuma, time.Now())
	f.db.FinishSiteProbeRun(failed, store.SiteProbeRunFailed, 0, "offline")
	result, err = f.service.Observations(24)
	if err != nil || len(result.Items) != 1 {
		t.Fatalf("failed collection lost snapshot: %v %+v", err, result)
	}
}
func mustSite(t *testing.T, f *fixture) *domain.Site {
	t.Helper()
	site, err := f.db.Site.GetByID(f.siteID)
	if err != nil {
		t.Fatal(err)
	}
	return site
}
func mustTime(t *testing.T, value string) time.Time {
	t.Helper()
	at, err := time.Parse("2006-01-02 15:04:05", value)
	if err != nil {
		t.Fatal(err)
	}
	return at
}

func TestAvailabilityKeepsSiblingModelsSeparate(t *testing.T) {
	source := &ratioSource{}
	source.set(5, 0)
	f := newFixture(t, source, "one", "alias")
	first := f.member(t)
	first.MappingJSON = `{"real":"one"}`
	if err := f.db.RouteMember.Update(first); err != nil {
		t.Fatal(err)
	}
	secondID, err := f.db.RouteMember.Create(&domain.RouteMember{RouteID: f.routeID, ChannelID: f.channelID, Enabled: true, Weight: 100, MappingJSON: `{"real":"two"}`})
	if err != nil {
		t.Fatal(err)
	}
	for _, log := range []domain.ProxyLog{
		{MemberID: f.memberID, RouteID: f.routeID, ChannelID: f.channelID, Model: "alias", UpstreamModel: "one", Status: 200},
		{MemberID: secondID, RouteID: f.routeID, ChannelID: f.channelID, Model: "alias", UpstreamModel: "two", Status: 503},
	} {
		if _, err := f.db.ProxyLog.Insert(&log); err != nil {
			t.Fatal(err)
		}
	}
	report, err := f.service.Availability([]int64{f.routeID}, 0, "", 24)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Members) != 2 {
		t.Fatalf("members=%+v", report.Members)
	}
	ratios := map[string]float64{}
	for _, m := range report.Members {
		if m.Primary == nil || m.Primary.Ratio == nil {
			t.Fatalf("missing primary: %+v", m)
		}
		ratios[m.Model] = *m.Primary.Ratio
	}
	if ratios["one"] != 1 || ratios["two"] != 0 {
		t.Fatalf("mixed sibling models: %v", ratios)
	}
	first.MappingJSON = `{"real":"changed"}`
	f.db.RouteMember.Update(first)
	report, err = f.service.Availability([]int64{f.routeID}, 0, "", 24)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range report.Members {
		if m.MemberID == f.memberID && m.Primary != nil {
			t.Fatalf("old model leaked into new mapping: %+v", m)
		}
	}
}
func TestSiteModelDoesNotOwnOtherVariantOnSameRoute(t *testing.T) {
	source := &ratioSource{}
	source.set(5, 0)
	f := newFixture(t, source, "one", "alias")
	first := f.member(t)
	first.MappingJSON = `{"real":"one"}`
	f.db.RouteMember.Update(first)
	secondID, err := f.db.RouteMember.Create(&domain.RouteMember{RouteID: f.routeID, ChannelID: f.channelID, Enabled: true, Weight: 100, MappingJSON: `{"real":"two"}`})
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := BuildResolver(f.db)
	if err != nil {
		t.Fatal(err)
	}
	members := resolver.MembersForModel("alias", f.siteID, "one")
	if len(members) != 1 || members[0].MemberID == secondID {
		t.Fatalf("unrelated alias attached: %+v", members)
	}
	_ = context.Background()
}

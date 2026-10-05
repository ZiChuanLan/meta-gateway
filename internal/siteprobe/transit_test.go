package siteprobe

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

// transitStub serves the discovery file plus a V1 snapshot in the field names
// the public-transit fork documents.
func transitStub(t *testing.T, availability string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/ai-transit.json":
			_, _ = w.Write([]byte(`{"schema_version":"ai-transit.v1","system":"sub2api",
				"snapshot_url":"` + serverURL(r) + `/api/public/transit/snapshot",
				"homepage_url":"` + serverURL(r) + `/public/transit",
				"generated_at":"2026-10-04T00:00:00Z"}`))
		case "/api/public/transit/snapshot", "/api/public/transit/v1/snapshot":
			_, _ = w.Write([]byte(`{"schema_version":"ai-transit.v1","system":"sub2api","mode":"v1",
				"groups":[{"name":"default","platform":"claude","group_rate_multiplier":1,"model_count":2}],
				"models":[
					{"name":"claude-sonnet-4-6","billing_mode":"token","input_price":3,"output_price":15,"cache_input_price":0.3,"currency":"USD","groups":["default"]},
					{"name":"kimi-k3","billing_mode":"fixed","per_request_price":0.02,"currency":"USD"}],
				"monitors":[{"name":"claude-sonnet-4-6","status":"up","availability":` + availability + `,"latency_ms":830,"group":"default"}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func serverURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// The happy path: paste any page of the site, the discovery file is found at the
// host root, and the snapshot turns into prices for every published model plus
// availability for every monitored one.
func TestFetchTransitReadsDiscoveryAndSnapshot(t *testing.T) {
	server := transitStub(t, "0.87")
	defer server.Close()

	discovery, snapshotURL, err := ResolveTransitDiscovery(context.Background(), server.Client(), server.URL+"/public/transit")
	if err != nil {
		t.Fatalf("discovery: %v", err)
	}
	if discovery.System != "sub2api" || snapshotURL != server.URL+"/api/public/transit/snapshot" {
		t.Fatalf("discovery = %+v url %q", discovery, snapshotURL)
	}
	snapshot, mode, err := FetchTransit(context.Background(), server.Client(), snapshotURL)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if mode != "v1" || len(snapshot.Models) != 2 {
		t.Fatalf("mode=%q models=%d", mode, len(snapshot.Models))
	}

	samples := TransitSamples(snapshot, 1, 2, time.Now().UTC())
	if len(samples) != 2 {
		t.Fatalf("samples = %d, want one per published model", len(samples))
	}
	byName := map[string]store.SiteProbeSample{}
	for _, sample := range samples {
		byName[sample.RawModel] = sample
	}
	sonnet := byName["claude-sonnet-4-6"]
	// Token price published per million: it is stored as-is and the shared
	// normalizer owns the per-1k conversion at adoption time.
	if sonner := sonnet; sonner.Price.InputPerMillion != 3 || sonnet.Price.OutputPerMillion != 15 || sonnet.Price.CacheReadPerMillion != 0.3 {
		t.Fatalf("sonnet price = %+v", sonnet.Price)
	}
	if sonnet.Samples != 100 || sonnet.UpCount != 87 || sonnet.WeakEvidence {
		t.Fatalf("sonnet availability = %+v", sonnet)
	}
	if sonnet.GroupName != "default" {
		t.Fatalf("sonnet group = %q", sonnet.GroupName)
	}
	kimi := byName["kimi-k3"]
	if kimi.Price.Mode != "fixed" || kimi.Price.PerRequest != 0.02 {
		t.Fatalf("kimi price = %+v", kimi.Price)
	}
	// No monitor for kimi: price only, no availability — it must never reach a
	// verdict (Samples stays 0).
	if kimi.Samples != 0 || kimi.UpCount != 0 {
		t.Fatalf("kimi availability = %+v, want none", kimi)
	}
}

// A site that publishes a bare up/down flag instead of a measured availability
// gives one sample of weak evidence — enough to show, never enough to park a
// member on its own (the verdict needs MinSamples).
func TestTransitAvailabilityFromAFlagIsWeakEvidence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/.well-known/ai-transit.json" {
			_, _ = w.Write([]byte(`{"schema_version":"ai-transit.v1","snapshot_url":"` + serverURL(r) + `/api/public/transit/snapshot"}`))
			return
		}
		_, _ = w.Write([]byte(`{"mode":"v1","models":[{"name":"glm-5.2","input_price":0.5}],
			"monitors":[{"name":"glm-5.2","available":true}]}`))
	}))
	defer server.Close()

	_, snapshotURL, err := ResolveTransitDiscovery(context.Background(), server.Client(), server.URL)
	if err != nil {
		t.Fatalf("discovery: %v", err)
	}
	snapshot, mode, err := FetchTransit(context.Background(), server.Client(), snapshotURL)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	samples := TransitSamples(snapshot, 1, 1, time.Now().UTC())
	if len(samples) != 1 || mode != "v1" {
		t.Fatalf("samples = %d mode = %q", len(samples), mode)
	}
	if samples[0].Samples != 1 || samples[0].UpCount != 1 || !samples[0].WeakEvidence {
		t.Fatalf("flag reading = %+v, want one weak sample", samples[0])
	}
}

// V2 passive aggregates are real traffic: they win over a V1 active probe of the
// same model, and their success rate becomes the availability.
func TestTransitPrefersPassiveAggregatesOverActiveProbes(t *testing.T) {
	snapshot := &TransitSnapshot{
		Mode:   "v2",
		Models: []TransitModel{{Name: "glm-5.2", InputPrice: 0.5}},
		PassiveMonitoring: []TransitPassiveAggregate{{
			Model: "glm-5.2", Requests: 1000, Successes: 991, SuccessRate: floatPtr(0.991),
			AvgTTFT: floatPtr(412),
		}},
		Monitors: []TransitMonitor{{Name: "glm-5.2", Availability: floatPtr(0.5)}},
	}
	samples := TransitSamples(snapshot, 1, 1, time.Now().UTC())
	if len(samples) != 1 {
		t.Fatalf("samples = %d", len(samples))
	}
	if samples[0].Samples != 1000 || samples[0].UpCount != 991 {
		t.Fatalf("reading = %+v, want the passive aggregate", samples[0])
	}
	if samples[0].WeakEvidence {
		t.Fatal("real-traffic aggregates are the strongest evidence a site can publish")
	}
	if samples[0].AvgPingMS == nil || *samples[0].AvgPingMS != 412 {
		t.Fatalf("ttft = %v, want 412", samples[0].AvgPingMS)
	}
}

// A site without the discovery file must fail the round with a readable reason —
// not fall back to scraping a login-gated dashboard.
func TestResolveTransitDiscoveryRejectsSitesWithoutIt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body>login page</body></html>"))
	}))
	defer server.Close()

	if _, _, err := ResolveTransitDiscovery(context.Background(), server.Client(), server.URL); err == nil {
		t.Fatal("a site without the discovery file must not be readable as a transit source")
	} else if !strings.Contains(err.Error(), "discovery") {
		t.Fatalf("error = %v, want it to mention the discovery document", err)
	}
}

func floatPtr(value float64) *float64 { return &value }

// domainSite builds a minimal site row for the source-resolution tests.
func domainSite(platform, baseURL, kind, url string, auto bool) domain.Site {
	return domain.Site{
		Platform: platform, BaseURL: baseURL,
		ProbeSourceKind: kind, ProbeSourceURL: url, ProbeAuto: auto,
		ProbeSourceEnabled: true,
	}
}

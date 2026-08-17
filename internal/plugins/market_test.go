package plugins

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestMarketListAndValidation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"schema_version": 1,
			"plugins": [
				{
					"id": "cpa-console",
					"name": "CPA 管理",
					"description": "OAuth 账号池管理",
					"author": "test",
					"version": "1.0.0",
					"logo": "https://example.com/logo.png",
					"tags": ["oauth", "管理"],
					"url": "http://127.0.0.1:8317",
					"page_path": "management.html",
					"health_path": "healthz",
					"api_prefix": "/v0/management"
				},
				{
					"id": "bad-插件",
					"name": "bad",
					"url": "http://127.0.0.1:1"
				}
			]
		}`))
	}))
	defer srv.Close()

	m := newMarket(&http.Client{Timeout: 5 * time.Second}, []string{srv.URL})
	entries, err := m.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	// The invalid second entry makes the whole source fail validation.
	if len(entries) != 0 {
		t.Fatalf("expected invalid source to be skipped, got %d entries", len(entries))
	}
}

func TestMarketDedupAndInstallSpec(t *testing.T) {
	good := `{"schema_version":1,"plugins":[{"id":"demo","name":"Demo","url":"http://127.0.0.1:9100"}]}`
	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(good))
	}))
	defer srv1.Close()
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"schema_version":1,"plugins":[{"id":"demo","name":"Demo2","url":"http://other:1"},{"id":"other","name":"Other","url":"http://127.0.0.1:9200"}]}`))
	}))
	defer srv2.Close()

	m := newMarket(&http.Client{Timeout: 5 * time.Second}, []string{srv1.URL, srv2.URL})
	entries, err := m.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 deduplicated entries, got %d", len(entries))
	}
	if entries[0].Name != "Demo" {
		t.Fatalf("first source should win, got %q", entries[0].Name)
	}
	if entries[1].ID != "other" {
		t.Fatalf("expected second plugin, got %q", entries[1].ID)
	}
	spec := entries[0].InstallSpec()
	if spec.ID != "demo" || spec.Version != "1.0.0" || spec.Name != "Demo" {
		t.Fatalf("bad install spec: %+v", spec)
	}
}

func TestMarketRejectsSensitiveQuery(t *testing.T) {
	if _, err := parseMarketSource("https://example.com/registry.json?token=abc"); err == nil {
		t.Fatal("expected sensitive query rejection")
	}
}

func TestMarketUsesIsolatedStaleCacheOnRefreshFailure(t *testing.T) {
	var fail atomic.Bool
	registry := `{"schema_version":1,"plugins":[{"id":"demo","name":"Demo","tags":["stable"],"url":"http://127.0.0.1:9100"}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if fail.Load() {
			http.Error(w, "temporary outage", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(registry))
	}))
	t.Cleanup(srv.Close)

	source := marketSourceOf(srv.URL)
	m := newMarket(srv.Client(), nil)
	m.sources = []MarketSource{source}
	first, err := m.List(context.Background())
	if err != nil || len(first) != 1 {
		t.Fatalf("initial market list=%+v err=%v", first, err)
	}
	first[0].Tags[0] = "mutated"
	m.mu.Lock()
	entry := m.cache[source.URL]
	entry.fetched = time.Now().Add(-marketCacheTTL - time.Second)
	m.cache[source.URL] = entry
	m.mu.Unlock()
	fail.Store(true)

	second, err := m.List(context.Background())
	if err != nil || len(second) != 1 {
		t.Fatalf("stale fallback=%+v err=%v", second, err)
	}
	if got := second[0].Tags[0]; got != "stable" {
		t.Fatalf("caller mutation poisoned cached tags: %q", got)
	}
}

func TestMarketRejectsResponseOverLimitEvenWithValidJSONPrefix(t *testing.T) {
	registry := `{"schema_version":1,"plugins":[]}`
	oversized := registry + strings.Repeat(" ", marketMaxBytes-len(registry)+1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(oversized))
	}))
	t.Cleanup(srv.Close)

	source := marketSourceOf(srv.URL)
	m := newMarket(srv.Client(), nil)
	entries, err := m.fetch(context.Background(), source)
	if err == nil || len(entries) != 0 {
		t.Fatalf("oversized registry accepted: entries=%+v err=%v", entries, err)
	}
}

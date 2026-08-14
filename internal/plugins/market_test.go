package plugins

import (
	"context"
	"net/http"
	"net/http/httptest"
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

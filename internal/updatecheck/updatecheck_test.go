package updatecheck

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/buildinfo"
)

func TestIsNewer(t *testing.T) {
	cases := []struct {
		tag     string
		current string
		want    bool
	}{
		{"v0.2.0", "v0.1.0", true},
		{"0.2", "0.1.9", true},
		{"v1.0.0", "v0.9.9", true},
		{"v0.1.0", "v0.1.0", false},
		{"v0.0.9", "v0.1.0", false},
		{"v0.1.0", "dev", false},
		{"nightly", "v0.1.0", false},
		{"", "v0.1.0", false},
		{"v1.2.3-rc1", "v1.2.2", true},
		{"v1.2.3-rc1", "v1.2.3", false},
	}
	for _, tc := range cases {
		if got := IsNewer(tc.tag, tc.current); got != tc.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", tc.tag, tc.current, got, tc.want)
		}
	}
}

func TestRefreshComparesLatestRelease(t *testing.T) {
	original := buildinfo.Version
	buildinfo.Version = "v0.1.0"
	t.Cleanup(func() { buildinfo.Version = original })

	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"tag_name": "v0.2.0",
			"html_url": "https://github.com/" + Repo + "/releases/tag/v0.2.0",
		})
	}))
	defer server.Close()

	service := New(func() bool { return true })
	service.baseURL = server.URL
	status := service.Refresh(context.Background())
	if status.Err != "" {
		t.Fatalf("unexpected error: %q", status.Err)
	}
	if status.Latest != "v0.2.0" || !status.HasUpdate {
		t.Fatalf("status = %+v, want latest v0.2.0 with update", status)
	}
	if status.Current != "v0.1.0" {
		t.Fatalf("current = %q, want v0.1.0", status.Current)
	}

	// A fresh cache must serve without touching the network again.
	cached := service.RefreshIfStale(context.Background(), time.Hour)
	if hits != 1 {
		t.Fatalf("refresh hit GitHub %d times, want 1", hits)
	}
	if cached.Latest != "v0.2.0" {
		t.Fatalf("cached status = %+v", cached)
	}

	// A stale cache triggers exactly one more fetch.
	service.RefreshIfStale(context.Background(), -time.Second)
	if hits != 2 {
		t.Fatalf("stale refresh hit GitHub %d times, want 2", hits)
	}
}

func TestFetchFailureKeepsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()

	service := New(func() bool { return true })
	service.baseURL = server.URL
	status := service.Refresh(context.Background())
	if status.Latest != "" || status.Err == "" {
		t.Fatalf("status = %+v, want empty latest with error", status)
	}
}

func TestBetaOrderingAndFinalRelease(t *testing.T) {
	for _, tc := range []struct {
		next, current string
		want          bool
	}{
		{"v1.2.3-beta.2", "v1.2.3-beta.1", true}, {"v1.2.3-beta.10", "v1.2.3-beta.2", true},
		{"v1.2.3", "v1.2.3-beta.10", true}, {"v1.2.3-beta.10", "v1.2.3", false},
		{"v1.2.2", "v1.2.3-beta.1", false}, {"v1.2.3-beta.1", "v1.2.3-beta.1", false},
	} {
		if got := IsNewer(tc.next, tc.current); got != tc.want {
			t.Errorf("%s > %s = %v", tc.next, tc.current, got)
		}
	}
}
func TestBetaSelectionAndChannelCache(t *testing.T) {
	original := buildinfo.Version
	buildinfo.Version = "v1.2.3-beta.1"
	t.Cleanup(func() { buildinfo.Version = original })
	hits := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.URL.Path)
		if r.URL.Path == "/repos/"+Repo+"/releases/latest" {
			json.NewEncoder(w).Encode(releaseResponse{TagName: "v1.2.2"})
			return
		}
		json.NewEncoder(w).Encode([]releaseResponse{{TagName: "v1.2.3-beta.2", Prerelease: true}, {TagName: "v1.2.3-beta.10", Prerelease: true}, {TagName: "v2.0.0-beta.1", Prerelease: true, Draft: true}, {TagName: "v8.0.0-rc.1", Prerelease: true}, {TagName: "v1.2.2"}})
	}))
	defer server.Close()
	channel := "stable"
	s := New(func() bool { return true })
	s.baseURL = server.URL
	s.SetChannelSource(func() string { return channel })
	if got := s.Refresh(context.Background()); got.Latest != "v1.2.2" || got.HasUpdate {
		t.Fatalf("stable: %+v", got)
	}
	channel = "beta"
	if s.Status().Latest != "" {
		t.Fatal("stale stable result reused")
	}
	got := s.RefreshIfStale(context.Background(), time.Hour)
	if got.Latest != "v1.2.3-beta.10" || !got.HasUpdate || got.Channel != "beta" {
		t.Fatalf("beta: %+v", got)
	}
	if len(hits) != 2 {
		t.Fatal(hits)
	}
}
func TestDisabledRefreshNeverFetches(t *testing.T) {
	s := New(func() bool { return false })
	s.baseURL = "http://127.0.0.1:1"
	if got := s.Refresh(context.Background()); got.Err != "" || got.Latest != "" {
		t.Fatal(got)
	}
}

package selfupdate

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTriggerWatchtower(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/update" && r.Method == http.MethodPost &&
			r.Header.Get("Authorization") == "Bearer tok" {
			hits++
			w.WriteHeader(200)
			return
		}
		w.WriteHeader(403)
	}))
	defer srv.Close()
	t.Setenv("WATCHTOWER_URL", srv.URL)
	t.Setenv("WATCHTOWER_TOKEN", "tok")

	if err := TriggerWatchtower(context.Background()); err != nil {
		t.Fatalf("trigger: %v", err)
	}
	if hits != 1 {
		t.Fatalf("hits=%d, want 1", hits)
	}
}

func TestWatchtowerModeDetection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()
	t.Setenv("WATCHTOWER_URL", srv.URL)

	// The reachability probe is TCP-level: it only decides whether the
	// companion service exists on the compose network.
	if !WatchtowerReachable() {
		t.Fatal("companion not detected")
	}

	t.Setenv("WATCHTOWER_URL", "http://127.0.0.1:1")
	if WatchtowerReachable() {
		t.Fatal("unreachable companion reported reachable")
	}
}

package sitenews_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/sitenews"
)

// countingBoardServer answers /api/status like a New-API site and counts the
// requests, so a scheduler test can see whether a round actually went out.
func countingBoardServer(t *testing.T, hits *atomic.Int64) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/status" {
			http.NotFound(w, r)
			return
		}
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"data":    map[string]any{"announcements": []map[string]any{}},
		})
	}))
	t.Cleanup(server.Close)
	return server
}

// waitFor polls until the condition holds: the loop is time-driven, so the test
// observes the effect instead of sleeping for a fixed amount.
func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// Zero means off: the scheduled read stops, the console's manual refresh keeps
// working, and a positive interval re-arms the loop without a restart.
func TestSchedulerZeroIntervalStopsRounds(t *testing.T) {
	db := openNewsDB(t)
	var hits atomic.Int64
	server := countingBoardServer(t, &hits)
	newAPISite(t, db, "x666", server.URL, "new-api")

	service := sitenews.NewService(db, nil, nil)
	scheduler := sitenews.NewScheduler(service, 20*time.Millisecond, 0, nil)
	scheduler.Start()
	defer scheduler.Stop()

	waitFor(t, "the first scheduled round", func() bool { return hits.Load() > 0 })

	scheduler.SetSchedule(0, 0)
	if interval, jitter := scheduler.Cadence(); interval != 0 || jitter != 0 {
		t.Fatalf("cadence after switching off = %v/%v, want 0/0", interval, jitter)
	}
	// A round may already be in flight; let it finish before comparing.
	time.Sleep(60 * time.Millisecond)
	settled := hits.Load()
	time.Sleep(200 * time.Millisecond)
	if got := hits.Load(); got != settled {
		t.Fatalf("the loop kept reading while off: %d extra rounds, want 0", got-settled)
	}

	// The manual path does not go through the schedule.
	result, err := scheduler.RefreshNow(context.Background())
	if err != nil {
		t.Fatalf("manual refresh while off: %v", err)
	}
	if result.Sites != 1 || result.Fetched != 1 {
		t.Fatalf("manual refresh read %d/%d sites, want 1/1", result.Sites, result.Fetched)
	}

	afterManual := hits.Load()
	scheduler.SetSchedule(20*time.Millisecond, 0)
	waitFor(t, "the loop to resume", func() bool { return hits.Load() > afterManual })
}

// A negative interval is off, not "the default", and jitter never exceeds the
// interval it decorates.
func TestSchedulerCadenceClampsToOffNotDefault(t *testing.T) {
	db := openNewsDB(t)
	service := sitenews.NewService(db, nil, nil)

	scheduler := sitenews.NewScheduler(service, -5*time.Second, 3*time.Second, nil)
	if interval, jitter := scheduler.Cadence(); interval != 0 || jitter != 0 {
		t.Fatalf("negative interval = %v/%v, want 0/0 (off)", interval, jitter)
	}

	scheduler.SetSchedule(time.Minute, 5*time.Minute)
	if interval, jitter := scheduler.Cadence(); interval != time.Minute || jitter != time.Minute {
		t.Fatalf("jitter clamp = %v/%v, want 1m/1m", interval, jitter)
	}
}

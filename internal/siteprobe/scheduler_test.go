package siteprobe

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// stubCatalog serves an empty directory snapshot and counts the requests, so a
// test can prove a round read *this* address instead of the shipped one.
func stubCatalog(t *testing.T) (catalogURL string, hits *atomic.Int64) {
	t.Helper()
	hits = &atomic.Int64{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"rows":[]}`))
	}))
	t.Cleanup(server.Close)
	return server.URL, hits
}

// The collection cadence is configurable because sites publish their probe data
// at wildly different rates: a status page polling every 60s is wasted at the
// 15-minute default. These tests pin the two halves of that promise — the loop
// actually fires at the configured rate, and a change is picked up without
// restarting the loop.
//
// The scheduler is given the stub directory explicitly. Built with an empty
// address the round would read the shipped directory instead, which made this
// test silently depend on the public internet: on a machine that cannot reach it
// the round spends 15s on the snapshot and misses the 10s deadline below.
func TestSchedulerCollectsOnTheConfiguredCadence(t *testing.T) {
	source := &ratioSource{}
	source.set(10, 0)
	f := newFixture(t, source, "z-ai/glm-5.2", "glm-5.2")
	catalogURL, catalogHits := stubCatalog(t)

	scheduler := NewScheduler(f.service, 50*time.Millisecond, 0, catalogURL, nil)
	scheduler.Start()
	defer scheduler.Stop()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		runs, err := f.db.ListSiteProbeRuns(f.siteID, 10)
		if err != nil {
			t.Fatalf("list runs: %v", err)
		}
		if len(runs) >= 2 {
			if catalogHits.Load() == 0 {
				t.Fatal("the round collected sites without reading the configured directory")
			}
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("the loop did not collect twice within 10s at a 50ms cadence")
}

// A directory that never answers must cost the round its own short budget, not
// hold the whole round (and the operator's "collect now") behind it: the sites
// come from the sites themselves, and the snapshot is only a fallback source.
func TestRoundSurvivesAStallingDirectory(t *testing.T) {
	source := &ratioSource{}
	source.set(10, 0)
	f := newFixture(t, source, "z-ai/glm-5.2", "glm-5.2")

	released := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-released:
		}
	}))
	defer server.Close()
	defer close(released)

	// Shrink the snapshot budget instead of waiting the shipped 15s.
	f.service.externalFetchTimeout = 50 * time.Millisecond
	scheduler := NewScheduler(f.service, time.Minute, 0, server.URL, nil)

	started := time.Now()
	result, err := scheduler.CollectNow(context.Background())
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if result.Collected != 1 || result.Failed != 0 {
		t.Fatalf("round = %d collected / %d failed, want 1/0", result.Collected, result.Failed)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("a round with a dead directory took %v", elapsed)
	}
}

func TestSchedulerCadenceIsHotReloadable(t *testing.T) {
	scheduler := NewScheduler(NewService(nil, nil, nil), 15*time.Minute, 2*time.Minute, "", nil)

	interval, jitter := scheduler.Cadence()
	if interval != 15*time.Minute || jitter != 2*time.Minute {
		t.Fatalf("bootstrap cadence = %v/%v, want 15m/2m", interval, jitter)
	}
	// nextDelay is the exact expression the loop evaluates at the top of every
	// round, so asserting on it is asserting on what the loop will wait.
	if delay := scheduler.nextDelay(); delay < 15*time.Minute || delay > 17*time.Minute {
		t.Fatalf("first delay = %v, want within 15m..17m", delay)
	}

	scheduler.SetSchedule(5*time.Minute, 30*time.Second)
	interval, jitter = scheduler.Cadence()
	if interval != 5*time.Minute || jitter != 30*time.Second {
		t.Fatalf("cadence after change = %v/%v, want 5m/30s", interval, jitter)
	}
	if delay := scheduler.nextDelay(); delay < 5*time.Minute || delay > 5*time.Minute+30*time.Second {
		t.Fatalf("delay after change = %v, want within 5m..5m30s", delay)
	}

	// An unset setting must not stop collection: the loop has no off switch, so
	// a zero interval falls back to the shipped default.
	scheduler.SetSchedule(0, 0)
	if interval, _ := scheduler.Cadence(); interval != DefaultInterval {
		t.Fatalf("zero interval = %v, want the default %v", interval, DefaultInterval)
	}
	// Jitter wider than the interval is clamped rather than allowed to stack
	// rounds on top of each other.
	scheduler.SetSchedule(2*time.Minute, time.Hour)
	interval, jitter = scheduler.Cadence()
	if jitter != interval {
		t.Fatalf("oversized jitter = %v, want it clamped to the interval %v", jitter, interval)
	}
}

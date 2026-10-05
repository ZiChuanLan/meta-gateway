package siteprobe

import (
	"testing"
	"time"
)

// The collection cadence is configurable because sites publish their probe data
// at wildly different rates: a status page polling every 60s is wasted at the
// 15-minute default. These two tests pin the two halves of that promise — the
// loop actually fires at the configured rate, and a change is picked up without
// restarting the loop.
func TestSchedulerCollectsOnTheConfiguredCadence(t *testing.T) {
	source := &ratioSource{}
	source.set(10, 0)
	f := newFixture(t, source, "z-ai/glm-5.2", "glm-5.2")

	scheduler := NewScheduler(f.service, 50*time.Millisecond, 0, nil)
	scheduler.Start()
	defer scheduler.Stop()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		runs, err := f.db.ListSiteProbeRuns(f.siteID, 10)
		if err != nil {
			t.Fatalf("list runs: %v", err)
		}
		if len(runs) >= 2 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("the loop did not collect twice within 10s at a 50ms cadence")
}

func TestSchedulerCadenceIsHotReloadable(t *testing.T) {
	scheduler := NewScheduler(NewService(nil, nil, nil), 15*time.Minute, 2*time.Minute, nil)

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

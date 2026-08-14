package discovery

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

type recordingRunner struct {
	calls atomic.Int64
}

func (r *recordingRunner) RefreshAll(ctx context.Context) (*RefreshSummary, error) {
	r.calls.Add(1)
	return &RefreshSummary{}, nil
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}

func TestSchedulerRunsOnCron(t *testing.T) {
	runner := &recordingRunner{}
	s := NewScheduler(runner, "* * * * *", nil, nil) // every minute; we test apply+next run below
	if err := s.SetSchedule("*/1 * * * *", true); err != nil {
		t.Fatal(err)
	}
	// The cron fires at minute boundaries; wait up to 70s for the first run.
	waitFor(t, 70*time.Second, func() bool { return runner.calls.Load() > 0 })
}

func TestSchedulerDisableStopsRuns(t *testing.T) {
	runner := &recordingRunner{}
	s := NewScheduler(runner, "*/1 * * * *", nil, nil)
	// Disable before any tick: no entry should be scheduled.
	if err := s.SetSchedule("", false); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Second)
	if runner.calls.Load() != 0 {
		t.Fatalf("disabled scheduler ran: %d", runner.calls.Load())
	}
}

func TestSchedulerInvalidExpressionRejected(t *testing.T) {
	runner := &recordingRunner{}
	s := NewScheduler(runner, "", nil, nil)
	if err := s.SetSchedule("not a cron", true); err == nil {
		t.Fatal("invalid cron must be rejected")
	}
	if err := s.SetSchedule("", true); err != nil {
		t.Fatalf("empty (disabled) must be accepted: %v", err)
	}
}

func TestSchedulerHotSwap(t *testing.T) {
	runner := &recordingRunner{}
	s := NewScheduler(runner, "", nil, nil)
	if err := s.SetSchedule("* * * * *", true); err != nil {
		t.Fatal(err)
	}
	// Swap to a different expression live (also exercises Remove+Add).
	if err := s.SetSchedule("*/2 * * * *", true); err != nil {
		t.Fatal(err)
	}
	s.Stop()
}

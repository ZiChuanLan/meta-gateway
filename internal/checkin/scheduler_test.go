package checkin

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type fakeBatchRunner struct {
	calls     atomic.Int32
	entered   chan struct{}
	release   chan struct{}
	result    *RunSummary
	err       error
	lastRunAt time.Time
}

func (r *fakeBatchRunner) LastScheduledRunAt(ctx context.Context) (time.Time, error) {
	return r.lastRunAt, nil
}

func (r *fakeBatchRunner) RunAll(ctx context.Context, source string) (*RunSummary, error) {
	if source != SourceScheduled {
		return nil, errors.New("unexpected source")
	}
	r.calls.Add(1)
	if r.entered != nil {
		select {
		case r.entered <- struct{}{}:
		default:
		}
	}
	if r.release != nil {
		select {
		case <-r.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return r.result, r.err
}

func TestSchedulerRejectsInvalidFiveFieldCron(t *testing.T) {
	runner := &fakeBatchRunner{}
	if _, err := NewScheduler(runner, "0 0 8 * * *", nil, time.UTC); err == nil {
		t.Fatal("six-field expression must be rejected")
	}
	if _, err := NewScheduler(runner, "@every 1h", nil, time.UTC); err == nil {
		t.Fatal("descriptor expression must be rejected")
	}
	if _, err := NewScheduler(runner, "0 8 * * *", nil, time.UTC); err != nil {
		t.Fatalf("valid expression: %v", err)
	}
}

func TestSchedulerRunPreventsBatchOverlapAndLogsAggregate(t *testing.T) {
	var output bytes.Buffer
	runner := &fakeBatchRunner{
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
		result:  &RunSummary{SuccessCount: 1, FailureCount: 2, SkippedCount: 3},
	}
	scheduler, err := NewScheduler(runner, "0 8 * * *", log.New(&output, "", 0), time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan bool, 1)
	go func() { done <- scheduler.run(t.Context()) }()
	<-runner.entered
	if scheduler.run(t.Context()) {
		t.Fatal("overlapping batch should be skipped")
	}
	close(runner.release)
	if !<-done || runner.calls.Load() != 1 {
		t.Fatalf("calls=%d", runner.calls.Load())
	}
	if got := output.String(); !strings.Contains(got, "success=1 failed=2 skipped=3") {
		t.Fatalf("log=%q", got)
	}
}

func TestSchedulerLocation(t *testing.T) {
	runner := &fakeBatchRunner{}
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Skipf("tz database unavailable: %v", err)
	}
	s, err := NewScheduler(runner, "0 8 * * *", nil, shanghai)
	if err != nil {
		t.Fatal(err)
	}
	if s.Location() != shanghai {
		t.Fatalf("location=%v, want Asia/Shanghai", s.Location())
	}
	local, err := NewScheduler(runner, "0 8 * * *", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if local.Location() != time.Local {
		t.Fatalf("default location=%v, want time.Local", local.Location())
	}
}

func TestSchedulerStopCancelsRunningBatch(t *testing.T) {
	runner := &fakeBatchRunner{entered: make(chan struct{}, 1), release: make(chan struct{})}
	scheduler, err := NewScheduler(runner, "0 8 * * *", nil, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if err := scheduler.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan bool, 1)
	go func() { done <- scheduler.run(scheduler.ctx) }()
	<-runner.entered
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := scheduler.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if !<-done {
		t.Fatal("running batch did not start")
	}
	if err := scheduler.Start(); !errors.Is(err, ErrSchedulerStopped) {
		t.Fatalf("restart err=%v", err)
	}
}

// waitForCalls polls until the runner has been invoked at least want times.
func waitForCalls(t *testing.T, runner *fakeBatchRunner, want int32) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runner.calls.Load() >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("runner calls=%d, want >= %d", runner.calls.Load(), want)
}

func assertNoCalls(t *testing.T, runner *fakeBatchRunner) {
	t.Helper()
	time.Sleep(200 * time.Millisecond)
	if calls := runner.calls.Load(); calls != 0 {
		t.Fatalf("runner calls=%d, want 0", calls)
	}
}

func newCatchUpScheduler(t *testing.T, expression string, now, lastRunAt time.Time) (*Scheduler, *fakeBatchRunner) {
	t.Helper()
	runner := &fakeBatchRunner{result: &RunSummary{}, lastRunAt: lastRunAt}
	scheduler, err := NewScheduler(runner, expression, nil, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	scheduler.now = func() time.Time { return now }
	return scheduler, runner
}

func TestCatchUpRunsWhenDailyTickWasMissed(t *testing.T) {
	// Restart at 08:30 with the last scheduled run yesterday: catch up once.
	lastRun := time.Date(2026, 8, 3, 8, 0, 0, 0, time.UTC) // Monday
	now := time.Date(2026, 8, 4, 8, 30, 0, 0, time.UTC)   // Tuesday 08:30
	scheduler, runner := newCatchUpScheduler(t, "0 8 * * *", now, lastRun)
	if err := scheduler.Start(); err != nil {
		t.Fatal(err)
	}
	waitForCalls(t, runner, 1)
}

func TestCatchUpSkipsBeforeDailyFireTime(t *testing.T) {
	// Boot at 07:00: today's 08:00 tick has not arrived, nothing to catch up.
	lastRun := time.Date(2026, 8, 3, 8, 0, 0, 0, time.UTC)
	now := time.Date(2026, 8, 4, 7, 0, 0, 0, time.UTC)
	scheduler, runner := newCatchUpScheduler(t, "0 8 * * *", now, lastRun)
	if err := scheduler.Start(); err != nil {
		t.Fatal(err)
	}
	assertNoCalls(t, runner)
}

func TestCatchUpSkipsWhenAlreadyRanToday(t *testing.T) {
	// Restart at 09:00 after today's 08:00 run: must not run twice.
	lastRun := time.Date(2026, 8, 4, 8, 2, 0, 0, time.UTC)
	now := time.Date(2026, 8, 4, 9, 0, 0, 0, time.UTC)
	scheduler, runner := newCatchUpScheduler(t, "0 8 * * *", now, lastRun)
	if err := scheduler.Start(); err != nil {
		t.Fatal(err)
	}
	assertNoCalls(t, runner)
}

func TestCatchUpSkipsOnFreshInstall(t *testing.T) {
	// No history at all: a first deployment must not surprise-run.
	now := time.Date(2026, 8, 4, 9, 0, 0, 0, time.UTC)
	scheduler, runner := newCatchUpScheduler(t, "0 8 * * *", now, time.Time{})
	if err := scheduler.Start(); err != nil {
		t.Fatal(err)
	}
	assertNoCalls(t, runner)
}

func TestCatchUpSkipsOnNonScheduledWeekday(t *testing.T) {
	// Weekday-only schedule: Saturday 10:00 has no missed weekday tick.
	lastRun := time.Date(2026, 8, 7, 8, 0, 0, 0, time.UTC) // Friday
	now := time.Date(2026, 8, 8, 10, 0, 0, 0, time.UTC)    // Saturday
	scheduler, runner := newCatchUpScheduler(t, "0 8 * * 1-5", now, lastRun)
	if err := scheduler.Start(); err != nil {
		t.Fatal(err)
	}
	assertNoCalls(t, runner)
}

func TestSetScheduleEnableTriggersCatchUp(t *testing.T) {
	// Disabled all day, re-enabled at 20:00 with yesterday's last run: catch up.
	lastRun := time.Date(2026, 8, 3, 8, 0, 0, 0, time.UTC)
	now := time.Date(2026, 8, 4, 20, 0, 0, 0, time.UTC)
	scheduler, runner := newCatchUpScheduler(t, "0 8 * * *", now, lastRun)
	if err := scheduler.SetSchedule("0 8 * * *", false); err != nil {
		t.Fatal(err)
	}
	if err := scheduler.SetSchedule("0 8 * * *", true); err != nil {
		t.Fatal(err)
	}
	waitForCalls(t, runner, 1)
}

func TestCatchUpFiresOnlyOnceAfterMiss(t *testing.T) {
	// Two consecutive triggers after a miss must dedupe to a single batch.
	lastRun := time.Date(2026, 8, 3, 8, 0, 0, 0, time.UTC)
	now := time.Date(2026, 8, 4, 8, 30, 0, 0, time.UTC)
	scheduler, runner := newCatchUpScheduler(t, "0 8 * * *", now, lastRun)
	if err := scheduler.Start(); err != nil {
		t.Fatal(err)
	}
	scheduler.maybeCatchUp()
	waitForCalls(t, runner, 1)
	time.Sleep(200 * time.Millisecond)
	if calls := runner.calls.Load(); calls != 1 {
		t.Fatalf("runner calls=%d, want exactly 1", calls)
	}
}

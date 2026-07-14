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
	calls   atomic.Int32
	entered chan struct{}
	release chan struct{}
	result  *RunSummary
	err     error
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
	if _, err := NewScheduler(runner, "0 0 8 * * *", nil); err == nil {
		t.Fatal("six-field expression must be rejected")
	}
	if _, err := NewScheduler(runner, "@every 1h", nil); err == nil {
		t.Fatal("descriptor expression must be rejected")
	}
	if _, err := NewScheduler(runner, "0 8 * * *", nil); err != nil {
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
	scheduler, err := NewScheduler(runner, "0 8 * * *", log.New(&output, "", 0))
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

func TestSchedulerStopCancelsRunningBatch(t *testing.T) {
	runner := &fakeBatchRunner{entered: make(chan struct{}, 1), release: make(chan struct{})}
	scheduler, err := NewScheduler(runner, "0 8 * * *", nil)
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

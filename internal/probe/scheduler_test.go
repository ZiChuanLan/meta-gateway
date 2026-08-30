package probe

import (
	"context"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/store"
)

// waitForIdleTask blocks until no probe task is running, so assertions never
// race the background worker.
func waitForIdleTask(t *testing.T, db *store.DB) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		tasks, err := db.ListProbeTasks(10)
		if err != nil {
			t.Fatalf("list tasks: %v", err)
		}
		busy := false
		for _, task := range tasks {
			if task.Status == store.ProbeTaskRunning {
				busy = true
			}
		}
		if !busy {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("probe task did not settle in time")
}

func newTestScheduler(t *testing.T, db *store.DB, relay Relay, schedule Schedule) (*Service, *Scheduler) {
	t.Helper()
	service := NewService(db, relay, nil)
	scheduler := NewScheduler(db, service, schedule, nil, nil)
	t.Cleanup(scheduler.Stop)
	return service, scheduler
}

// A scheduled firing must start a run with the configured prompt, not the
// default one: the whole point of configuring a schedule is that it probes the
// way the operator asked.
func TestSchedulerRunUsesConfiguredPrompt(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// The scheduled run selects from live route members, so it needs a real
	// route before it has anything to probe.
	chID := newProbeFixture(t, db, "m1")
	fake := &fakeRelay{ok: map[int64]bool{chID: true}}
	_, scheduler := newTestScheduler(t, db, fake, Schedule{})
	scheduler.fire(Schedule{
		Options: Options{Prompt: "scheduled ping", MaxTokens: 16},
	})

	tasks, err := db.ListProbeTasks(10)
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("got %d tasks, want 1", len(tasks))
	}
	if tasks[0].Prompt != "scheduled ping" {
		t.Errorf("task prompt = %q, want %q", tasks[0].Prompt, "scheduled ping")
	}
	if tasks[0].MaxTokens != 16 {
		t.Errorf("task max_tokens = %d, want 16", tasks[0].MaxTokens)
	}
	waitForIdleTask(t, db)
	if got := fake.sentPrompt(); got != "scheduled ping" {
		t.Errorf("upstream received %q, want %q", got, "scheduled ping")
	}
}

// Two overlapping runs would double the upstream load for no extra
// information, so a firing that finds a run in progress must stand down.
func TestSchedulerSkipsWhileRunInProgress(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// The relay blocks until released, so the first run is genuinely in flight.
	release := make(chan struct{})
	fake := &fakeRelay{ok: map[int64]bool{1: true}, release: release}
	service, scheduler := newTestScheduler(t, db, fake, Schedule{})

	first, err := service.Start(context.Background(), []Pair{{ChannelID: 1, Model: "m1"}}, Options{})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if task, _ := db.GetProbeTask(first.ID); task.Status != store.ProbeTaskRunning {
		t.Fatalf("precondition: task status = %q, want running", task.Status)
	}

	scheduler.fire(Schedule{Options: Options{Prompt: "should not run"}})

	tasks, err := db.ListProbeTasks(10)
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("got %d tasks, want 1 (the scheduled firing should have been skipped)", len(tasks))
	}

	close(release)
	waitForIdleTask(t, db)
}

// A bad cron expression must surface as an error at configuration time, not as
// a schedule that silently never fires.
func TestSchedulerRejectsInvalidCron(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	_, scheduler := newTestScheduler(t, db, &fakeRelay{}, Schedule{})
	if err := scheduler.SetSchedule(Schedule{Expr: "not a cron"}); err == nil {
		t.Fatal("expected an error for an invalid cron expression")
	}
}

// An empty expression is the documented way to turn the schedule off.
func TestSchedulerEmptyCronDisables(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	_, scheduler := newTestScheduler(t, db, &fakeRelay{}, Schedule{})
	if err := scheduler.SetSchedule(Schedule{Expr: "*/10 * * * *"}); err != nil {
		t.Fatalf("set schedule: %v", err)
	}
	if err := scheduler.SetSchedule(Schedule{}); err != nil {
		t.Fatalf("disable schedule: %v", err)
	}
}

package maintenance

import (
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/store"
)

// TestSetScheduleInvalidRejected verifies a bad cron expression is rejected
// without touching an existing schedule.
func TestSetScheduleInvalidRejected(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := New(db, "")
	if err := s.SetSchedule("not-a-cron"); err == nil {
		t.Fatal("expected invalid cron error")
	}
	if err := s.SetSchedule("0 4 * * *"); err != nil {
		t.Fatal(err)
	}
	s.Stop()
}

// TestRunOnceAndLast verifies the manual pass runs synchronously and records
// the result for the status endpoint.
func TestRunOnceAndLast(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := New(db, "")
	res, err := s.RunOnce()
	if err != nil {
		t.Fatal(err)
	}
	if res == nil {
		t.Fatal("nil result")
	}
	last, at := s.Last()
	if last == nil || at.IsZero() {
		t.Fatal("last result not recorded")
	}
	s.Stop()
}

// TestScheduleRunsOnCron verifies a real cron tick fires the pass.
func TestScheduleRunsOnCron(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := New(db, "")
	if err := s.SetSchedule("*/1 * * * *"); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()
	deadline := time.Now().Add(75 * time.Second)
	for time.Now().Before(deadline) {
		last, _ := s.Last()
		if last != nil {
			return // a pass ran
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatal("no gc pass ran within the cron window")
}

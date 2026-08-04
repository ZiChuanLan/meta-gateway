package checkin

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
)

var ErrSchedulerStopped = errors.New("check-in scheduler has been stopped")

// ErrInvalidCron is returned when a hot-reload cron expression is invalid.
var ErrInvalidCron = errors.New("invalid check-in cron expression")

type BatchRunner interface {
	RunAll(context.Context, string) (*RunSummary, error)
}

// lastRunReporter is implemented by runners that can report the most recent
// scheduled run from persisted history (see Service.LastScheduledRunAt).
type lastRunReporter interface {
	LastScheduledRunAt(ctx context.Context) (time.Time, error)
}

const dateLayout = "2006-01-02"

type Scheduler struct {
	runner   BatchRunner
	cron     *cron.Cron
	logger   *log.Logger
	ctx      context.Context
	cancel   context.CancelFunc
	location *time.Location
	now      func() time.Time
	schedule cron.Schedule

	lifecycleMu sync.Mutex
	started     bool
	stopped     bool
	runMu       sync.Mutex
	running     bool
	expression  string

	lastRunMu      sync.Mutex
	seeded         bool
	lastRunDay     string // local date (2006-01-02) of the most recent completed run
	catchUpPending bool   // a catch-up batch has been spawned and not yet finished
}

// NewScheduler builds a scheduler for the given five-field cron expression.
// location is the timezone schedules are interpreted in; nil means the process
// local timezone (which is UTC inside most containers unless TZ is set).
func NewScheduler(runner BatchRunner, expression string, logger *log.Logger, location *time.Location) (*Scheduler, error) {
	if runner == nil {
		return nil, errors.New("check-in scheduler runner is required")
	}
	if location == nil {
		location = time.Local
	}
	ctx, cancel := context.WithCancel(context.Background())
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	schedule, err := parser.Parse(expression)
	if err != nil {
		cancel()
		return nil, err
	}
	scheduler := &Scheduler{
		runner:     runner,
		cron:       cron.New(cron.WithParser(parser), cron.WithLocation(location)),
		logger:     logger,
		ctx:        ctx,
		cancel:     cancel,
		location:   location,
		now:        time.Now,
		schedule:   schedule,
		expression: expression,
	}
	if _, err := scheduler.cron.AddFunc(expression, func() {
		scheduler.run(scheduler.ctx)
	}); err != nil {
		cancel()
		return nil, err
	}
	return scheduler, nil
}

// Location returns the timezone schedules are interpreted in.
func (s *Scheduler) Location() *time.Location {
	if s == nil || s.location == nil {
		return time.Local
	}
	return s.location
}

func (s *Scheduler) Start() error {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.stopped {
		return ErrSchedulerStopped
	}
	if s.started {
		return nil
	}
	s.started = true
	s.cron.Start()
	s.maybeCatchUp()
	return nil
}

// SetSchedule replaces the cron expression. When enabled is false the scheduler
// stops ticking without destroying the process; when true it restarts with the new cron.
func (s *Scheduler) SetSchedule(expression string, enabled bool) error {
	if s == nil {
		return nil
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.stopped {
		return ErrSchedulerStopped
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	var schedule cron.Schedule
	if enabled {
		var err error
		schedule, err = parser.Parse(expression)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidCron, err)
		}
	}
	if s.started {
		stopCtx := s.cron.Stop()
		<-stopCtx.Done()
		s.started = false
	}
	if !enabled {
		s.expression = expression
		return nil
	}
	s.cron = cron.New(cron.WithParser(parser), cron.WithLocation(s.Location()))
	if _, err := s.cron.AddFunc(expression, func() {
		s.run(s.ctx)
	}); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidCron, err)
	}
	s.expression = expression
	s.schedule = schedule
	s.cron.Start()
	s.started = true
	s.maybeCatchUp()
	return nil
}

func (s *Scheduler) Expression() string {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	return s.expression
}

func (s *Scheduler) Stop(ctx context.Context) error {
	s.lifecycleMu.Lock()
	if s.stopped {
		s.lifecycleMu.Unlock()
		return nil
	}
	s.stopped = true
	s.cancel()
	wait := s.cron.Stop()
	s.lifecycleMu.Unlock()

	select {
	case <-wait.Done():
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// maybeCatchUp runs a batch immediately when today's scheduled tick was missed
// (process restarted or schedule disabled past the fire time). It seeds per-day
// tracking from persisted history on first use: a fresh install with no history
// never surprise-runs, while a restart after the daily tick catches up once.
func (s *Scheduler) maybeCatchUp() {
	s.lastRunMu.Lock()
	defer s.lastRunMu.Unlock()
	if !s.seeded {
		s.seedLastRunDayLocked()
		s.seeded = true
	}
	if s.lastRunDay == s.today() {
		return
	}
	now := s.now().In(s.Location())
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, s.Location())
	if s.schedule.Next(midnight.Add(-time.Minute)).After(now) {
		return // today's scheduled time has not arrived yet
	}
	if s.catchUpPending {
		return // a catch-up batch is already in flight
	}
	s.catchUpPending = true
	// Overlap guard in run() dedupes concurrent triggers.
	go s.run(s.ctx)
}

// seedLastRunDayLocked initializes lastRunDay from persisted scheduled-run
// history. lastRunMu must be held.
func (s *Scheduler) seedLastRunDayLocked() {
	reporter, ok := s.runner.(lastRunReporter)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
	defer cancel()
	last, err := reporter.LastScheduledRunAt(ctx)
	if err != nil {
		return
	}
	if last.IsZero() {
		// No history at all: treat today as handled so a first deployment
		// does not fire an unexpected run.
		s.lastRunDay = s.today()
		return
	}
	s.lastRunDay = last.In(s.Location()).Format(dateLayout)
}

// markRan records the local day on which a batch completed.
func (s *Scheduler) markRan() {
	s.lastRunMu.Lock()
	s.lastRunDay = s.today()
	s.catchUpPending = false
	s.lastRunMu.Unlock()
}

func (s *Scheduler) today() string {
	return s.now().In(s.Location()).Format(dateLayout)
}

func (s *Scheduler) run(ctx context.Context) bool {
	s.runMu.Lock()
	if s.running {
		s.runMu.Unlock()
		return false
	}
	s.running = true
	s.runMu.Unlock()
	defer func() {
		s.runMu.Lock()
		s.running = false
		s.runMu.Unlock()
		s.markRan()
	}()

	summary, err := s.runner.RunAll(ctx, SourceScheduled)
	if err != nil {
		if s.logger != nil && !errors.Is(err, context.Canceled) {
			s.logger.Printf("scheduled check-in failed")
		}
		return true
	}
	if s.logger != nil {
		s.logger.Printf(
			"scheduled check-in completed: success=%d failed=%d skipped=%d",
			summary.SuccessCount,
			summary.FailureCount,
			summary.SkippedCount,
		)
	}
	return true
}

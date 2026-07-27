package checkin

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"

	"github.com/robfig/cron/v3"
)

var ErrSchedulerStopped = errors.New("check-in scheduler has been stopped")

// ErrInvalidCron is returned when a hot-reload cron expression is invalid.
var ErrInvalidCron = errors.New("invalid check-in cron expression")

type BatchRunner interface {
	RunAll(context.Context, string) (*RunSummary, error)
}

type Scheduler struct {
	runner BatchRunner
	cron   *cron.Cron
	logger *log.Logger
	ctx    context.Context
	cancel context.CancelFunc

	lifecycleMu sync.Mutex
	started     bool
	stopped     bool
	runMu       sync.Mutex
	running     bool
	expression  string
}

func NewScheduler(runner BatchRunner, expression string, logger *log.Logger) (*Scheduler, error) {
	if runner == nil {
		return nil, errors.New("check-in scheduler runner is required")
	}
	ctx, cancel := context.WithCancel(context.Background())
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	scheduler := &Scheduler{
		runner:     runner,
		cron:       cron.New(cron.WithParser(parser)),
		logger:     logger,
		ctx:        ctx,
		cancel:     cancel,
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
	if enabled {
		if _, err := parser.Parse(expression); err != nil {
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
	s.cron = cron.New(cron.WithParser(parser))
	if _, err := s.cron.AddFunc(expression, func() {
		s.run(s.ctx)
	}); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidCron, err)
	}
	s.expression = expression
	s.cron.Start()
	s.started = true
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

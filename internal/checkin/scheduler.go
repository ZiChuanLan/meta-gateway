package checkin

import (
	"context"
	"errors"
	"log"
	"sync"

	"github.com/robfig/cron/v3"
)

var ErrSchedulerStopped = errors.New("check-in scheduler has been stopped")

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
}

func NewScheduler(runner BatchRunner, expression string, logger *log.Logger) (*Scheduler, error) {
	if runner == nil {
		return nil, errors.New("check-in scheduler runner is required")
	}
	ctx, cancel := context.WithCancel(context.Background())
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	scheduler := &Scheduler{
		runner: runner,
		cron:   cron.New(cron.WithParser(parser)),
		logger: logger,
		ctx:    ctx,
		cancel: cancel,
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

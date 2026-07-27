package webdavsync

import (
	"context"
	"errors"
	"log"
	"sync"

	"github.com/robfig/cron/v3"
)

var ErrSchedulerStopped = errors.New("webdav scheduler has been stopped")

// ScheduledRunner runs a scheduled WebDAV import.
type ScheduledRunner interface {
	RunScheduled(context.Context) (*SyncResult, error)
}

// Scheduler runs opt-in cron pulls of the remote AAH backup.
type Scheduler struct {
	runner ScheduledRunner
	cron   *cron.Cron
	logger *log.Logger
	ctx    context.Context
	cancel context.CancelFunc

	lifecycleMu sync.Mutex
	started     bool
	stopped     bool
}

func NewScheduler(runner ScheduledRunner, expression string, logger *log.Logger) (*Scheduler, error) {
	if runner == nil {
		return nil, errors.New("webdav scheduler runner is required")
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

func (s *Scheduler) run(ctx context.Context) {
	result, err := s.runner.RunScheduled(ctx)
	if err != nil {
		if s.logger != nil && !errors.Is(err, context.Canceled) {
			category := CategoryUpstream
			if result != nil && result.Category != "" {
				category = result.Category
			}
			s.logger.Printf("scheduled webdav sync failed category=%s", category)
		}
		return
	}
	if s.logger != nil && result != nil {
		s.logger.Printf("scheduled webdav sync completed status=%s", result.Status)
	}
}

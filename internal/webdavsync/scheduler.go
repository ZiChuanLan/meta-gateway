package webdavsync

import (
	"context"
	"errors"
	"log"
	"strings"
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
	entryID     cron.EntryID
	expression  string
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
	if err := scheduler.SetSchedule(expression, expression != ""); err != nil {
		cancel()
		return nil, err
	}
	return scheduler, nil
}

// SetSchedule replaces the cron entry atomically. An empty expression or a
// false enabled flag disarms the scheduler but keeps the scheduler object
// alive, so a later Admin update can enable it without a process restart.
// Invalid expressions leave the previous entry untouched.
func (s *Scheduler) SetSchedule(expression string, enabled bool) error {
	expression = strings.TrimSpace(expression)
	if !enabled {
		expression = ""
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.stopped {
		return ErrSchedulerStopped
	}
	if expression == s.expression {
		return nil
	}
	var nextID cron.EntryID
	if expression != "" {
		id, err := s.cron.AddFunc(expression, func() {
			s.run(s.ctx)
		})
		if err != nil {
			return err
		}
		nextID = id
	}
	if s.entryID != 0 {
		s.cron.Remove(s.entryID)
	}
	s.entryID = nextID
	s.expression = expression
	return nil
}

// Armed reports whether a cron entry is currently installed.
func (s *Scheduler) Armed() bool {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	return !s.stopped && s.entryID != 0
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
	if ctx == nil {
		ctx = context.Background()
	}
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

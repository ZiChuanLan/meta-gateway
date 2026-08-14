package discovery

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
)

// ErrInvalidDiscoveryCron is returned when a hot-reload cron expression is
// invalid or the scheduler has been stopped.
var ErrInvalidDiscoveryCron = errors.New("invalid discovery cron expression")

// Runner executes one scheduled model-refresh pass.
type Runner interface {
	RefreshAll(ctx context.Context) (*RefreshSummary, error)
}

// Scheduler runs discovery.RefreshAll on a five-field cron expression.
// Empty expression or enabled=false stops the schedule (a no-op until
// re-enabled). SetSchedule is safe to call concurrently and applies live.
type Scheduler struct {
	mu      sync.Mutex
	cron    *cron.Cron
	runner  Runner
	ctx     context.Context
	cancel  context.CancelFunc
	started bool
	stopped bool
	entryID cron.EntryID
}

// NewScheduler builds a discovery scheduler for the given five-field cron
// expression ("" = disabled). The expression can be changed later through
// SetSchedule.
func NewScheduler(runner Runner, expression string, logger *log.Logger, location *time.Location) *Scheduler {
	if logger == nil {
		logger = log.Default()
	}
	if location == nil {
		location = time.Local
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Scheduler{
		runner: runner,
		ctx:    ctx,
		cancel: cancel,
		cron:   cron.New(cron.WithParser(cron.NewParser(cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow)), cron.WithLocation(location)),
	}
	s.cron.Start()
	s.started = true
	s.applyExpression(expression)
	return s
}

// SetSchedule swaps the cron expression ("" = disabled) and applies it live.
func (s *Scheduler) SetSchedule(expression string, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return ErrInvalidDiscoveryCron
	}
	if !enabled || expression == "" {
		expression = ""
	}
	return s.applyExpression(expression)
}

// applyExpression must be called with s.mu held.
func (s *Scheduler) applyExpression(expression string) error {
	if expression == "" {
		if s.entryID != 0 {
			s.cron.Remove(s.entryID)
			s.entryID = 0
		}
		return nil
	}
	entryID, err := s.cron.AddFunc(expression, func() {
		ctx, cancel := context.WithTimeout(s.ctx, 10*time.Minute)
		defer cancel()
		if _, err := s.runner.RefreshAll(ctx); err != nil {
			log.Printf("discovery: scheduled refresh failed: %v", err)
		}
	})
	if err != nil {
		return err
	}
	if s.entryID != 0 {
		s.cron.Remove(s.entryID)
	}
	s.entryID = entryID
	return nil
}

// Stop halts the scheduler permanently.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return
	}
	s.stopped = true
	s.cron.Stop()
	s.cancel()
}

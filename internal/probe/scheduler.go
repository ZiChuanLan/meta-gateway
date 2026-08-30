package probe

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/lan/meta-gateway/internal/store"
	"github.com/robfig/cron/v3"
)

// ErrSchedulerStopped is returned when a schedule is applied after Stop.
var ErrSchedulerStopped = errors.New("probe: scheduler stopped")

// Schedule is one scheduled probe run: when to fire and what to probe.
type Schedule struct {
	// Expr is a five-field cron expression. Empty disables the schedule.
	Expr string
	// Options carries the prompt, cost and auto-disable settings for the run.
	Options Options
	// ChannelIDs and Models scope the run; empty slices mean everything.
	ChannelIDs []int64
	Models     []string
}

// Scheduler runs a probe task on a cron expression. It mirrors the discovery
// scheduler: SetSchedule applies live, Stop is permanent, and a firing that
// finds a run already in progress is skipped rather than stacked.
type Scheduler struct {
	db      *store.DB
	service *Service
	logger  *slog.Logger

	mu      sync.Mutex
	cron    *cron.Cron
	entryID cron.EntryID
	stopped bool

	ctx    context.Context
	cancel context.CancelFunc
}

// NewScheduler builds a probe scheduler. An empty Schedule.Expr leaves it idle
// until SetSchedule supplies one.
func NewScheduler(db *store.DB, service *Service, schedule Schedule, logger *slog.Logger, location *time.Location) *Scheduler {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if location == nil {
		location = time.Local
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Scheduler{
		db:      db,
		service: service,
		logger:  logger,
		ctx:     ctx,
		cancel:  cancel,
		cron: cron.New(cron.WithParser(
			cron.NewParser(cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow),
		), cron.WithLocation(location)),
	}
	s.cron.Start()
	s.apply(schedule)
	return s
}

// SetSchedule swaps the schedule and applies it live. Pass an empty Expr to
// disable.
func (s *Scheduler) SetSchedule(schedule Schedule) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return ErrSchedulerStopped
	}
	return s.apply(schedule)
}

// apply must be called with s.mu held.
func (s *Scheduler) apply(schedule Schedule) error {
	if s.entryID != 0 {
		s.cron.Remove(s.entryID)
		s.entryID = 0
	}
	if schedule.Expr == "" {
		return nil
	}
	entryID, err := s.cron.AddFunc(schedule.Expr, func() { s.fire(schedule) })
	if err != nil {
		return err
	}
	s.entryID = entryID
	return nil
}

// fire runs one scheduled pass. A previous run still going is skipped: probes
// are real upstream traffic, and two overlapping runs would double the load on
// every channel for no extra information.
func (s *Scheduler) fire(schedule Schedule) {
	tasks, err := s.db.ListProbeTasks(5)
	if err != nil {
		s.logger.Warn("probe schedule: list tasks", "error", err)
		return
	}
	for _, task := range tasks {
		if task.Status == store.ProbeTaskRunning {
			s.logger.Info("probe schedule: skipped, a run is already in progress", "task", task.ID)
			return
		}
	}

	pairs, err := CandidatePairs(s.db, schedule.ChannelIDs, schedule.Models)
	if err != nil {
		s.logger.Warn("probe schedule: build candidates", "error", err)
		return
	}
	if len(pairs) == 0 {
		s.logger.Info("probe schedule: nothing to probe")
		return
	}
	// The run is deliberately tied to the scheduler's own context rather than a
	// per-firing one: Start returns immediately and the worker keeps going, so
	// a deferred cancel here would kill the run the moment this function
	// returned. Stop() is what cancels it. Individual probes still have their
	// own timeout, so a hung upstream cannot wedge the schedule for long.
	task, err := s.service.Start(s.ctx, pairs, schedule.Options)
	if err != nil {
		s.logger.Warn("probe schedule: start", "error", err)
		return
	}
	s.logger.Info("probe schedule: started",
		"task", task.ID, "pairs", len(pairs), "cron", schedule.Expr)
}

// Stop halts the scheduler permanently.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	s.stopped = true
	s.cancel()
	engine := s.cron
	s.mu.Unlock()
	<-engine.Stop().Done()
}

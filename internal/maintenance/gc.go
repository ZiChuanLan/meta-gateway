// Package maintenance runs periodic database upkeep: orphan-row GC and
// SQLite VACUUM on a configurable cron schedule.
package maintenance

import (
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/lan/meta-gateway/internal/store"
)

const defaultGCInitialDelay = 2 * time.Minute

// GCService schedules store.GC() passes. SetSchedule hot-reloads the
// expression ("" disables); the first transition from disabled to enabled
// also runs a delayed pass so an operator does not have to wait for the next
// cron boundary.
type GCService struct {
	db   *store.DB
	cron *cron.Cron

	mu         sync.Mutex
	entryID    cron.EntryID
	expression string
	stopped    bool

	ctx         context.Context
	cancel      context.CancelFunc
	delayCancel context.CancelFunc
	wg          sync.WaitGroup
	runMu       sync.Mutex

	last     *store.GCResult
	lastTime time.Time

	// Kept as a field so tests can use a short delay without sleeping two
	// minutes; production always gets the conservative default.
	initialDelay time.Duration
}

// New creates the maintenance scheduler. expression "" = disabled.
func New(db *store.DB, expression string) *GCService {
	ctx, cancel := context.WithCancel(context.Background())
	s := &GCService{
		db:           db,
		cron:         cron.New(),
		ctx:          ctx,
		cancel:       cancel,
		initialDelay: defaultGCInitialDelay,
	}
	// Start the cron engine once; entries can then be replaced live without
	// leaking one engine per settings update.
	s.cron.Start()
	if strings.TrimSpace(expression) != "" {
		_ = s.SetSchedule(expression)
	}
	return s
}

// SetSchedule swaps the cron expression ("" = disabled). Invalid expressions
// are rejected before the current schedule is touched.
func (s *GCService) SetSchedule(expression string) error {
	expression = strings.TrimSpace(expression)
	if expression != "" {
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		if _, err := parser.Parse(expression); err != nil {
			return err
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return context.Canceled
	}
	if expression == s.expression {
		return nil
	}
	wasEnabled := s.entryID != 0
	var nextID cron.EntryID
	if expression != "" {
		id, err := s.cron.AddFunc(expression, func() { s.run() })
		if err != nil {
			return err
		}
		nextID = id
	}
	if s.entryID != 0 {
		s.cron.Remove(s.entryID)
	}
	if s.delayCancel != nil {
		s.delayCancel()
		s.delayCancel = nil
	}
	s.entryID = nextID
	s.expression = expression
	if expression != "" && !wasEnabled {
		ctx, cancel := context.WithCancel(s.ctx)
		s.delayCancel = cancel
		delay := s.initialDelay
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-timer.C:
				s.run()
			case <-ctx.Done():
			}
		}()
	}
	return nil
}

// Stop halts the scheduler and waits for cron callbacks and delayed work to
// exit. It is safe to call repeatedly.
func (s *GCService) Stop() {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	s.stopped = true
	s.cancel()
	if s.delayCancel != nil {
		s.delayCancel()
		s.delayCancel = nil
	}
	cronEngine := s.cron
	s.mu.Unlock()
	wait := cronEngine.Stop()
	<-wait.Done()
	s.wg.Wait()
}

// RunOnce executes a maintenance pass synchronously and records it as the
// most recent result.
func (s *GCService) RunOnce() (*store.GCResult, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("maintenance: database unavailable")
	}
	s.runMu.Lock()
	defer s.runMu.Unlock()
	res, err := s.db.GC()
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.last = res
	s.lastTime = time.Now()
	s.mu.Unlock()
	return res, nil
}

// Last returns a copy of the most recent pass result (nil when none ran yet).
func (s *GCService) Last() (*store.GCResult, time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.last == nil {
		return nil, s.lastTime
	}
	copyResult := *s.last
	return &copyResult, s.lastTime
}

func (s *GCService) run() {
	res, err := s.RunOnce()
	if err != nil {
		log.Printf("maintenance: gc pass failed: %v", err)
		return
	}
	log.Printf("maintenance: gc pass deleted route_members=%d proxy_logs=%d discovered=%d checkin_logs=%d usage=%d balance=%d decision=%d health=%d blocks=%d redemptions=%d error_rules=%d vacuumed=%v freed_bytes=%d",
		res.RouteMembers, res.ProxyLogs, res.Discovered, res.CheckinLogs, res.UsageRecords,
		res.BalanceHistory, res.DecisionSnaps, res.HealthHistory,
		res.ModelBlocks, res.Redemptions, res.ErrorRules, res.Vacuumed, res.VacuumFreedBytes)
}

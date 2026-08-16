// Package maintenance runs periodic database upkeep: orphan-row GC and
// SQLite VACUUM on a configurable cron schedule.
package maintenance

import (
	"log"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/lan/meta-gateway/internal/store"
)

// GCService schedules store.GC() passes. SetSchedule hot-reloads the
// expression ("" disables); the job also runs once immediately after the
// first non-empty schedule is set, so enabling maintenance takes effect
// without waiting for the next cron tick.
type GCService struct {
	db       *store.DB
	cron     *cron.Cron
	mu       sync.Mutex
	last     *store.GCResult
	lastTime time.Time
}

// New creates the maintenance scheduler. expression "" = disabled.
func New(db *store.DB, expression string) *GCService {
	s := &GCService{db: db}
	if expression != "" {
		_ = s.SetSchedule(expression)
	}
	return s
}

// SetSchedule swaps the cron expression ("" = disabled). Invalid expressions
// are rejected without touching the current schedule.
func (s *GCService) SetSchedule(expression string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cron != nil {
		s.cron.Stop()
		s.cron = nil
	}
	expression = strings.TrimSpace(expression)
	if expression == "" {
		return nil
	}
	s.cron = cron.New()
	if _, err := s.cron.AddFunc(expression, func() { s.run() }); err != nil {
		s.cron = nil
		return err
	}
	s.cron.Start()
	// First pass shortly after enable (deferred so boot-time migrations and
	// route loading settle first).
	go func() {
		time.Sleep(2 * time.Minute)
		s.run()
	}()
	return nil
}

// Stop halts the scheduler.
func (s *GCService) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cron != nil {
		s.cron.Stop()
		s.cron = nil
	}
}

// RunOnce executes a maintenance pass synchronously and records it as the
// most recent result.
func (s *GCService) RunOnce() (*store.GCResult, error) {
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

// Last returns the most recent pass result (nil when none ran yet).
func (s *GCService) Last() (*store.GCResult, time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last, s.lastTime
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

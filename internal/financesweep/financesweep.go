// Package financesweep wires operational alerts (balance low / token expired /
// checkin failed / request failures) and the scheduled daily summary into the
// multi-channel notifier.
package financesweep

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/lan/meta-gateway/internal/account"
	"github.com/lan/meta-gateway/internal/store"
	"github.com/lan/meta-gateway/internal/webhook"
)

// Sweep is the proactive background health sweep: it periodically refreshes
// the finance overview (balance-low alerts) and probes every channel's access
// token (token-expired alerts) so those alerts fire without an operator
// visiting the admin pages.
type Sweep struct {
	account  FinanceProber
	notifier *webhook.Notifier
	mu       sync.RWMutex
	interval time.Duration
	stopCh   chan struct{}
	stopOnce sync.Once
	updateCh chan struct{}
}

// FinanceProber is the subset of the account service the sweep needs.
type FinanceProber interface {
	FinanceOverview(ctx context.Context) ([]account.FinanceItem, error)
	ProbeAll(ctx context.Context) ([]account.ProbeAllItem, error)
}

// NewSweep creates the background sweep. interval <= 0 disables it.
func NewSweep(account FinanceProber, notifier *webhook.Notifier, interval time.Duration) *Sweep {
	return &Sweep{account: account, notifier: notifier, interval: interval, stopCh: make(chan struct{}), updateCh: make(chan struct{}, 1)}
}

// Start launches the sweep loop. The goroutine always runs so a later
// SetInterval can start an initially disabled sweep without a restart.
func (s *Sweep) Start() {
	go func() {
		ticker := s.newTicker()
		defer func() {
			if ticker != nil {
				ticker.Stop()
			}
		}()
		for {
			select {
			case <-s.updateCh:
				if ticker != nil {
					ticker.Stop()
				}
				ticker = s.newTicker()
			case <-s.tick(ticker):
				s.runOnce()
			case <-s.stopCh:
				return
			}
		}
	}()
}

// SetInterval hot-reloads the sweep cadence (<= 0 pauses the loop).
func (s *Sweep) SetInterval(interval time.Duration) {
	s.mu.Lock()
	s.interval = interval
	s.mu.Unlock()
	select {
	case s.updateCh <- struct{}{}:
	default:
	}
}

// Stop halts the sweep loop. Idempotent: a second call is a no-op.
func (s *Sweep) Stop() {
	s.stopOnce.Do(func() { close(s.stopCh) })
}

// newTicker returns nil when the current interval is disabled.
func (s *Sweep) newTicker() *time.Ticker {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.interval <= 0 || s.account == nil {
		return nil
	}
	return time.NewTicker(s.interval)
}

// tick resolves a nil ticker (disabled) to a channel that never fires.
func (s *Sweep) tick(ticker *time.Ticker) <-chan time.Time {
	if ticker == nil {
		return nil
	}
	return ticker.C
}

func (s *Sweep) runOnce() {
	s.mu.RLock()
	account := s.account
	s.mu.RUnlock()
	if account == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	// FinanceOverview refreshes the cache and fires balance-low alerts on
	// channels below one unit.
	if _, err := account.FinanceOverview(ctx); err != nil {
		log.Printf("alert: sweep finance: %v", err)
	}
	// ProbeAll re-checks every enabled channel's access token; 401/403 probes
	// fire token-expired alerts.
	if _, err := account.ProbeAll(ctx); err != nil {
		log.Printf("alert: sweep probe: %v", err)
	}
}

// DailySummary is the scheduled digest (Metapi dailySummaryService-inspired):
// aggregates channel health, failures and low balances once a day.
type DailySummary struct {
	db       *store.DB
	notifier *webhook.Notifier
	mu       sync.RWMutex
	interval time.Duration
	stopCh   chan struct{}
	stopOnce sync.Once
	updateCh chan struct{}
	now      func() time.Time
}

// NewDailySummary creates the daily digest runner. interval <= 0 or
// enabled=false disables it.
func NewDailySummary(db *store.DB, notifier *webhook.Notifier, interval time.Duration, enabled bool) *DailySummary {
	if !enabled {
		interval = 0
	}
	return &DailySummary{
		db:       db,
		notifier: notifier,
		interval: interval,
		stopCh:   make(chan struct{}),
		updateCh: make(chan struct{}, 1),
		now:      time.Now,
	}
}

// Start launches the digest loop (runs once immediately, then on the tick).
// The goroutine always runs so a later SetInterval can start an initially
// disabled digest without a restart.
func (s *DailySummary) Start() {
	go func() {
		// Preserve the original semantics: an initially disabled digest does
		// not run once at startup (a later SetInterval restart only ticks).
		s.mu.RLock()
		initialEnabled := s.interval > 0
		s.mu.RUnlock()
		if initialEnabled {
			s.runOnce()
		}
		ticker := s.newTicker()
		defer func() {
			if ticker != nil {
				ticker.Stop()
			}
		}()
		for {
			select {
			case <-s.updateCh:
				if ticker != nil {
					ticker.Stop()
				}
				ticker = s.newTicker()
			case <-s.tick(ticker):
				s.runOnce()
			case <-s.stopCh:
				return
			}
		}
	}()
}

// SetInterval hot-reloads the digest cadence (<= 0 pauses the loop).
func (s *DailySummary) SetInterval(interval time.Duration) {
	s.mu.Lock()
	s.interval = interval
	s.mu.Unlock()
	select {
	case s.updateCh <- struct{}{}:
	default:
	}
}

// Stop halts the digest loop. Idempotent: a second call is a no-op.
func (s *DailySummary) Stop() {
	s.stopOnce.Do(func() { close(s.stopCh) })
}

// newTicker returns nil when the current interval is disabled.
func (s *DailySummary) newTicker() *time.Ticker {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.interval <= 0 || s.db == nil || s.notifier == nil {
		return nil
	}
	return time.NewTicker(s.interval)
}

// tick resolves a nil ticker (disabled) to a channel that never fires.
func (s *DailySummary) tick(ticker *time.Ticker) <-chan time.Time {
	if ticker == nil {
		return nil
	}
	return ticker.C
}

func (s *DailySummary) runOnce() {
	s.mu.RLock()
	db := s.db
	s.mu.RUnlock()
	if db == nil {
		return
	}
	overviews, err := db.Channel.ListOverviews(s.now())
	if err != nil {
		log.Printf("alert: daily summary channel list: %v", err)
		return
	}
	total := len(overviews)
	var healthy, degraded, unhealthy, disabled, autoDisabled int
	var failures int64
	for _, o := range overviews {
		failures += int64(o.FailureCount)
		switch o.HealthState {
		case "healthy":
			healthy++
		case "degraded":
			degraded++
			if o.FailureCount > 0 {
				unhealthy++ // reported separately below
			}
		case "unhealthy":
			unhealthy++
		case "disabled":
			disabled++
		}
		if o.Channel.Status == "auto_disabled" {
			autoDisabled++
		}
	}
	var lines []string
	lines = append(lines, fmt.Sprintf("渠道总数: %d", total))
	lines = append(lines, fmt.Sprintf("健康: %d · 降级: %d · 不健康: %d · 已禁用: %d · 自动禁用: %d", healthy, degraded, unhealthy, disabled, autoDisabled))
	lines = append(lines, fmt.Sprintf("累计失败成员: %d", failures))
	lines = append(lines, "低余额: 见各连接详情页")
	s.notifier.SendAlert(context.Background(), webhook.AlertInfo, "每日摘要", strings.Join(lines, "\n"))
}

package maintenance

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/lan/meta-gateway/internal/store"
)

// BalanceAccount is the account-service surface the daily sweep needs; the
// concrete *account.Service satisfies it structurally.
type BalanceAccount interface {
	RecordBalanceHistory(ctx context.Context) (int, error)
	BalanceHistory(ctx context.Context, days int) ([]store.BalanceHistoryPoint, error)
	PruneBalanceHistory(ctx context.Context, retentionDays int) (int, error)
}

// BalanceSweeper records each channel's balance once a day (plus a first
// snapshot shortly after boot when the table is empty) so the dashboard
// trend chart has data without waiting 24h, and prunes expired rows from
// the balance-history, decision-snapshot and health-history tables on the
// same cadence.
type BalanceSweeper struct {
	account             BalanceAccount
	db                  *store.DB
	healthRetentionDays int
	logger              *slog.Logger

	stopOnce sync.Once
	stop     chan struct{}
	done     chan struct{}
}

// NewBalanceSweeper builds the daily sweep; healthRetentionDays <= 0 falls
// back to the 90-day default.
func NewBalanceSweeper(account BalanceAccount, db *store.DB, healthRetentionDays int, logger *slog.Logger) *BalanceSweeper {
	if healthRetentionDays <= 0 {
		healthRetentionDays = 90
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &BalanceSweeper{
		account:             account,
		db:                  db,
		healthRetentionDays: healthRetentionDays,
		logger:              logger,
		stop:                make(chan struct{}),
		done:                make(chan struct{}),
	}
}

// Start launches the sweep loop; the first snapshot attempt happens ~30s
// after boot when there is no history yet, then once every 24h.
func (s *BalanceSweeper) Start() {
	go func() {
		defer close(s.done)
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		time.Sleep(30 * time.Second)
		if points, err := s.account.BalanceHistory(context.Background(), 2); err == nil && len(points) == 0 {
			s.run()
		}
		for {
			select {
			case <-ticker.C:
				s.run()
			case <-s.stop:
				return
			}
		}
	}()
}

// Stop terminates the sweep loop and waits for it to exit. Safe to call once.
func (s *BalanceSweeper) Stop() {
	s.stopOnce.Do(func() { close(s.stop) })
	<-s.done
}

func (s *BalanceSweeper) run() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if n, err := s.account.RecordBalanceHistory(ctx); err != nil {
		s.logger.Warn("balance history snapshot failed", "error", err)
	} else if n > 0 {
		s.logger.Info("balance history snapshot", "channels", n)
	}
	if _, err := s.account.PruneBalanceHistory(ctx, 90); err != nil {
		s.logger.Warn("balance history prune failed", "error", err)
	}
	if _, err := s.db.PruneDecisionSnapshots(7); err != nil {
		s.logger.Warn("decision snapshot prune failed", "error", err)
	}
	if _, err := s.db.HealthHistory.Prune(time.Now().AddDate(0, 0, -s.healthRetentionDays)); err != nil {
		s.logger.Warn("health history prune failed", "error", err)
	}
}

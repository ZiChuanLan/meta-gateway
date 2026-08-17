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
	account               BalanceAccount
	db                    *store.DB
	balanceRetentionDays  int
	decisionRetentionDays int
	healthRetentionDays   int
	logger                *slog.Logger

	lifecycleMu sync.Mutex
	started     bool
	stopped     bool
	ctx         context.Context
	cancel      context.CancelFunc
	done        chan struct{}
}

// RetentionConfig controls the daily maintenance pruning windows. A value of
// zero disables that particular pruner; negative values select the default.
type RetentionConfig struct {
	BalanceHistoryDays   int
	DecisionSnapshotDays int
	HealthHistoryDays    int
}

const (
	DefaultBalanceHistoryRetentionDays   = 90
	DefaultDecisionSnapshotRetentionDays = 7
	DefaultHealthHistoryRetentionDays    = 90
)

// NewBalanceSweeper preserves the historical constructor defaults.
func NewBalanceSweeper(account BalanceAccount, db *store.DB, healthRetentionDays int, logger *slog.Logger) *BalanceSweeper {
	if healthRetentionDays <= 0 {
		healthRetentionDays = DefaultHealthHistoryRetentionDays
	}
	return NewBalanceSweeperWithRetention(account, db, RetentionConfig{
		BalanceHistoryDays:   DefaultBalanceHistoryRetentionDays,
		DecisionSnapshotDays: DefaultDecisionSnapshotRetentionDays,
		HealthHistoryDays:    healthRetentionDays,
	}, logger)
}

// NewBalanceSweeperWithRetention builds a sweep with independently configured
// retention windows. It is used by the application config path so a zero
// value can intentionally disable pruning.
func NewBalanceSweeperWithRetention(account BalanceAccount, db *store.DB, retention RetentionConfig, logger *slog.Logger) *BalanceSweeper {
	if retention.BalanceHistoryDays < 0 {
		retention.BalanceHistoryDays = DefaultBalanceHistoryRetentionDays
	}
	if retention.DecisionSnapshotDays < 0 {
		retention.DecisionSnapshotDays = DefaultDecisionSnapshotRetentionDays
	}
	if retention.HealthHistoryDays < 0 {
		retention.HealthHistoryDays = DefaultHealthHistoryRetentionDays
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &BalanceSweeper{
		account:               account,
		db:                    db,
		balanceRetentionDays:  retention.BalanceHistoryDays,
		decisionRetentionDays: retention.DecisionSnapshotDays,
		healthRetentionDays:   retention.HealthHistoryDays,
		logger:                logger,
		done:                  make(chan struct{}),
	}
}

// Start launches the sweep loop; the first snapshot attempt happens ~30s
// after boot when there is no history yet, then once every 24h.
func (s *BalanceSweeper) Start() {
	s.lifecycleMu.Lock()
	if s.started || s.stopped {
		s.lifecycleMu.Unlock()
		return
	}
	s.started = true
	s.ctx, s.cancel = context.WithCancel(context.Background())
	ctx := s.ctx
	s.lifecycleMu.Unlock()
	go func() {
		defer close(s.done)
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		if s.account == nil || s.db == nil {
			return
		}
		first := time.NewTimer(30 * time.Second)
		defer first.Stop()
		select {
		case <-ctx.Done():
			return
		case <-first.C:
		}
		if points, err := s.account.BalanceHistory(ctx, 2); err == nil && len(points) == 0 {
			s.run(ctx)
		}
		for {
			select {
			case <-ticker.C:
				s.run(ctx)
			case <-ctx.Done():
				return
			}
		}
	}()
}

// Stop terminates the sweep loop and waits for it to exit. Safe to call once.
func (s *BalanceSweeper) Stop() {
	s.lifecycleMu.Lock()
	if !s.started {
		s.stopped = true
		s.lifecycleMu.Unlock()
		return
	}
	if !s.stopped {
		s.stopped = true
		s.cancel()
	}
	done := s.done
	s.lifecycleMu.Unlock()
	<-done
}

func (s *BalanceSweeper) run(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, 3*time.Minute)
	defer cancel()
	if n, err := s.account.RecordBalanceHistory(ctx); err != nil {
		s.logger.Warn("balance history snapshot failed", "error", err)
	} else if n > 0 {
		s.logger.Info("balance history snapshot", "channels", n)
	}
	if err := ctx.Err(); err != nil {
		return
	}
	if s.balanceRetentionDays > 0 {
		if _, err := s.account.PruneBalanceHistory(ctx, s.balanceRetentionDays); err != nil {
			s.logger.Warn("balance history prune failed", "error", err)
		}
	}
	if err := ctx.Err(); err != nil {
		return
	}
	if s.decisionRetentionDays > 0 {
		if _, err := s.db.PruneDecisionSnapshots(s.decisionRetentionDays); err != nil {
			s.logger.Warn("decision snapshot prune failed", "error", err)
		}
	}
	if err := ctx.Err(); err != nil {
		return
	}
	if s.healthRetentionDays > 0 {
		if _, err := s.db.HealthHistory.Prune(time.Now().AddDate(0, 0, -s.healthRetentionDays)); err != nil {
			s.logger.Warn("health history prune failed", "error", err)
		}
	}
}

package maintenance

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

// fakeAccount satisfies BalanceAccount without touching the network. The sweep
// calls it for the balance snapshot; the probe pruner below is independent of it.
type fakeAccount struct{}

func (fakeAccount) RecordBalanceHistory(context.Context) (int, error) { return 0, nil }
func (fakeAccount) BalanceHistory(context.Context, int) ([]store.BalanceHistoryPoint, error) {
	return nil, nil
}
func (fakeAccount) PruneBalanceHistory(context.Context, int) (int, error) { return 0, nil }

// TestBalanceSweeperPrunesSiteProbe covers the wiring, not the SQL: the daily
// sweep is the only place probe history is trimmed, and site_probe_samples is
// the highest-volume table in the schema (one row per monitored model per site
// per round). A dropped call here would let it grow without bound while every
// store-level test still passed.
func TestBalanceSweeperPrunesSiteProbe(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	site, err := db.Site.Create(&domain.Site{Name: "probe-site", BaseURL: "https://a.example", Platform: "new-api", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().AddDate(0, 0, -30).Format("2006-01-02 15:04:05")
	inserted, err := db.Exec(`INSERT INTO site_probe_runs (site_id, source_kind, started_at, status) VALUES (?, 'uptime_kuma', ?, 'ok')`, site, old)
	if err != nil {
		t.Fatal(err)
	}
	runID, _ := inserted.LastInsertId()
	if _, err := db.Exec(`INSERT INTO site_probe_samples (run_id, site_id, monitor_name, raw_model, observed_at) VALUES (?, ?, 'glm-5.2', 'glm-5.2', ?)`, runID, site, old); err != nil {
		t.Fatal(err)
	}
	freshRun, err := db.CreateSiteProbeRun(site, "uptime_kuma", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}

	sweeper := NewBalanceSweeperWithRetention(fakeAccount{}, db,
		RetentionConfig{SiteProbeDays: 7},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	sweeper.run(context.Background())

	if got := countRows(t, db, `SELECT COUNT(*) FROM site_probe_samples`); got != 0 {
		t.Fatalf("samples after sweep = %d, want the aged round's sample gone", got)
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM site_probe_runs`); got != 1 {
		t.Fatalf("runs after sweep = %d, want only the fresh round %d", got, freshRun)
	}

	// The switch is honoured: 0 keeps the history.
	if _, err := db.Exec(`UPDATE site_probe_runs SET started_at = ? WHERE id = ?`, old, freshRun); err != nil {
		t.Fatal(err)
	}
	disabled := NewBalanceSweeperWithRetention(fakeAccount{}, db, RetentionConfig{SiteProbeDays: 0},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	disabled.run(context.Background())
	if got := countRows(t, db, `SELECT COUNT(*) FROM site_probe_runs`); got != 1 {
		t.Fatalf("runs after a disabled pruner = %d, want the row kept", got)
	}
}

func countRows(t *testing.T, db *store.DB, query string) int {
	t.Helper()
	var value int
	if err := db.QueryRow(query).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

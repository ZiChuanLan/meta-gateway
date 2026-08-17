package store

import (
	"fmt"
	"time"
)

// BalanceHistoryPoint is one channel's balance at a probe time.
type BalanceHistoryPoint struct {
	ID          int64     `json:"id"`
	ChannelID   int64     `json:"channel_id"`
	ChannelName string    `json:"channel_name"`
	Balance     int64     `json:"balance"`
	ProbedAt    time.Time `json:"probed_at"`
}

// InsertBalanceHistory records one channel's balance snapshot.
func (s *DB) InsertBalanceHistory(channelID int64, channelName string, balance int64, probedAt time.Time) error {
	_, err := s.Exec(
		`INSERT INTO balance_history (channel_id, channel_name, balance, probed_at) VALUES (?, ?, ?, ?)`,
		channelID, channelName, balance, probedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("balance history insert: %w", err)
	}
	return nil
}

// ListBalanceHistory returns all snapshots within the last N days in
// chronological order (oldest first), which is the natural order for trend
// charts.
func (s *DB) ListBalanceHistory(days int) ([]BalanceHistoryPoint, error) {
	if days <= 0 || days > 365 {
		days = 30
	}
	since := time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour).Format(time.RFC3339Nano)
	rows, err := s.Query(
		`SELECT id, channel_id, channel_name, balance, probed_at FROM balance_history
		 WHERE probed_at >= ? ORDER BY probed_at ASC, id ASC`, since)
	if err != nil {
		return nil, fmt.Errorf("balance history list: %w", err)
	}
	defer rows.Close()
	out := make([]BalanceHistoryPoint, 0, 64)
	for rows.Next() {
		var p BalanceHistoryPoint
		var probed string
		if err := rows.Scan(&p.ID, &p.ChannelID, &p.ChannelName, &p.Balance, &probed); err != nil {
			return nil, fmt.Errorf("balance history scan: %w", err)
		}
		if t, err := time.Parse(time.RFC3339Nano, probed); err == nil {
			p.ProbedAt = t
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// PruneBalanceHistory deletes snapshots older than the retention window and
// returns how many rows were removed.
func (s *DB) PruneBalanceHistory(retentionDays int) (int, error) {
	if retentionDays <= 0 {
		return 0, nil
	}
	before := time.Now().UTC().Add(-time.Duration(retentionDays) * 24 * time.Hour).Format(time.RFC3339Nano)
	res, err := s.Exec(`DELETE FROM balance_history WHERE probed_at < ?`, before)
	if err != nil {
		return 0, fmt.Errorf("balance history prune: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

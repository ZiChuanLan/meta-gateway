package store

import (
	"encoding/json"
	"fmt"
	"time"
)

// FinanceSnapshotRow is one persisted channel finance observation. Payload is
// the caller's allowlisted JSON; the store never inspects account data.
type FinanceSnapshotRow struct {
	ChannelID  int64
	ObservedAt time.Time
	Payload    json.RawMessage
}

// SaveFinanceSnapshot appends one channel's finance observation. History is
// bounded to the last 20 observations per channel. Account balance_history is
// the historical ledger; this table is the reloadable UI snapshot.
func (s *DB) SaveFinanceSnapshot(channelID int64, payload []byte, observedAt time.Time) error {
	if channelID <= 0 {
		return fmt.Errorf("finance snapshot: channel id required")
	}
	tx, err := s.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(
		`INSERT INTO finance_snapshots (channel_id, observed_at, payload) VALUES (?, ?, ?)`,
		channelID, observedAt.UTC().Format(time.RFC3339Nano), string(payload),
	)
	if err != nil {
		return fmt.Errorf("finance snapshot insert: %w", err)
	}
	if _, err = tx.Exec(`DELETE FROM finance_snapshots WHERE channel_id=? AND id NOT IN (SELECT id FROM finance_snapshots WHERE channel_id=? ORDER BY id DESC LIMIT 20)`, channelID, channelID); err != nil {
		return err
	}
	return tx.Commit()
}

// LatestFinanceSnapshots returns the most recent observation per channel so a
// restart can serve the console before the first refresh completes.
func (s *DB) LatestFinanceSnapshots() ([]FinanceSnapshotRow, error) {
	rows, err := s.Query(`SELECT f.channel_id, f.observed_at, f.payload
		FROM finance_snapshots f
		WHERE f.id = (SELECT MAX(p.id) FROM finance_snapshots p WHERE p.channel_id = f.channel_id)
		ORDER BY f.channel_id ASC`)
	if err != nil {
		return nil, fmt.Errorf("finance snapshot list: %w", err)
	}
	defer rows.Close()
	out := make([]FinanceSnapshotRow, 0, 16)
	for rows.Next() {
		var row FinanceSnapshotRow
		var observed string
		var payload []byte
		if err = rows.Scan(&row.ChannelID, &observed, &payload); err != nil {
			return nil, fmt.Errorf("finance snapshot scan: %w", err)
		}
		row.Payload = json.RawMessage(payload)
		if parsed, parseErr := time.Parse(time.RFC3339Nano, observed); parseErr == nil {
			row.ObservedAt = parsed
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

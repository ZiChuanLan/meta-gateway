package store

import (
	"fmt"
	"time"
)

// ModelBlock records a channel × model combination reported by the upstream
// as not found; routing skips it until manually cleared.
type ModelBlock struct {
	ID        int64     `json:"id"`
	ChannelID int64     `json:"channel_id"`
	Model     string    `json:"model"`
	Reason    string    `json:"reason"`
	CreatedAt time.Time `json:"created_at"`
}

// IsModelBlocked reports whether channelID cannot serve model.
func (s *DB) IsModelBlocked(channelID int64, model string) (bool, error) {
	var one int
	err := s.QueryRow(
		`SELECT 1 FROM channel_model_blocks WHERE channel_id = ? AND model = ? LIMIT 1`,
		channelID, model,
	).Scan(&one)
	if err != nil {
		return false, nil // not blocked (or lookup error — treat as not blocked)
	}
	return true, nil
}

// BlockModel inserts a block; duplicate inserts are ignored.
func (s *DB) BlockModel(channelID int64, model, reason string) error {
	if channelID <= 0 || model == "" {
		return nil
	}
	_, err := s.Exec(
		`INSERT OR IGNORE INTO channel_model_blocks (channel_id, model, reason, created_at) VALUES (?, ?, ?, ?)`,
		channelID, model, reason, time.Now().UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("model block insert: %w", err)
	}
	return nil
}

// UnblockModel removes a block (manual clear).
func (s *DB) UnblockModel(channelID int64, model string) error {
	_, err := s.Exec(
		`DELETE FROM channel_model_blocks WHERE channel_id = ? AND model = ?`,
		channelID, model,
	)
	if err != nil {
		return fmt.Errorf("model block delete: %w", err)
	}
	return nil
}

// ListModelBlocks returns all blocks, newest first.
func (s *DB) ListModelBlocks() ([]ModelBlock, error) {
	rows, err := s.Query(
		`SELECT id, channel_id, model, reason, created_at FROM channel_model_blocks ORDER BY id DESC LIMIT 500`,
	)
	if err != nil {
		return nil, fmt.Errorf("model block list: %w", err)
	}
	defer rows.Close()
	var out []ModelBlock
	for rows.Next() {
		var b ModelBlock
		var created string
		if err := rows.Scan(&b.ID, &b.ChannelID, &b.Model, &b.Reason, &created); err != nil {
			return nil, fmt.Errorf("model block scan: %w", err)
		}
		if t, err := time.Parse(time.RFC3339Nano, created); err == nil {
			b.CreatedAt = t
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

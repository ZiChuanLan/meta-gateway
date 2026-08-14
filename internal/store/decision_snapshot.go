package store

import (
	"encoding/json"
	"fmt"
	"time"
)

// DecisionSnapshot is one routing decision persisted for audit: the full
// explanation (candidates, scores, reasons, sticky/stable-first state) plus
// the serving channel that was selected.
type DecisionSnapshot struct {
	ID                int64           `json:"id"`
	RequestID         string          `json:"request_id"`
	Model             string          `json:"model"`
	RouteID           int64           `json:"route_id"`
	SelectedChannelID int64           `json:"selected_channel_id"`
	Payload           json.RawMessage `json:"payload"`
	CreatedAt         time.Time       `json:"created_at"`
}

// InsertDecisionSnapshot stores one routing decision. payload must be the
// serialized routing explanation; it is stored verbatim.
func (s *DB) InsertDecisionSnapshot(requestID, model string, routeID, selectedChannelID int64, payload []byte, at time.Time) error {
	if requestID == "" {
		return nil
	}
	_, err := s.Exec(
		`INSERT INTO decision_snapshots (request_id, model, route_id, selected_channel_id, payload, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		requestID, model, routeID, selectedChannelID, string(payload), at.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("decision snapshot insert: %w", err)
	}
	return nil
}

// LatestDecisionSnapshot returns the most recent snapshot for a request id.
func (s *DB) LatestDecisionSnapshot(requestID string) (*DecisionSnapshot, error) {
	if requestID == "" {
		return nil, nil
	}
	row := s.QueryRow(
		`SELECT id, request_id, model, route_id, selected_channel_id, payload, created_at FROM decision_snapshots WHERE request_id = ? ORDER BY id DESC LIMIT 1`,
		requestID,
	)
	var snap DecisionSnapshot
	var payload, created string
	if err := row.Scan(&snap.ID, &snap.RequestID, &snap.Model, &snap.RouteID, &snap.SelectedChannelID, &payload, &created); err != nil {
		return nil, nil // no snapshot for this request
	}
	snap.Payload = json.RawMessage(payload)
	if t, err := time.Parse(time.RFC3339Nano, created); err == nil {
		snap.CreatedAt = t
	}
	return &snap, nil
}

// PruneDecisionSnapshots deletes snapshots older than retentionDays.
func (s *DB) PruneDecisionSnapshots(retentionDays int) (int, error) {
	if retentionDays <= 0 {
		return 0, nil
	}
	before := time.Now().UTC().Add(-time.Duration(retentionDays) * 24 * time.Hour).Format(time.RFC3339Nano)
	res, err := s.Exec(`DELETE FROM decision_snapshots WHERE created_at < ?`, before)
	if err != nil {
		return 0, fmt.Errorf("decision snapshot prune: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

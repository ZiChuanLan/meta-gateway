package store

import (
	"encoding/json"
	"fmt"
	"time"
)

// DecisionSnapshot is one routing decision persisted for audit: the full
// explanation (candidates, scores, reasons, sticky/stable-first state) plus
// the serving channel that was selected. One row per failover attempt;
// Attempt matches the proxy_logs row it produced (0 for legacy rows).
type DecisionSnapshot struct {
	ID                int64           `json:"id"`
	RequestID         string          `json:"request_id"`
	Model             string          `json:"model"`
	RouteID           int64           `json:"route_id"`
	SelectedChannelID int64           `json:"selected_channel_id"`
	Attempt           int             `json:"attempt"`
	Payload           json.RawMessage `json:"payload"`
	CreatedAt         time.Time       `json:"created_at"`
}

// InsertDecisionSnapshot stores one routing decision. payload must be the
// serialized routing explanation; it is stored verbatim.
func (s *DB) InsertDecisionSnapshot(requestID, model string, routeID, selectedChannelID int64, attempt int, payload []byte, at time.Time) error {
	if requestID == "" {
		return nil
	}
	_, err := s.Exec(
		`INSERT INTO decision_snapshots (request_id, model, route_id, selected_channel_id, attempt, payload, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		requestID, model, routeID, selectedChannelID, attempt, string(payload), at.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("decision snapshot insert: %w", err)
	}
	return nil
}

const snapshotColumns = `id, request_id, model, route_id, selected_channel_id, attempt, payload, created_at`

func scanSnapshot(row interface{ Scan(...any) error }) (*DecisionSnapshot, error) {
	var snap DecisionSnapshot
	var payload, created string
	if err := row.Scan(&snap.ID, &snap.RequestID, &snap.Model, &snap.RouteID, &snap.SelectedChannelID, &snap.Attempt, &payload, &created); err != nil {
		return nil, err
	}
	snap.Payload = json.RawMessage(payload)
	if t, err := time.Parse(time.RFC3339Nano, created); err == nil {
		snap.CreatedAt = t
	}
	return &snap, nil
}

// LatestDecisionSnapshot returns the most recent snapshot for a request id.
func (s *DB) LatestDecisionSnapshot(requestID string) (*DecisionSnapshot, error) {
	if requestID == "" {
		return nil, nil
	}
	snap, err := scanSnapshot(s.QueryRow(
		`SELECT `+snapshotColumns+` FROM decision_snapshots WHERE request_id = ? ORDER BY id DESC LIMIT 1`,
		requestID,
	))
	if err != nil {
		return nil, nil // no snapshot for this request
	}
	return snap, nil
}

// DecisionSnapshotForAttempt returns the snapshot written for one attempt of
// a request. Legacy rows (attempt 0) never match so callers can fall back to
// LatestDecisionSnapshot.
func (s *DB) DecisionSnapshotForAttempt(requestID string, attempt int) (*DecisionSnapshot, error) {
	if requestID == "" || attempt <= 0 {
		return nil, nil
	}
	snap, err := scanSnapshot(s.QueryRow(
		`SELECT `+snapshotColumns+` FROM decision_snapshots WHERE request_id = ? AND attempt = ? ORDER BY id DESC LIMIT 1`,
		requestID, attempt,
	))
	if err != nil {
		return nil, nil
	}
	return snap, nil
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

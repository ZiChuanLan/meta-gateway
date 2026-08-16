package store

import (
	"database/sql"
	"fmt"
	"time"
)

// HealthPoint is one probe record (success/failure + latency + verdict).
type HealthPoint struct {
	ID        int64  `json:"id"`
	ChannelID int64  `json:"channel_id"`
	OK        bool   `json:"ok"`
	LatencyMs int    `json:"latency_ms"`
	Verdict   string `json:"verdict"`
	ProbedAt  string `json:"probed_at"`
	// Kind is the probe layer: ping (network) / probe (business) / account.
	Kind string `json:"kind,omitempty"`
}

// ChannelHealthSummary is the per-channel availability aggregate over a
// window (last 24h by default). All probe kinds are aggregated together so
// the availability curve and the health badge share one data source.
type ChannelHealthSummary struct {
	ChannelID    int64   `json:"channel_id"`
	Total        int     `json:"total"`
	OK           int     `json:"ok"`
	Availability float64 `json:"availability"` // 0..1, 1 when no probes
}

// HealthHistoryStore owns the channel_health_history table.
type HealthHistoryStore struct {
	db *sql.DB
}

// Append records one probe outcome. Kind is one of the domain.ProbeKind*
// values and identifies the probe layer that produced the record.
func (s *HealthHistoryStore) Append(channelID int64, kind string, ok bool, latencyMs int, verdict string, at time.Time) error {
	_, err := s.db.Exec(
		`INSERT INTO channel_health_history (channel_id, kind, ok, latency_ms, verdict, probed_at) VALUES (?, ?, ?, ?, ?, ?)`,
		channelID, kind, boolInt(ok), latencyMs, verdict, at.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("health history append: %w", err)
	}
	return nil
}

// Recent returns the last n probe points for a channel (or all channels when
// channelID is 0), newest first.
func (s *HealthHistoryStore) Recent(channelID int64, limit int) ([]HealthPoint, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Query(
		`SELECT id, channel_id, ok, latency_ms, verdict, probed_at, kind FROM channel_health_history WHERE (? = 0 OR channel_id = ?) ORDER BY id DESC LIMIT ?`,
		channelID, channelID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("health history recent: %w", err)
	}
	defer rows.Close()
	out := []HealthPoint{}
	for rows.Next() {
		var p HealthPoint
		var ok int
		if err := rows.Scan(&p.ID, &p.ChannelID, &ok, &p.LatencyMs, &p.Verdict, &p.ProbedAt, &p.Kind); err != nil {
			return nil, fmt.Errorf("health history scan: %w", err)
		}
		p.OK = ok == 1
		out = append(out, p)
	}
	return out, rows.Err()
}

// Summaries computes per-channel availability over the trailing window
// (default 24h). Channels without any probe in the window are omitted; the
// UI can treat them as "no data" rather than 100%.
func (s *HealthHistoryStore) Summaries(since time.Time) ([]ChannelHealthSummary, error) {
	rows, err := s.db.Query(
		`SELECT channel_id, COUNT(*), SUM(ok) FROM channel_health_history WHERE probed_at >= ? GROUP BY channel_id ORDER BY channel_id`,
		since.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, fmt.Errorf("health history summaries: %w", err)
	}
	defer rows.Close()
	out := []ChannelHealthSummary{}
	for rows.Next() {
		var sum ChannelHealthSummary
		var okSum sql.NullInt64
		if err := rows.Scan(&sum.ChannelID, &sum.Total, &okSum); err != nil {
			return nil, fmt.Errorf("health summary scan: %w", err)
		}
		sum.OK = int(okSum.Int64)
		if sum.Total > 0 {
			sum.Availability = float64(sum.OK) / float64(sum.Total)
		} else {
			sum.Availability = 1
		}
		out = append(out, sum)
	}
	return out, rows.Err()
}

// Prune removes history older than the retention window; returns rows deleted.
func (s *HealthHistoryStore) Prune(olderThan time.Time) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM channel_health_history WHERE probed_at < ?`, olderThan.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, fmt.Errorf("health history prune: %w", err)
	}
	return res.RowsAffected()
}
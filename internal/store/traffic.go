package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// TrafficStat is what our own relay observed for one (channel, model) pair over
// a window: how many attempts were logged and how many of them failed.
//
// This is the zero-cost availability signal. A site's own status page is the
// exception (most sites publish none), while every request we relay is already
// written to proxy_logs — so the gateway can answer "is this channel usable for
// this model" without sending a single probe request.
type TrafficStat struct {
	ChannelID int64   `json:"channel_id"`
	Model     string  `json:"model"`
	Samples   int     `json:"samples"`
	Failures  int     `json:"failures"`
	Ratio     float64 `json:"ratio"`
	// AvgFirstByteMS averages first_byte_ms over the attempts that reported one
	// (0 when none did).
	AvgFirstByteMS int `json:"avg_first_byte_ms"`
}

// TrafficKey identifies one entry of a TrafficAvailability map.
func TrafficKey(channelID int64, model string) string {
	return fmt.Sprintf("%d|%s", channelID, strings.ToLower(strings.TrimSpace(model)))
}

// TrafficAvailability aggregates proxy_logs per (channel, model) since the given
// time.
//
// The failure predicate is the one the console's failure-rate stat already uses
// (status >= 400 OR a non-empty error_brief): inventing a second definition here
// would make two screens disagree about the same requests.
func (db *DB) TrafficAvailability(since time.Time) (map[string]TrafficStat, error) {
	rows, err := db.Query(`SELECT channel_id, model,
			COUNT(*) AS samples,
			COALESCE(SUM(CASE WHEN status >= 400 OR error_brief <> '' THEN 1 ELSE 0 END), 0) AS failures,
			COALESCE(AVG(CASE WHEN first_byte_ms > 0 THEN first_byte_ms END), 0) AS avg_first_byte
		FROM proxy_logs
		WHERE created_at >= ? AND model <> '' AND channel_id > 0
		GROUP BY channel_id, model`, since.UTC().Format("2006-01-02 15:04:05"))
	if err != nil {
		return nil, fmt.Errorf("traffic availability: %w", err)
	}
	defer rows.Close()
	out := map[string]TrafficStat{}
	for rows.Next() {
		var stat TrafficStat
		var firstByte sql.NullFloat64
		if err := rows.Scan(&stat.ChannelID, &stat.Model, &stat.Samples, &stat.Failures, &firstByte); err != nil {
			return nil, fmt.Errorf("traffic availability scan: %w", err)
		}
		if stat.Samples > 0 {
			stat.Ratio = float64(stat.Samples-stat.Failures) / float64(stat.Samples)
		}
		if firstByte.Valid {
			stat.AvgFirstByteMS = int(firstByte.Float64)
		}
		out[TrafficKey(stat.ChannelID, stat.Model)] = stat
	}
	return out, rows.Err()
}

package store

import (
	"database/sql"
	"fmt"
	"strings"
)

// SearchHits is the grouped global-search result: matching channels, routes
// (model patterns), credentials, and recent proxy logs. Each slice is capped
// (10) so the dropdown stays snappy.
type SearchHits struct {
	Channels    []SearchChannelHit `json:"channels"`
	Routes      []SearchRouteHit   `json:"routes"`
	Credentials []SearchCredHit    `json:"credentials"`
	Logs        []SearchLogHit     `json:"logs"`
}

type SearchChannelHit struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

type SearchRouteHit struct {
	ID     int64  `json:"id"`
	Model  string `json:"model"`
	Status string `json:"status"`
}

type SearchCredHit struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	SiteID int64  `json:"site_id"`
}

type SearchLogHit struct {
	ID          int64  `json:"id"`
	RequestID   string `json:"request_id"`
	Model       string `json:"model"`
	ChannelID   int64  `json:"channel_id"`
	Status      int    `json:"status"`
	CreatedAt   string `json:"created_at"`
	UpstreamID  string `json:"upstream_request_id"`
	KeyFP       string `json:"key_fingerprint"`
}

// Search runs the global admin search across channels, routes, credentials
// and proxy logs. The term is matched as a case-insensitive substring; empty
// terms return empty groups (never an error).
func (db *DB) Search(term string, limit int) (*SearchHits, error) {
	hits := &SearchHits{}
	if limit <= 0 || limit > 20 {
		limit = 10
	}
	term = strings.TrimSpace(term)
	if term == "" {
		return hits, nil
	}
	like := "%" + term + "%"

	// Channels: name, base_url, group.
	rows, err := db.Query(`
		SELECT id, name, base_url FROM channels
		WHERE name LIKE ? OR base_url LIKE ? OR group_name LIKE ?
		ORDER BY id LIMIT ?`, like, like, like, limit)
	if err != nil {
		return nil, fmt.Errorf("search channels: %w", err)
	}
	for rows.Next() {
		var hit SearchChannelHit
		if err := rows.Scan(&hit.ID, &hit.Name, &hit.URL); err != nil {
			rows.Close()
			return nil, err
		}
		hits.Channels = append(hits.Channels, hit)
	}
	rows.Close()

	// Routes: model_pattern.
	rows, err = db.Query(`
		SELECT id, model_pattern, enabled FROM routes
		WHERE model_pattern LIKE ?
		ORDER BY id LIMIT ?`, like, limit)
	if err != nil {
		return nil, fmt.Errorf("search routes: %w", err)
	}
	for rows.Next() {
		var hit SearchRouteHit
		var enabled int
		if err := rows.Scan(&hit.ID, &hit.Model, &enabled); err != nil {
			rows.Close()
			return nil, err
		}
		if enabled == 1 {
			hit.Status = "enabled"
		} else {
			hit.Status = "disabled"
		}
		hits.Routes = append(hits.Routes, hit)
	}
	rows.Close()

	// Downstream keys: name.
	rows, err = db.Query(`
		SELECT id, name, enabled FROM downstream_keys
		WHERE name LIKE ?
		ORDER BY id LIMIT ?`, like, limit)
	if err != nil {
		return nil, fmt.Errorf("search keys: %w", err)
	}
	for rows.Next() {
		var hit SearchCredHit
		var enabled int
		if err := rows.Scan(&hit.ID, &hit.Name, &enabled); err != nil {
			rows.Close()
			return nil, err
		}
		hit.Kind = "downstream"
		hits.Credentials = append(hits.Credentials, hit)
	}
	rows.Close()

	// Proxy logs: model, request_id, upstream_request_id, key_fingerprint.
	rows, err = db.Query(`
		SELECT id, request_id, model, channel_id, status, created_at, upstream_request_id, key_fingerprint
		FROM proxy_logs
		WHERE model LIKE ? OR request_id LIKE ? OR upstream_request_id LIKE ? OR key_fingerprint LIKE ?
		ORDER BY id DESC LIMIT ?`, like, like, like, like, limit)
	if err != nil {
		return nil, fmt.Errorf("search logs: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var hit SearchLogHit
		var channelID sql.NullInt64
		var status sql.NullInt64
		var upstream sql.NullString
		var keyFP sql.NullString
		if err := rows.Scan(&hit.ID, &hit.RequestID, &hit.Model, &channelID, &status, &hit.CreatedAt, &upstream, &keyFP); err != nil {
			return nil, err
		}
		if channelID.Valid {
			hit.ChannelID = channelID.Int64
		}
		if status.Valid {
			hit.Status = int(status.Int64)
		}
		if upstream.Valid {
			hit.UpstreamID = upstream.String
		}
		if keyFP.Valid {
			hit.KeyFP = keyFP.String
		}
		hits.Logs = append(hits.Logs, hit)
	}
	return hits, nil
}

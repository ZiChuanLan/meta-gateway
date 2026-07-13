package store

import (
	"database/sql"
	"fmt"

	"github.com/lan/meta-gateway/internal/domain"
)

// ProxyLogStore provides insert and query operations for proxy logs.
type ProxyLogStore struct {
	db *sql.DB
}

// Insert writes a proxy log entry.
func (s *ProxyLogStore) Insert(log *domain.ProxyLog) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO proxy_logs (request_id, channel_id, model, status, latency_ms, error_brief) VALUES (?, ?, ?, ?, ?, ?)`,
		log.RequestID, log.ChannelID, log.Model, log.Status, log.LatencyMs, log.ErrorBrief)
	if err != nil {
		return 0, fmt.Errorf("proxylog insert: %w", err)
	}
	return res.LastInsertId()
}

// List returns proxy logs ordered by creation time descending.
func (s *ProxyLogStore) List(limit int) ([]domain.ProxyLog, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, err := s.db.Query(`SELECT id, request_id, channel_id, model, status, latency_ms, error_brief, created_at FROM proxy_logs ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("proxylog list: %w", err)
	}
	defer rows.Close()

	var result []domain.ProxyLog
	for rows.Next() {
		var r domain.ProxyLog
		if err := rows.Scan(&r.ID, &r.RequestID, &r.ChannelID, &r.Model, &r.Status, &r.LatencyMs, &r.ErrorBrief, scanTime(&r.CreatedAt)); err != nil {
			return nil, fmt.Errorf("proxylog scan: %w", err)
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

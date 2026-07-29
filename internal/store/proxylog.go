package store

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/lan/meta-gateway/internal/domain"
)

// ProxyLogStore provides insert and query operations for proxy logs.
type ProxyLogStore struct {
	db *sql.DB
}

// ProxyLogFilter selects a page of proxy logs for Admin list views.
// Zero-value fields are ignored. SiteID matches via channels.site_id.
// Status, when set, matches exactly. FailedOnly selects status >= 400
// and is ignored when Status is set.
type ProxyLogFilter struct {
	SiteID     *int64
	ChannelID  *int64
	Model      string
	Status     *int
	FailedOnly bool
	BeforeID   *int64
	Limit      int
}

// Insert writes a proxy log entry.
func (s *ProxyLogStore) Insert(log *domain.ProxyLog) (int64, error) {
	stream := 0
	if log.Stream {
		stream = 1
	}
	res, err := s.db.Exec(`INSERT INTO proxy_logs (request_id, channel_id, model, status, latency_ms, attempt, error_brief, downstream_key_id, prompt_tokens, completion_tokens, total_tokens, stream, path) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		log.RequestID, log.ChannelID, log.Model, log.Status, log.LatencyMs, log.Attempt, log.ErrorBrief,
		log.DownstreamKeyID, log.PromptTokens, log.CompletionTokens, log.TotalTokens, stream, log.Path)
	if err != nil {
		return 0, fmt.Errorf("proxylog insert: %w", err)
	}
	return res.LastInsertId()
}

// List returns the newest proxy logs up to limit (legacy unfiltered path).
func (s *ProxyLogStore) List(limit int) ([]domain.ProxyLog, error) {
	return s.ListFilter(ProxyLogFilter{Limit: limit})
}

// ListFilter returns proxy logs ordered by id descending with optional filters.
func (s *ProxyLogStore) ListFilter(f ProxyLogFilter) ([]domain.ProxyLog, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}

	where := []string{"1=1"}
	args := []any{}
	needsChannelJoin := f.SiteID != nil

	if f.SiteID != nil {
		where = append(where, "c.site_id = ?")
		args = append(args, *f.SiteID)
	}
	if f.ChannelID != nil {
		where = append(where, "pl.channel_id = ?")
		args = append(args, *f.ChannelID)
	}
	if model := strings.TrimSpace(f.Model); model != "" {
		where = append(where, "pl.model = ?")
		args = append(args, model)
	}
	if f.Status != nil {
		where = append(where, "pl.status = ?")
		args = append(args, *f.Status)
	} else if f.FailedOnly {
		where = append(where, "pl.status >= 400")
	}
	if f.BeforeID != nil {
		where = append(where, "pl.id < ?")
		args = append(args, *f.BeforeID)
	}
	args = append(args, limit)

	from := "proxy_logs pl"
	if needsChannelJoin {
		// LEFT JOIN so orphan channel_id rows remain visible when not site-filtered;
		// site filter still requires a matching channels.site_id row.
		from = "proxy_logs pl INNER JOIN channels c ON c.id = pl.channel_id"
	}

	query := `SELECT pl.id, pl.request_id, pl.channel_id, pl.model, pl.status, pl.latency_ms, pl.attempt, pl.error_brief,
		pl.downstream_key_id, pl.prompt_tokens, pl.completion_tokens, pl.total_tokens, pl.stream, pl.path, pl.created_at
FROM ` + from + `
WHERE ` + strings.Join(where, " AND ") + `
ORDER BY pl.id DESC
LIMIT ?`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("proxylog list: %w", err)
	}
	defer rows.Close()

	var result []domain.ProxyLog
	for rows.Next() {
		var r domain.ProxyLog
		var stream int
		if err := rows.Scan(
			&r.ID, &r.RequestID, &r.ChannelID, &r.Model, &r.Status, &r.LatencyMs, &r.Attempt, &r.ErrorBrief,
			&r.DownstreamKeyID, &r.PromptTokens, &r.CompletionTokens, &r.TotalTokens, &stream, &r.Path, scanTime(&r.CreatedAt),
		); err != nil {
			return nil, fmt.Errorf("proxylog scan: %w", err)
		}
		r.Stream = stream != 0
		result = append(result, r)
	}
	return result, rows.Err()
}

// UpdateTokensByRequestID attaches metered tokens to the newest log row for a request.
func (s *ProxyLogStore) UpdateTokensByRequestID(requestID string, promptTokens, completionTokens, totalTokens int) error {
	if strings.TrimSpace(requestID) == "" || totalTokens <= 0 {
		return nil
	}
	_, err := s.db.Exec(
		`UPDATE proxy_logs
		 SET prompt_tokens = ?, completion_tokens = ?, total_tokens = ?
		 WHERE id = (
		   SELECT id FROM proxy_logs WHERE request_id = ? ORDER BY id DESC LIMIT 1
		 )`,
		promptTokens, completionTokens, totalTokens, requestID,
	)
	if err != nil {
		return fmt.Errorf("proxylog update tokens: %w", err)
	}
	return nil
}

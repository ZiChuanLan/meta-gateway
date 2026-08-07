package store

import (
	"database/sql"
	"fmt"
	"sort"
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
	res, err := s.db.Exec(`INSERT INTO proxy_logs (request_id, channel_id, route_id, model, status, latency_ms, attempt, error_brief, downstream_key_id, prompt_tokens, completion_tokens, total_tokens, cache_read_tokens, cache_creation_tokens, stream, path, session_key, reasoning_effort) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		log.RequestID, log.ChannelID, log.RouteID, log.Model, log.Status, log.LatencyMs, log.Attempt, log.ErrorBrief,
		log.DownstreamKeyID, log.PromptTokens, log.CompletionTokens, log.TotalTokens, log.CacheReadTokens, log.CacheCreationTokens, stream, log.Path, log.SessionKey, log.ReasoningEffort)
	if err != nil {
		return 0, fmt.Errorf("proxylog insert: %w", err)
	}
	return res.LastInsertId()
}

// UpdateMetaByRequestID attaches the first-byte latency and client family to
// the newest log row for a request (best-effort observability enrichment).
func (s *ProxyLogStore) UpdateMetaByRequestID(requestID string, firstByteMs int, clientFamily string) error {
	if strings.TrimSpace(requestID) == "" {
		return nil
	}
	_, err := s.db.Exec(
		`UPDATE proxy_logs
		 SET first_byte_ms = ?, client_family = ?
		 WHERE id = (
		   SELECT id FROM proxy_logs WHERE request_id = ? ORDER BY id DESC LIMIT 1
		 )`,
		firstByteMs, clientFamily, requestID,
	)
	if err != nil {
		return fmt.Errorf("proxylog update meta: %w", err)
	}
	return nil
}

// List returns the newest proxy logs up to limit (legacy unfiltered path).
func (s *ProxyLogStore) List(limit int) ([]domain.ProxyLog, error) {
	return s.ListFilter(ProxyLogFilter{Limit: limit})
}

// LatencyBucketBounds are the histogram upper bounds in milliseconds (AAH
// USAGE_HISTORY_LATENCY_BUCKET_UPPER_BOUNDS_SECONDS, x1000). The final bucket
// is open-ended (>= 34s).
var LatencyBucketBounds = []int{250, 500, 1000, 2000, 3000, 5000, 8000, 13000, 21000, 34000}

// LatencyHistogram is the aggregated latency distribution over the newest
// sampleSize log rows (slow = >= 5s, AAH USAGE_HISTORY_SLOW_THRESHOLD_SECONDS).
type LatencyHistogram struct {
	// Buckets[i] counts rows with latency in [bounds[i-1], bounds[i]) (first
	// bucket < 250ms, last bucket >= 34s).
	Buckets   []int   `json:"buckets"`
	Total     int     `json:"total"`
	SlowCount int     `json:"slow_count"`
	P50Ms     int     `json:"p50_ms"`
	P95Ms     int     `json:"p95_ms"`
}

// LatencyHistogram aggregates the newest sampleSize proxy log latencies.
func (s *ProxyLogStore) LatencyHistogram(sampleSize int) (*LatencyHistogram, error) {
	if sampleSize <= 0 {
		sampleSize = 1000
	}
	if sampleSize > 10000 {
		sampleSize = 10000
	}
	rows, err := s.db.Query(`SELECT latency_ms FROM proxy_logs ORDER BY id DESC LIMIT ?`, sampleSize)
	if err != nil {
		return nil, fmt.Errorf("proxylog histogram: %w", err)
	}
	defer rows.Close()

	hist := &LatencyHistogram{Buckets: make([]int, len(LatencyBucketBounds)+1)}
	latencies := make([]int, 0, sampleSize)
	for rows.Next() {
		var latency int
		if err := rows.Scan(&latency); err != nil {
			return nil, fmt.Errorf("proxylog histogram scan: %w", err)
		}
		hist.Total++
		if latency >= 5000 {
			hist.SlowCount++
		}
		hist.Buckets[bucketIndex(latency)]++
		latencies = append(latencies, latency)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("proxylog histogram rows: %w", err)
	}
	sort.Ints(latencies)
	hist.P50Ms = percentile(latencies, 50)
	hist.P95Ms = percentile(latencies, 95)
	return hist, nil
}

func bucketIndex(latencyMs int) int {
	for i, bound := range LatencyBucketBounds {
		if latencyMs < bound {
			return i
		}
	}
	return len(LatencyBucketBounds)
}

func percentile(sorted []int, p int) int {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	idx := (n * p) / 100
	if idx >= n {
		idx = n - 1
	}
	return sorted[idx]
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
	// Always LEFT JOIN routes so the route pattern (model_pattern) can be shown
	// alongside each log row; route_id 0 / missing routes render as empty.
	from += " LEFT JOIN routes rt ON rt.id = pl.route_id"

	query := `SELECT pl.id, pl.request_id, pl.channel_id, pl.route_id, COALESCE(rt.model_pattern, ''), pl.model, pl.status, pl.latency_ms, pl.attempt, pl.error_brief,
		pl.downstream_key_id, pl.prompt_tokens, pl.completion_tokens, pl.total_tokens,
		pl.cache_read_tokens, pl.cache_creation_tokens, pl.first_byte_ms, pl.client_family, pl.reasoning_effort, pl.tokens_per_second, pl.stream, pl.path, pl.session_key, pl.created_at
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
			&r.ID, &r.RequestID, &r.ChannelID, &r.RouteID, &r.RoutePattern, &r.Model, &r.Status, &r.LatencyMs, &r.Attempt, &r.ErrorBrief,
			&r.DownstreamKeyID, &r.PromptTokens, &r.CompletionTokens, &r.TotalTokens,
			&r.CacheReadTokens, &r.CacheCreationTokens, &r.FirstByteMs, &r.ClientFamily, &r.ReasoningEffort, &r.TokensPerSecond, &stream, &r.Path, &r.SessionKey, scanTime(&r.CreatedAt),
		); err != nil {
			return nil, fmt.Errorf("proxylog scan: %w", err)
		}
		r.Stream = stream != 0
		result = append(result, r)
	}
	return result, rows.Err()
}

// UpdateTokensByRequestID attaches metered tokens to the newest log row for a
// request. TokensPerSecond is derived in SQL from completion tokens over the
// effective latency (total minus first-byte), mirroring AxonHub's TPS metric
// with a 10ms floor.
func (s *ProxyLogStore) UpdateTokensByRequestID(requestID string, promptTokens, completionTokens, totalTokens, cacheReadTokens, cacheCreationTokens int) error {
	if strings.TrimSpace(requestID) == "" || totalTokens <= 0 {
		return nil
	}
	_, err := s.db.Exec(
		`UPDATE proxy_logs
		 SET prompt_tokens = ?, completion_tokens = ?, total_tokens = ?,
		     cache_read_tokens = ?, cache_creation_tokens = ?,
		     tokens_per_second = CASE
		       WHEN ? > 0 AND latency_ms > 0
		         THEN ? / MAX((latency_ms - COALESCE(first_byte_ms, 0)) / 1000.0, 0.01)
		       ELSE 0 END
		 WHERE id = (
		   SELECT id FROM proxy_logs WHERE request_id = ? ORDER BY id DESC LIMIT 1
		 )`,
		promptTokens, completionTokens, totalTokens, cacheReadTokens, cacheCreationTokens,
		completionTokens, float64(completionTokens), requestID,
	)
	if err != nil {
		return fmt.Errorf("proxylog update tokens: %w", err)
	}
	return nil
}

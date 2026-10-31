package store

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
)

// ProxyLogStore provides insert and query operations for proxy logs.
type ProxyLogStore struct {
	db  *sql.DB
	fts LogFTSState
}

// ProxyLogFilter selects a page of proxy logs for Admin list views.
// Zero-value fields are ignored. SiteID matches via channels.site_id.
// Status, when set, matches exactly. FailedOnly selects status >= 400
// and is ignored when Status is set.
type ProxyLogFilter struct {
	SiteID    *int64
	ChannelID *int64
	// DownstreamKeyID selects one client token. Rows written before the
	// column existed (or rejected before authentication) carry 0 and are
	// never matched by a key filter.
	DownstreamKeyID   *int64
	Model             string
	Status            *int
	FailedOnly        bool
	UpstreamRequestID string
	BeforeID          *int64
	Limit             int
	// Since/Until bound created_at inclusively (nil = open-ended). The console
	// sends RFC3339; SQLiteStoreTime normalizes it to the stored UTC layout.
	Since *time.Time
	Until *time.Time
	// Query is free text searched across model, error text, path and both
	// request ids. It runs through FTS5 when available and degrades to LIKE
	// when the SQLite build has no FTS5 (or the query has no usable tokens).
	Query string
}

// sqliteUTC renders a time in the exact layout proxy_logs/usage_records store
// (SQLite `datetime('now')` = UTC "YYYY-MM-DD HH:MM:SS"). Binding an RFC3339
// string would compare greater than every row and silently return nothing.
func sqliteUTC(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04:05")
}

// createdRange narrows a query to an inclusive created_at window. Column is
// the qualified column name (e.g. "pl.created_at").
func createdRange(column string, since, until *time.Time) ([]string, []any) {
	clauses := []string{}
	args := []any{}
	if since != nil {
		clauses = append(clauses, column+" >= ?")
		args = append(args, sqliteUTC(*since))
	}
	if until != nil {
		clauses = append(clauses, column+" <= ?")
		args = append(args, sqliteUTC(*until))
	}
	return clauses, args
}

// Insert writes a proxy log entry.
func (s *ProxyLogStore) Insert(log *domain.ProxyLog) (int64, error) {
	stream := 0
	if log.Stream {
		stream = 1
	}
	res, err := s.db.Exec(`INSERT INTO proxy_logs (request_id, channel_id, route_id, model, status, latency_ms, attempt, key_retries, error_brief, error_detail, downstream_key_id, prompt_tokens, completion_tokens, total_tokens, cache_read_tokens, cache_creation_tokens, stream, path, session_key, reasoning_effort, key_fingerprint, upstream_request_id, mapped_reasoning_effort, upstream_model, upstream_url, upstream_key_id, member_id, request_kind, completion_outcome) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		log.RequestID, log.ChannelID, log.RouteID, log.Model, log.Status, log.LatencyMs, log.Attempt, log.KeyRetries, log.ErrorBrief, log.ErrorDetail,
		log.DownstreamKeyID, log.PromptTokens, log.CompletionTokens, log.TotalTokens, log.CacheReadTokens, log.CacheCreationTokens, stream, log.Path, log.SessionKey, log.ReasoningEffort, log.KeyFingerprint, log.UpstreamRequestID, log.MappedReasoningEffort, log.UpstreamModel, log.UpstreamURL, log.UpstreamKeyID, log.MemberID, requestKind(log.RequestKind), func() string {
			if log.CompletionOutcome != "" {
				return log.CompletionOutcome
			}
			if log.Stream {
				return "pending"
			}
			return ""
		}())
	if err != nil {
		return 0, fmt.Errorf("proxylog insert: %w", err)
	}
	return res.LastInsertId()
}

// UpdateMetaByRequestID attaches the first-byte latency and the client
// identity (coarse family + the raw User-Agent it came from) to the newest log
// row for a request (best-effort observability enrichment).
func (s *ProxyLogStore) UpdateMetaByRequestID(requestID string, firstByteMs int, clientFamily, clientUA string) error {
	if strings.TrimSpace(requestID) == "" {
		return nil
	}
	_, err := s.db.Exec(
		`UPDATE proxy_logs
		 SET first_byte_ms = ?, client_family = ?, client_ua = ?
		 WHERE id = (
		   SELECT id FROM proxy_logs WHERE request_id = ? ORDER BY id DESC LIMIT 1
		 )`,
		firstByteMs, clientFamily, clientUA, requestID,
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

// MarkSuperseded folds every earlier attempt of this request into keepID.
//
// One request produces one upstream attempt row per channel (and per key
// re-send), which is exactly what the audit, health and billing paths need. What
// it should not produce is a console list where a single failed-over request
// shows up three times: the operator then has to reconstruct the walk by
// matching request ids by eye. So each new attempt points its predecessors at
// itself, and list queries read only the rows nothing points at.
//
// The earlier rows are not deleted: they are the chain (see attachAttempts) and
// the only record of what each channel answered.
func (s *ProxyLogStore) MarkSuperseded(requestID string, keepID int64) error {
	if strings.TrimSpace(requestID) == "" || keepID <= 0 {
		return nil
	}
	_, err := s.db.Exec(
		`UPDATE proxy_logs SET merged_into = ?
		 WHERE request_id = ? AND id <> ? AND merged_into IS NULL`,
		keepID, requestID, keepID,
	)
	if err != nil {
		return fmt.Errorf("proxylog mark superseded: %w", err)
	}
	return nil
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
	Buckets   []int `json:"buckets"`
	Total     int   `json:"total"`
	SlowCount int   `json:"slow_count"`
	P50Ms     int   `json:"p50_ms"`
	P95Ms     int   `json:"p95_ms"`
	P99Ms     int   `json:"p99_ms"`
	// Matched is how many rows the window held before the newest-first sample
	// cut it down to Total. The console shows both so a sample that is smaller
	// than the window cannot be mistaken for the whole window.
	Matched int `json:"matched"`
	// SampleSize is the newest-first sample the distribution covers, so the
	// caption can distinguish "all of the window" from "the tail of it".
	SampleSize int `json:"sample_size"`
}

// FailRate returns (total, failed) relay requests since the given time.
// Failed = status >= 400 or a non-empty error_brief. The since bound is
// formatted to match proxy_logs.created_at (datetime('now') = SQLite UTC
// "YYYY-MM-DD HH:MM:SS"); an RFC3339 bound would string-compare greater
// than every row and silently return zeros.
func (s *ProxyLogStore) FailRate(since time.Time) (total, failed int) {
	// Count requests, not attempts: the console lists one row per request, so a
	// request that failed over and then succeeded must read as one success here
	// too. Counting raw rows made the two numbers disagree.
	row := s.db.QueryRow(
		`SELECT COUNT(*), COALESCE(SUM(CASE WHEN status >= 400 OR error_brief <> '' THEN 1 ELSE 0 END), 0)
		 FROM proxy_logs WHERE created_at >= ? AND merged_into IS NULL`,
		sqliteUTC(since),
	)
	_ = row.Scan(&total, &failed)
	return total, failed
}

// LatencyHistogram aggregates the newest sampleSize proxy log latencies,
// optionally restricted to an inclusive time window. A window makes the panel
// answer "how slow was the last hour", not just "how slow is the tail".
func (s *ProxyLogStore) LatencyHistogram(sampleSize int, since, until *time.Time) (*LatencyHistogram, error) {
	return s.latencyHistogram("proxy_logs", "id", nil, sampleSize, since, until)
}

// MemberLatencyHistogram uses completed account requests rather than individual
// upstream attempts. userID is mandatory: it can never widen into a global read.
func (s *ProxyLogStore) MemberLatencyHistogram(userID int64, sampleSize int, since, until *time.Time) (*LatencyHistogram, error) {
	if userID <= 0 {
		return nil, fmt.Errorf("member histogram requires a user")
	}
	return s.latencyHistogram("team_requests", "rowid", &userID, sampleSize, since, until)
}

// Table/order are private constants supplied only by the two wrappers above.
func (s *ProxyLogStore) latencyHistogram(table, order string, userID *int64, sampleSize int, since, until *time.Time) (*LatencyHistogram, error) {
	if sampleSize <= 0 {
		sampleSize = 1000
	}
	if sampleSize > 200000 {
		sampleSize = 200000
	}
	where, args := createdRange("created_at", since, until)
	if userID != nil {
		where = append(where, "user_id = ?")
		args = append(args, *userID)
	}
	if table == "proxy_logs" {
		// One row per request, matching the log table: a request that failed
		// over and then succeeded must not also draw a failed attempt's latency
		// into the distribution, or p95 describes upstream attempts instead of
		// what the client waited for.
		where = append(where, "merged_into IS NULL")
	}
	scope := ""
	if len(where) > 0 {
		scope = " WHERE " + strings.Join(where, " AND ")
	}

	hist := &LatencyHistogram{
		Buckets:    make([]int, len(LatencyBucketBounds)+1),
		SampleSize: sampleSize,
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM `+table+scope, args...).Scan(&hist.Matched); err != nil {
		return nil, fmt.Errorf("proxylog histogram count: %w", err)
	}

	rows, err := s.db.Query(
		`SELECT latency_ms FROM `+table+scope+` ORDER BY `+order+` DESC LIMIT ?`,
		append(append([]any{}, args...), sampleSize)...,
	)
	if err != nil {
		return nil, fmt.Errorf("proxylog histogram: %w", err)
	}
	defer rows.Close()

	latencies := make([]int, 0, 256)
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
	hist.P99Ms = percentile(latencies, 99)
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

	// One request, one row: every attempt but the last is marked merged_into,
	// and the chain it belongs to is attached below as Attempts. Without this
	// clause the list shows a failed-over request once per channel, which is the
	// thing operators were reading as three separate requests.
	where := []string{"pl.merged_into IS NULL"}
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
	if f.DownstreamKeyID != nil {
		where = append(where, "pl.downstream_key_id = ?")
		args = append(args, *f.DownstreamKeyID)
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
	if clauses, rangeArgs := createdRange("pl.created_at", f.Since, f.Until); len(clauses) > 0 {
		where = append(where, clauses...)
		args = append(args, rangeArgs...)
	}
	if id := strings.TrimSpace(f.UpstreamRequestID); id != "" {
		where = append(where, "pl.upstream_request_id = ?")
		args = append(args, id)
	}

	from := "proxy_logs pl"
	if query := strings.TrimSpace(f.Query); query != "" {
		// The search runs over the request's WHOLE chain, not just the surviving
		// row: the error an operator greps for is usually the one a hop returned
		// before the request was retried somewhere else, and that hop has been
		// merged away. Matching request ids keeps the result "one row per
		// request" while still finding it by any hop's error text.
		if match := BuildLogFTSMatch(query); match != "" && s.fts.Available(s.db) {
			where = append(where, "pl.request_id IN (SELECT request_id FROM proxy_logs WHERE id IN (SELECT rowid FROM proxy_logs_fts WHERE proxy_logs_fts MATCH ?))")
			args = append(args, match)
		} else {
			// No FTS5 (or nothing tokenizable): substring scan over the same
			// columns. Slower, but the feature still works — and still spans the
			// chain, for the same reason as above.
			like := "%" + strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(query) + "%"
			where = append(where, `(pl.request_id IN (SELECT request_id FROM proxy_logs WHERE model LIKE ? ESCAPE '\' OR error_brief LIKE ? ESCAPE '\' OR error_detail LIKE ? ESCAPE '\' OR path LIKE ? ESCAPE '\' OR request_id LIKE ? ESCAPE '\' OR upstream_request_id LIKE ? ESCAPE '\' OR upstream_url LIKE ? ESCAPE '\' OR client_ua LIKE ? ESCAPE '\'))`)
			args = append(args, like, like, like, like, like, like, like, like)
		}
	}
	args = append(args, limit)

	if needsChannelJoin {
		// A site filter requires a matching channel, while unfiltered logs
		// still include orphan channel IDs. Preserve any FTS join above.
		from += " INNER JOIN channels c ON c.id = pl.channel_id"
	}
	// Always LEFT JOIN routes so the route pattern (model_pattern) can be shown
	// alongside each log row; route_id 0 / missing routes render as empty.
	from += " LEFT JOIN routes rt ON rt.id = pl.route_id"
	// Same for the upstream key: the id is stored, the display name lives in the
	// credential's meta_json. Resolving it at read time keeps renames visible;
	// a deleted key (or a row from before the column existed) resolves empty and
	// the console falls back to the fingerprint.
	from += " LEFT JOIN credentials kc ON kc.id = pl.upstream_key_id"

	query := `SELECT pl.id, pl.request_id, pl.channel_id, pl.route_id, COALESCE(rt.model_pattern, ''), pl.model, pl.status, pl.latency_ms, pl.attempt, pl.key_retries, pl.error_brief, pl.error_detail,
		pl.downstream_key_id, pl.prompt_tokens, pl.completion_tokens, pl.total_tokens,
		pl.cache_read_tokens, pl.cache_creation_tokens, pl.first_byte_ms, pl.client_family, COALESCE(pl.client_ua, ''), pl.reasoning_effort, pl.mapped_reasoning_effort, pl.tokens_per_second, pl.stream, pl.path, pl.session_key, pl.upstream_request_id, COALESCE(pl.upstream_model, ''), COALESCE(pl.upstream_url, ''), COALESCE(pl.key_fingerprint, ''), pl.upstream_key_id, pl.member_id, pl.request_kind, pl.completion_outcome, COALESCE(NULLIF(CASE WHEN json_valid(kc.meta_json) THEN json_extract(kc.meta_json, '$.name') END, ''), ''), pl.created_at
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
			&r.ID, &r.RequestID, &r.ChannelID, &r.RouteID, &r.RoutePattern, &r.Model, &r.Status, &r.LatencyMs, &r.Attempt, &r.KeyRetries, &r.ErrorBrief, &r.ErrorDetail,
			&r.DownstreamKeyID, &r.PromptTokens, &r.CompletionTokens, &r.TotalTokens,
			&r.CacheReadTokens, &r.CacheCreationTokens, &r.FirstByteMs, &r.ClientFamily, &r.ClientUA, &r.ReasoningEffort, &r.MappedReasoningEffort, &r.TokensPerSecond, &stream, &r.Path, &r.SessionKey, &r.UpstreamRequestID, &r.UpstreamModel, &r.UpstreamURL, &r.KeyFingerprint, &r.UpstreamKeyID, &r.MemberID, &r.RequestKind, &r.CompletionOutcome, &r.UpstreamKeyName, scanTime(&r.CreatedAt),
		); err != nil {
			return nil, fmt.Errorf("proxylog scan: %w", err)
		}
		r.Stream = stream != 0
		result = append(result, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := s.attachAttempts(result); err != nil {
		return nil, err
	}
	return result, nil
}

// attemptChainLimit bounds how many hops one request can return. The retry
// settings already bound a chain in practice; this is the backstop for a
// pathological one, because the alternative (an unbounded array per row) is how
// a single log record once grew into hundreds of megabytes in the wild.
const attemptChainLimit = 20

// attachAttempts fills in each row's failover chain from the attempt rows that
// were merged into it.
//
// Rows are read newest-first, but a chain has to read in walk order, so this is
// one extra query for the whole page (grouped by request_id, ordered by id) and
// not one per row. A request with a single attempt gets a one-hop chain, so the
// console never has to special-case "no failover happened".
func (s *ProxyLogStore) attachAttempts(rows []domain.ProxyLog) error {
	if len(rows) == 0 {
		return nil
	}
	ids := make([]string, 0, len(rows))
	seen := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if row.RequestID == "" {
			continue
		}
		if _, ok := seen[row.RequestID]; ok {
			continue
		}
		seen[row.RequestID] = struct{}{}
		ids = append(ids, row.RequestID)
	}
	if len(ids) == 0 {
		return nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	/* #nosec G202 -- placeholders are generated, ids are bound parameters. */
	query := `SELECT id, request_id, attempt, key_retries, channel_id, model,
		COALESCE(upstream_model, ''), status, latency_ms, COALESCE(error_brief, ''),
		COALESCE(error_detail, ''), COALESCE(upstream_url, ''), created_at
		FROM proxy_logs WHERE request_id IN (` + placeholders + `) ORDER BY request_id, id`
	cursor, err := s.db.Query(query, args...)
	if err != nil {
		return fmt.Errorf("proxylog attempts: %w", err)
	}
	defer cursor.Close()

	chains := make(map[string][]domain.ProxyLogAttempt, len(ids))
	for cursor.Next() {
		var requestID string
		var hop domain.ProxyLogAttempt
		if err := cursor.Scan(
			&hop.LogID, &requestID, &hop.Attempt, &hop.KeyRetries, &hop.ChannelID, &hop.Model,
			&hop.UpstreamModel, &hop.Status, &hop.LatencyMs, &hop.ErrorBrief, &hop.ErrorDetail,
			&hop.UpstreamURL, scanTime(&hop.CreatedAt),
		); err != nil {
			return fmt.Errorf("proxylog attempts scan: %w", err)
		}
		chains[requestID] = append(chains[requestID], hop)
	}
	if err := cursor.Err(); err != nil {
		return err
	}
	for i := range rows {
		chain := chains[rows[i].RequestID]
		if len(chain) > attemptChainLimit {
			// Keep both ends: where the request started and what was finally
			// returned. The middle is reconstructible from the raw rows by id.
			half := attemptChainLimit / 2
			trimmed := make([]domain.ProxyLogAttempt, 0, attemptChainLimit)
			trimmed = append(trimmed, chain[:half]...)
			trimmed = append(trimmed, chain[len(chain)-half:]...)
			chain = trimmed
			rows[i].AttemptsTruncated = true
		}
		if len(chain) > 0 {
			chain[len(chain)-1].Final = true
		}
		rows[i].Attempts = chain
	}
	return nil
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

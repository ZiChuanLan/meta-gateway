package store

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
)

// UsageStore persists metered relay usage for billing summaries.
type UsageStore struct {
	db *sql.DB
}

// Insert writes one usage record.
func (s *UsageStore) Insert(record *domain.UsageRecord) (int64, error) {
	stream := 0
	if record.Stream {
		stream = 1
	}
	res, err := s.db.Exec(
		`INSERT INTO usage_records (
			request_id, downstream_key_id, channel_id, model, path, stream,
			prompt_tokens, completion_tokens, total_tokens,
			cache_read_tokens, cache_creation_tokens, status, cost
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.RequestID,
		record.DownstreamKeyID,
		record.ChannelID,
		record.Model,
		record.Path,
		stream,
		record.PromptTokens,
		record.CompletionTokens,
		record.TotalTokens,
		record.CacheReadTokens,
		record.CacheCreationTokens,
		record.Status,
		record.Cost,
	)
	if err != nil {
		return 0, fmt.Errorf("usage insert: %w", err)
	}
	return res.LastInsertId()
}

// UsageFilter selects usage rows for Admin views.
type UsageFilter struct {
	DownstreamKeyID *int64
	ChannelID       *int64
	Model           string
	Limit           int
	// Since/Until bound created_at inclusively (nil = open-ended).
	Since *time.Time
	Until *time.Time
}

// List returns newest usage records.
func (s *UsageStore) List(filter UsageFilter) ([]domain.UsageRecord, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	where := []string{"1=1"}
	args := []any{}
	if filter.DownstreamKeyID != nil {
		where = append(where, "downstream_key_id = ?")
		args = append(args, *filter.DownstreamKeyID)
	}
	if filter.ChannelID != nil {
		where = append(where, "channel_id = ?")
		args = append(args, *filter.ChannelID)
	}
	if model := strings.TrimSpace(filter.Model); model != "" {
		where = append(where, "model = ?")
		args = append(args, model)
	}
	if clauses, rangeArgs := createdRange("created_at", filter.Since, filter.Until); len(clauses) > 0 {
		where = append(where, clauses...)
		args = append(args, rangeArgs...)
	}
	args = append(args, limit)
	query := `SELECT id, request_id, downstream_key_id, channel_id, model, path, stream,
	prompt_tokens, completion_tokens, total_tokens,
	cache_read_tokens, cache_creation_tokens, status, cost, created_at
	FROM usage_records WHERE ` + strings.Join(where, " AND ") + `
	ORDER BY id DESC LIMIT ?`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("usage list: %w", err)
	}
	defer rows.Close()
	var result []domain.UsageRecord
	for rows.Next() {
		var record domain.UsageRecord
		var stream int
		if err := rows.Scan(
			&record.ID,
			&record.RequestID,
			&record.DownstreamKeyID,
			&record.ChannelID,
			&record.Model,
			&record.Path,
			&stream,
			&record.PromptTokens,
			&record.CompletionTokens,
			&record.TotalTokens,
			&record.CacheReadTokens,
			&record.CacheCreationTokens,
			&record.Status,
			&record.Cost,
			scanTime(&record.CreatedAt),
		); err != nil {
			return nil, fmt.Errorf("usage scan: %w", err)
		}
		record.Stream = stream != 0
		result = append(result, record)
	}
	return result, rows.Err()
}

// Summary aggregates usage optionally filtered by downstream key.
func (s *UsageStore) Summary(downstreamKeyID *int64) (domain.UsageSummary, error) {
	return s.SummarySince(downstreamKeyID, nil)
}

// SummarySince aggregates usage from the optional inclusive UTC timestamp.
func (s *UsageStore) SummarySince(downstreamKeyID *int64, since *time.Time) (domain.UsageSummary, error) {
	return s.SummaryRange(downstreamKeyID, since, nil)
}

// SummaryRange aggregates usage over an inclusive [since, until] window; nil
// bounds are open-ended. The console uses it for arbitrary time selections,
// which is why the window — not just a lower bound — is expressible.
func (s *UsageStore) SummaryRange(downstreamKeyID *int64, since, until *time.Time) (domain.UsageSummary, error) {
	query := `SELECT COUNT(*),
		COALESCE(SUM(prompt_tokens),0),
		COALESCE(SUM(completion_tokens),0),
		COALESCE(SUM(total_tokens),0),
		COALESCE(SUM(cache_read_tokens),0),
		COALESCE(SUM(cache_creation_tokens),0),
		COALESCE(SUM(CASE WHEN status >= 200 AND status < 300 THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN status >= 400 AND status < 500 THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN status >= 500 THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN status < 200 OR (status >= 300 AND status < 400) THEN 1 ELSE 0 END),0),
		COALESCE(SUM(cost),0)
		FROM usage_records`
	where := []string{}
	args := []any{}
	if downstreamKeyID != nil {
		where = append(where, "downstream_key_id = ?")
		args = append(args, *downstreamKeyID)
	}
	if clauses, rangeArgs := createdRange("created_at", since, until); len(clauses) > 0 {
		where = append(where, clauses...)
		args = append(args, rangeArgs...)
	}
	if len(where) > 0 {
		query += ` WHERE ` + strings.Join(where, " AND ")
	}
	var summary domain.UsageSummary
	if err := s.db.QueryRow(query, args...).Scan(
		&summary.RequestCount,
		&summary.PromptTokens,
		&summary.CompletionTokens,
		&summary.TotalTokens,
		&summary.CacheReadTokens,
		&summary.CacheCreationTokens,
		&summary.OkCount,
		&summary.ClientErrorCount,
		&summary.ServerErrorCount,
		&summary.OtherCount,
		&summary.Cost,
	); err != nil {
		return domain.UsageSummary{}, fmt.Errorf("usage summary: %w", err)
	}
	return summary, nil
}

// seriesBucketUnits are the bucket sizes the console chart understands, in
// seconds (1m, 5m, 15m, 30m, 1h, 3h, 6h, 12h, 1d).
var seriesBucketUnits = []int{60, 300, 900, 1800, 3600, 10800, 21600, 43200, 86400}

// UsageSeries is a fixed-width time series over usage_records. Buckets are
// epoch-aligned so consecutive ranges produce adjacent, stable labels; the
// caller formats them in ITS timezone from Since + BucketSeconds.
type UsageSeries struct {
	Since            time.Time `json:"since"`
	Until            time.Time `json:"until"`
	BucketSeconds    int       `json:"bucket_seconds"`
	Requests         []int     `json:"requests"`
	Failed           []int     `json:"failed"`
	Tokens           []int64   `json:"tokens"`
	PromptTokens     []int64   `json:"prompt_tokens"`
	CompletionTokens []int64   `json:"completion_tokens"`
	CacheRead        []int64   `json:"cache_read_tokens"`
	CacheWrite       []int64   `json:"cache_creation_tokens"`
	Cost             []float64 `json:"cost"`
}

// Series aggregates usage_records into at most `buckets` epoch-aligned slots.
// Aggregation happens in SQL, so the chart reflects every row in the window
// instead of the newest 500 the list endpoint can return.
func (s *UsageStore) Series(since, until time.Time, buckets int) (*UsageSeries, error) {
	if buckets <= 0 {
		buckets = 24
	}
	if buckets > 1500 {
		buckets = 1500
	}
	if !until.After(since) {
		until = since.Add(time.Hour)
	}
	span := until.Sub(since).Seconds()
	sec := int(math.Ceil(span / float64(buckets)))
	if sec < seriesBucketUnits[0] {
		sec = seriesBucketUnits[0]
	}
	for _, unit := range seriesBucketUnits {
		if sec <= unit {
			sec = unit
			break
		}
	}
	if sec > seriesBucketUnits[len(seriesBucketUnits)-1] {
		sec = seriesBucketUnits[len(seriesBucketUnits)-1]
	}

	// Align the first bucket to the epoch grid so labels land on clean
	// wall-clock boundaries (…:00, …:15, midnight).
	startUnix := since.Unix()
	startUnix -= startUnix % int64(sec)
	start := time.Unix(startUnix, 0).UTC()
	count := int((until.Unix()-startUnix)/int64(sec)) + 1
	if count < 1 {
		count = 1
	}
	if count > 1500 {
		count = 1500
	}

	series := &UsageSeries{
		Since:            start,
		Until:            until.UTC(),
		BucketSeconds:    sec,
		Requests:         make([]int, count),
		Failed:           make([]int, count),
		Tokens:           make([]int64, count),
		PromptTokens:     make([]int64, count),
		CompletionTokens: make([]int64, count),
		CacheRead:        make([]int64, count),
		CacheWrite:       make([]int64, count),
		Cost:             make([]float64, count),
	}

	rows, err := s.db.Query(
		`SELECT CAST((CAST(strftime('%s', created_at) AS INTEGER) - ?) / ? AS INTEGER) AS idx,
			COUNT(*),
			COALESCE(SUM(CASE WHEN status >= 400 THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(total_tokens), 0),
			COALESCE(SUM(prompt_tokens), 0),
			COALESCE(SUM(completion_tokens), 0),
			COALESCE(SUM(cache_read_tokens), 0),
			COALESCE(SUM(cache_creation_tokens), 0),
			COALESCE(SUM(cost), 0)
		FROM usage_records
		WHERE created_at >= ? AND created_at <= ?
		GROUP BY idx`,
		startUnix, int64(sec),
		sqliteUTC(start), sqliteUTC(until),
	)
	if err != nil {
		return nil, fmt.Errorf("usage series: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var idx, requests, failed int
		var tokens, prompt, completion, cacheRead, cacheWrite int64
		var cost float64
		if err := rows.Scan(&idx, &requests, &failed, &tokens, &prompt, &completion, &cacheRead, &cacheWrite, &cost); err != nil {
			return nil, fmt.Errorf("usage series scan: %w", err)
		}
		if idx < 0 || idx >= count {
			continue
		}
		series.Requests[idx] += requests
		series.Failed[idx] += failed
		series.Tokens[idx] += tokens
		series.PromptTokens[idx] += prompt
		series.CompletionTokens[idx] += completion
		series.CacheRead[idx] += cacheRead
		series.CacheWrite[idx] += cacheWrite
		series.Cost[idx] += cost
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("usage series rows: %w", err)
	}
	return series, nil
}

// ModelUsage is one row of the per-model usage ranking.
type ModelUsage struct {
	Model    string  `json:"model"`
	Requests int     `json:"requests"`
	Tokens   int64   `json:"total_tokens"`
	Cost     float64 `json:"cost"`
	Failed   int     `json:"failed"`
}

// TopModels ranks models by token usage inside an inclusive window. Ranking in
// SQL means a chart over a week is not biased by the list endpoint's row cap.
func (s *UsageStore) TopModels(since, until *time.Time, limit int) ([]ModelUsage, error) {
	if limit <= 0 {
		limit = 8
	}
	if limit > 50 {
		limit = 50
	}
	where := []string{"model <> ''"}
	args := []any{}
	if clauses, rangeArgs := createdRange("created_at", since, until); len(clauses) > 0 {
		where = append(where, clauses...)
		args = append(args, rangeArgs...)
	}
	args = append(args, limit)
	rows, err := s.db.Query(
		`SELECT model, COUNT(*),
			COALESCE(SUM(total_tokens), 0),
			COALESCE(SUM(cost), 0),
			COALESCE(SUM(CASE WHEN status >= 400 THEN 1 ELSE 0 END), 0)
		FROM usage_records
		WHERE `+strings.Join(where, " AND ")+`
		GROUP BY model
		ORDER BY 3 DESC, 2 DESC
		LIMIT ?`,
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("usage top models: %w", err)
	}
	defer rows.Close()
	var result []ModelUsage
	for rows.Next() {
		var row ModelUsage
		if err := rows.Scan(&row.Model, &row.Requests, &row.Tokens, &row.Cost, &row.Failed); err != nil {
			return nil, fmt.Errorf("usage top models scan: %w", err)
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

// CostByKey returns the persisted billing total per downstream key (all time).
// Only keys with usage appear in the map. It replaces the retired key-level
// unit prices: the authoritative per-key spend is what the relay actually
// recorded, not a price×token estimate.
func (s *UsageStore) CostByKey() (map[int64]float64, error) {
	rows, err := s.db.Query(`SELECT downstream_key_id, COALESCE(SUM(cost), 0) FROM usage_records WHERE downstream_key_id > 0 GROUP BY downstream_key_id`)
	if err != nil {
		return nil, fmt.Errorf("usage cost by key: %w", err)
	}
	defer rows.Close()
	out := make(map[int64]float64)
	for rows.Next() {
		var id int64
		var cost float64
		if err := rows.Scan(&id, &cost); err != nil {
			return nil, fmt.Errorf("usage cost by key scan: %w", err)
		}
		out[id] = cost
	}
	return out, rows.Err()
}

// CostByRequestIDs returns the persisted billing amount for each request id.
// Missing ids are absent from the map. Batched to stay well within SQLite's
// bound-parameter limit when a page of logs is annotated in one call.
func (s *UsageStore) CostByRequestIDs(ids []string) (map[string]float64, error) {
	out := make(map[string]float64)
	filtered := make([]string, 0, len(ids))
	for _, id := range ids {
		if strings.TrimSpace(id) != "" {
			filtered = append(filtered, id)
		}
	}
	if len(filtered) == 0 {
		return out, nil
	}
	const chunkSize = 400
	for start := 0; start < len(filtered); start += chunkSize {
		end := start + chunkSize
		if end > len(filtered) {
			end = len(filtered)
		}
		batch := filtered[start:end]
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		args := make([]any, len(batch))
		for i, id := range batch {
			args[i] = id
		}
		rows, err := s.db.Query(
			`SELECT request_id, COALESCE(SUM(cost), 0) FROM usage_records WHERE request_id IN (`+placeholders+`) GROUP BY request_id`,
			args...,
		)
		if err != nil {
			return nil, fmt.Errorf("usage cost by request: %w", err)
		}
		for rows.Next() {
			var requestID string
			var cost float64
			if err := rows.Scan(&requestID, &cost); err != nil {
				rows.Close()
				return nil, fmt.Errorf("usage cost by request scan: %w", err)
			}
			out[requestID] = cost
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, fmt.Errorf("usage cost by request rows: %w", err)
		}
	}
	return out, nil
}

// RecordRelayUsage atomically persists one relay's usage accounting: the
// usage_records row, the downstream-key quota increment, and the token
// backfill on the newest proxy_log row for the request. A single transaction
// removes the partial-write window where usage lands but the key quota does
// not (or vice versa) and cuts the hot-path write round-trips from three
// to one. Rows with no measurable tokens are a no-op.
func (db *DB) RecordRelayUsage(record *domain.UsageRecord, keyID int64) error {
	if record == nil || record.TotalTokens <= 0 {
		return nil
	}
	keyEpoch := db.DownstreamKey.mutationEpochSnapshot()
	groupName := normalizeGroupName(record.GroupName)
	groupEpoch := db.Group.mutationEpochSnapshot()
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("usage record begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stream := 0
	if record.Stream {
		stream = 1
	}
	if _, err := tx.Exec(
		`INSERT INTO usage_records (
			request_id, downstream_key_id, channel_id, model, path, stream,
			prompt_tokens, completion_tokens, total_tokens,
			cache_read_tokens, cache_creation_tokens, status, cost
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.RequestID,
		record.DownstreamKeyID,
		record.ChannelID,
		record.Model,
		record.Path,
		stream,
		record.PromptTokens,
		record.CompletionTokens,
		record.TotalTokens,
		record.CacheReadTokens,
		record.CacheCreationTokens,
		record.Status,
		record.Cost,
	); err != nil {
		return fmt.Errorf("usage record insert: %w", err)
	}

	var keyUsage int64
	keyUsageUpdated := false
	if keyID > 0 {
		err := tx.QueryRow(`UPDATE downstream_keys SET quota_used_tokens = quota_used_tokens + ? WHERE id = ? RETURNING quota_used_tokens`, record.TotalTokens, keyID).Scan(&keyUsage)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("usage record key quota: %w", err)
		}
		keyUsageUpdated = err == nil
	}

	// Accrue the tenant group quota in the same transaction (no-op when the
	// group row does not exist — absent groups are unlimited).
	var groupUsage int64
	groupUsageUpdated := false
	if groupName != "" {
		err := tx.QueryRow(`UPDATE key_groups SET quota_used_tokens = quota_used_tokens + ?, updated_at = datetime('now') WHERE name = ? RETURNING quota_used_tokens`, record.TotalTokens, groupName).Scan(&groupUsage)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("usage record group quota: %w", err)
		}
		groupUsageUpdated = err == nil
	}

	if strings.TrimSpace(record.RequestID) != "" {
		if _, err := tx.Exec(
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
			record.PromptTokens, record.CompletionTokens, record.TotalTokens,
			record.CacheReadTokens, record.CacheCreationTokens,
			record.CompletionTokens, float64(record.CompletionTokens), record.RequestID,
		); err != nil {
			return fmt.Errorf("usage record log backfill: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("usage record commit: %w", err)
	}
	// Keep hot caches in sync with the absolute committed values. The epoch
	// check prevents an older callback from undoing a concurrent reset/update.
	if keyUsageUpdated {
		db.DownstreamKey.setCachedUsageIfEpoch(keyID, keyUsage, keyEpoch)
	}
	if groupUsageUpdated {
		db.Group.setCachedUsageIfEpoch(groupName, groupUsage, groupEpoch)
	}
	return nil
}

// ModelRatioStore persists per-model billing markup (ratio 1.0 = no markup).
// The table is tiny and written only by admins; reads on the usage path are
// served from a small process cache invalidated by SetRatio.
type ModelRatioStore struct {
	db *sql.DB

	mu         sync.RWMutex
	cache      map[string]float64
	generation uint64
}

func newModelRatioStore(db *sql.DB) *ModelRatioStore {
	return &ModelRatioStore{db: db, cache: make(map[string]float64)}
}

// ClearCache drops all cached model ratios after a bulk SQL operation.
func (s *ModelRatioStore) ClearCache() {
	s.mu.Lock()
	s.cache = make(map[string]float64)
	s.generation++
	s.mu.Unlock()
}

// GetRatio returns the markup for a model (1.0 when unset).
func (s *ModelRatioStore) GetRatio(model string) (float64, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		return 1.0, nil
	}
	s.mu.RLock()
	ratio, ok := s.cache[model]
	generation := s.generation
	s.mu.RUnlock()
	if ok {
		return ratio, nil
	}
	var stored float64
	err := s.db.QueryRow(`SELECT ratio FROM model_ratios WHERE model = ?`, model).Scan(&stored)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			s.cachePutIfGeneration(model, 1.0, generation)
			return 1.0, nil
		}
		return 1.0, fmt.Errorf("model ratio get: %w", err)
	}
	s.cachePutIfGeneration(model, stored, generation)
	return stored, nil
}

func (s *ModelRatioStore) cachePutIfGeneration(model string, ratio float64, generation uint64) {
	s.mu.Lock()
	if s.generation == generation {
		s.cache[model] = ratio
	}
	s.mu.Unlock()
}

// SetRatio upserts a model's markup and refreshes the cache. Deleting a ratio
// (ratio < 0) falls back to 1.0.
func (s *ModelRatioStore) SetRatio(model string, ratio float64) error {
	model = strings.TrimSpace(model)
	if model == "" {
		return fmt.Errorf("model ratio: empty model")
	}
	if ratio < 0 {
		if _, err := s.db.Exec(`DELETE FROM model_ratios WHERE model = ?`, model); err != nil {
			return fmt.Errorf("model ratio delete: %w", err)
		}
		s.mu.Lock()
		s.generation++
		delete(s.cache, model)
		s.mu.Unlock()
		return nil
	}
	if _, err := s.db.Exec(
		`INSERT INTO model_ratios (model, ratio, updated_at) VALUES (?, ?, datetime('now'))
		 ON CONFLICT(model) DO UPDATE SET ratio = excluded.ratio, updated_at = datetime('now')`,
		model, ratio,
	); err != nil {
		return fmt.Errorf("model ratio set: %w", err)
	}
	s.mu.Lock()
	s.generation++
	s.cache[model] = ratio
	s.mu.Unlock()
	return nil
}

// ListRatios returns all configured model ratios ordered by model.
func (s *ModelRatioStore) ListRatios() ([]domain.ModelRatio, error) {
	rows, err := s.db.Query(`SELECT model, ratio, updated_at FROM model_ratios ORDER BY model`)
	if err != nil {
		return nil, fmt.Errorf("model ratio list: %w", err)
	}
	defer rows.Close()
	var result []domain.ModelRatio
	for rows.Next() {
		var r domain.ModelRatio
		if err := rows.Scan(&r.Model, &r.Ratio, scanTime(&r.UpdatedAt)); err != nil {
			return nil, fmt.Errorf("model ratio scan: %w", err)
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

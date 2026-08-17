package store

import (
	"database/sql"
	"errors"
	"fmt"
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
	query := `SELECT COUNT(*),
		COALESCE(SUM(prompt_tokens),0),
		COALESCE(SUM(completion_tokens),0),
		COALESCE(SUM(total_tokens),0),
		COALESCE(SUM(cost),0)
		FROM usage_records`
	where := []string{}
	args := []any{}
	if downstreamKeyID != nil {
		where = append(where, "downstream_key_id = ?")
		args = append(args, *downstreamKeyID)
	}
	if since != nil {
		where = append(where, "created_at >= ?")
		args = append(args, since.UTC().Format("2006-01-02 15:04:05"))
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
		&summary.Cost,
	); err != nil {
		return domain.UsageSummary{}, fmt.Errorf("usage summary: %w", err)
	}
	return summary, nil
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

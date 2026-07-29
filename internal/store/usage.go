package store

import (
	"database/sql"
	"fmt"
	"strings"

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
			prompt_tokens, completion_tokens, total_tokens, status
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.RequestID,
		record.DownstreamKeyID,
		record.ChannelID,
		record.Model,
		record.Path,
		stream,
		record.PromptTokens,
		record.CompletionTokens,
		record.TotalTokens,
		record.Status,
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
		prompt_tokens, completion_tokens, total_tokens, status, created_at
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
			&record.Status,
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
	query := `SELECT COUNT(*),
		COALESCE(SUM(prompt_tokens),0),
		COALESCE(SUM(completion_tokens),0),
		COALESCE(SUM(total_tokens),0)
		FROM usage_records`
	args := []any{}
	if downstreamKeyID != nil {
		query += ` WHERE downstream_key_id = ?`
		args = append(args, *downstreamKeyID)
	}
	var summary domain.UsageSummary
	if err := s.db.QueryRow(query, args...).Scan(
		&summary.RequestCount,
		&summary.PromptTokens,
		&summary.CompletionTokens,
		&summary.TotalTokens,
	); err != nil {
		return domain.UsageSummary{}, fmt.Errorf("usage summary: %w", err)
	}
	return summary, nil
}

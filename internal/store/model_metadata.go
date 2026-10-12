package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
)

// ModelMetadataStore owns the optional per-model capability library.
type ModelMetadataStore struct {
	db *sql.DB
}

// Upsert inserts or replaces metadata for one canonical model name.
func (s *ModelMetadataStore) Upsert(meta *domain.ModelMetadata) error {
	if meta == nil || strings.TrimSpace(meta.ModelName) == "" {
		return fmt.Errorf("model metadata upsert: model_name is required")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.Exec(
		`INSERT INTO model_metadata (model_name, context_window, input_modalities, output_modalities, supports_thinking, vendor, notes, price_prompt_per_1k, price_completion_per_1k, price_cache_per_1k, price_per_request, price_tiers, price_schedule, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(model_name) DO UPDATE SET
		   context_window = excluded.context_window,
		   input_modalities = excluded.input_modalities,
		   output_modalities = excluded.output_modalities,
		   supports_thinking = excluded.supports_thinking,
		   vendor = excluded.vendor,
		   notes = excluded.notes,
		   price_prompt_per_1k = excluded.price_prompt_per_1k,
		   price_completion_per_1k = excluded.price_completion_per_1k,
		   price_cache_per_1k = excluded.price_cache_per_1k,
		   price_per_request = excluded.price_per_request,
		   price_tiers = excluded.price_tiers,
		   price_schedule = excluded.price_schedule,
		   updated_at = excluded.updated_at`,
		strings.TrimSpace(meta.ModelName), meta.ContextWindow,
		strings.TrimSpace(meta.InputModalities), strings.TrimSpace(meta.OutputModalities),
		meta.SupportsThinking, strings.TrimSpace(meta.Vendor), strings.TrimSpace(meta.Notes),
		meta.PricePromptPer1k, meta.PriceCompletionPer1k, meta.PriceCachePer1k, meta.PricePerRequest,
		meta.PriceTiers, meta.PriceSchedule,
		now,
	)
	if err != nil {
		return fmt.Errorf("model metadata upsert: %w", err)
	}
	return nil
}

// List returns all metadata rows ordered by model name.
func (s *ModelMetadataStore) List() ([]domain.ModelMetadata, error) {
	rows, err := s.db.Query(
		`SELECT id, model_name, context_window, input_modalities, output_modalities, supports_thinking, vendor, notes, price_prompt_per_1k, price_completion_per_1k, price_cache_per_1k, price_per_request, price_tiers, price_schedule, updated_at
		 FROM model_metadata ORDER BY model_name`)
	if err != nil {
		return nil, fmt.Errorf("model metadata list: %w", err)
	}
	defer rows.Close()
	var out []domain.ModelMetadata
	for rows.Next() {
		var m domain.ModelMetadata
		if err := rows.Scan(&m.ID, &m.ModelName, &m.ContextWindow, &m.InputModalities,
			&m.OutputModalities, &m.SupportsThinking, &m.Vendor, &m.Notes,
			&m.PricePromptPer1k, &m.PriceCompletionPer1k, &m.PriceCachePer1k, &m.PricePerRequest,
			&m.PriceTiers, &m.PriceSchedule, &m.UpdatedAt); err != nil {
			return nil, fmt.Errorf("model metadata list scan: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Get returns one row; nil when absent.
func (s *ModelMetadataStore) Get(modelName string) (*domain.ModelMetadata, error) {
	row := s.db.QueryRow(
		`SELECT id, model_name, context_window, input_modalities, output_modalities, supports_thinking, vendor, notes, price_prompt_per_1k, price_completion_per_1k, price_cache_per_1k, price_per_request, price_tiers, price_schedule, updated_at
		 FROM model_metadata WHERE model_name = ?`, strings.TrimSpace(modelName))
	var m domain.ModelMetadata
	if err := row.Scan(&m.ID, &m.ModelName, &m.ContextWindow, &m.InputModalities,
		&m.OutputModalities, &m.SupportsThinking, &m.Vendor, &m.Notes,
		&m.PricePromptPer1k, &m.PriceCompletionPer1k, &m.PriceCachePer1k, &m.PricePerRequest,
		&m.PriceTiers, &m.PriceSchedule, &m.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("model metadata get: %w", err)
	}
	return &m, nil
}

// Delete removes one row; missing rows are not an error.
func (s *ModelMetadataStore) Delete(modelName string) error {
	if _, err := s.db.Exec(`DELETE FROM model_metadata WHERE model_name = ?`, strings.TrimSpace(modelName)); err != nil {
		return fmt.Errorf("model metadata delete: %w", err)
	}
	return nil
}

// MergeCatalogMetadata never writes billing columns, even if a manual price is
// changed while the external download is in flight. Only unset metadata fills.
func (s *ModelMetadataStore) MergeCatalogMetadata(m *domain.ModelMetadata) error {
	_, err := s.db.Exec(`INSERT INTO model_metadata(model_name,context_window,input_modalities,output_modalities,supports_thinking,vendor,notes,updated_at)
 VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(model_name) DO UPDATE SET
 context_window=CASE WHEN model_metadata.context_window=0 THEN excluded.context_window ELSE model_metadata.context_window END,
 input_modalities=CASE WHEN model_metadata.input_modalities='' THEN excluded.input_modalities ELSE model_metadata.input_modalities END,
 output_modalities=CASE WHEN model_metadata.output_modalities='' THEN excluded.output_modalities ELSE model_metadata.output_modalities END,
 supports_thinking=CASE WHEN model_metadata.supports_thinking=-1 THEN excluded.supports_thinking ELSE model_metadata.supports_thinking END,
 vendor=CASE WHEN model_metadata.vendor='' THEN excluded.vendor ELSE model_metadata.vendor END,
 updated_at=excluded.updated_at`, m.ModelName, m.ContextWindow, m.InputModalities, m.OutputModalities, m.SupportsThinking, m.Vendor, m.Notes, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

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
		`INSERT INTO model_metadata (model_name, context_window, input_modalities, output_modalities, supports_thinking, vendor, notes, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(model_name) DO UPDATE SET
		   context_window = excluded.context_window,
		   input_modalities = excluded.input_modalities,
		   output_modalities = excluded.output_modalities,
		   supports_thinking = excluded.supports_thinking,
		   vendor = excluded.vendor,
		   notes = excluded.notes,
		   updated_at = excluded.updated_at`,
		strings.TrimSpace(meta.ModelName), meta.ContextWindow,
		strings.TrimSpace(meta.InputModalities), strings.TrimSpace(meta.OutputModalities),
		meta.SupportsThinking, strings.TrimSpace(meta.Vendor), strings.TrimSpace(meta.Notes),
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
		`SELECT id, model_name, context_window, input_modalities, output_modalities, supports_thinking, vendor, notes, updated_at
		 FROM model_metadata ORDER BY model_name`)
	if err != nil {
		return nil, fmt.Errorf("model metadata list: %w", err)
	}
	defer rows.Close()
	var out []domain.ModelMetadata
	for rows.Next() {
		var m domain.ModelMetadata
		if err := rows.Scan(&m.ID, &m.ModelName, &m.ContextWindow, &m.InputModalities,
			&m.OutputModalities, &m.SupportsThinking, &m.Vendor, &m.Notes, &m.UpdatedAt); err != nil {
			return nil, fmt.Errorf("model metadata list scan: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Get returns one row; nil when absent.
func (s *ModelMetadataStore) Get(modelName string) (*domain.ModelMetadata, error) {
	row := s.db.QueryRow(
		`SELECT id, model_name, context_window, input_modalities, output_modalities, supports_thinking, vendor, notes, updated_at
		 FROM model_metadata WHERE model_name = ?`, strings.TrimSpace(modelName))
	var m domain.ModelMetadata
	if err := row.Scan(&m.ID, &m.ModelName, &m.ContextWindow, &m.InputModalities,
		&m.OutputModalities, &m.SupportsThinking, &m.Vendor, &m.Notes, &m.UpdatedAt); err != nil {
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

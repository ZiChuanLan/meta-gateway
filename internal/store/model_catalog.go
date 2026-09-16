package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
)

// ModelCatalogStore owns the single-row status board for the external model
// catalog sync (LiteLLM's price list and models.dev's capability index).
type ModelCatalogStore struct {
	db *sql.DB
}

const catalogStateColumns = `synced_at, requested, matched, capabilities, metadata, prices,
	skipped_manual, missing, sources, errors`

// GetCatalogState returns the last sync outcome, or nil when the registry has
// never been synced. The bool reports whether it came from the database.
func (s *ModelCatalogStore) GetCatalogState() (*domain.CatalogSyncState, error) {
	row := s.db.QueryRow(`SELECT ` + catalogStateColumns + ` FROM model_catalog_sync WHERE id = 1`)
	var (
		state       domain.CatalogSyncState
		sources     string
		problemText string
	)
	err := row.Scan(&state.SyncedAt, &state.Requested, &state.Matched, &state.Capabilities,
		&state.Metadata, &state.Prices, &state.SkippedManual, &state.Missing,
		&sources, &problemText)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("model catalog state: %w", err)
	}
	state.Sources = splitNonEmpty(sources)
	state.Errors = splitNonEmpty(problemText)
	return &state, nil
}

// SaveCatalogState overwrites the status board. Only the latest sync matters,
// so there is no history to preserve.
func (s *ModelCatalogStore) SaveCatalogState(state *domain.CatalogSyncState) error {
	if state == nil {
		return nil
	}
	syncedAt := strings.TrimSpace(state.SyncedAt)
	if syncedAt == "" {
		syncedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	_, err := s.db.Exec(
		`INSERT INTO model_catalog_sync (id, synced_at, requested, matched, capabilities, metadata,
		   prices, skipped_manual, missing, sources, errors)
		 VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   synced_at = excluded.synced_at,
		   requested = excluded.requested,
		   matched = excluded.matched,
		   capabilities = excluded.capabilities,
		   metadata = excluded.metadata,
		   prices = excluded.prices,
		   skipped_manual = excluded.skipped_manual,
		   missing = excluded.missing,
		   sources = excluded.sources,
		   errors = excluded.errors`,
		syncedAt, state.Requested, state.Matched, state.Capabilities, state.Metadata,
		state.Prices, state.SkippedManual, state.Missing,
		strings.Join(state.Sources, ","), strings.Join(state.Errors, "\n"),
	)
	if err != nil {
		return fmt.Errorf("model catalog state save: %w", err)
	}
	return nil
}

// splitNonEmpty splits a comma- or newline-separated column into trimmed parts,
// dropping empties so a blank column yields a nil slice rather than [""].
func splitNonEmpty(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == '\n' })
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if trimmed := strings.TrimSpace(field); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

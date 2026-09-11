package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
)

// DiscoveredModelStore owns discovery snapshots and route reconciliation.
type DiscoveredModelStore struct {
	db *sql.DB
	// credential invalidates the per-site model-set cache after snapshots
	// rewrite a key's recorded models.
	credential *CredentialStore
}

type ReconcileInput struct {
	ChannelID int64
	Models    []string
	// CredentialModels records which models each credential could list in this
	// successful snapshot (key -> model names). It is persisted so routing can
	// pick a key that actually serves the requested model.
	CredentialModels map[int64][]string
	// PartialDiscovery marks a snapshot taken while only some of the channel's
	// API keys returned a model list. Removals recorded from such a snapshot
	// may be false positives (an unreachable key hides its models from the
	// merge), so they are flagged and do not advance the confirmation counter.
	PartialDiscovery bool
	Source           string
	LatencyMs        int
	CheckedAt        time.Time
}

type ReconcileResult struct {
	CreatedRoutes  int `json:"created_routes"`
	CreatedMembers int `json:"created_members"`
	DeletedMembers int `json:"deleted_members"`
	DeletedRoutes  int `json:"deleted_routes"`
}

func (s *DiscoveredModelStore) List(channelID *int64) ([]domain.DiscoveredModel, error) {
	query := `SELECT id, channel_id, model_name, available, source, latency_ms, checked_at FROM discovered_models`
	var args []any
	if channelID != nil {
		query += ` WHERE channel_id = ?`
		args = append(args, *channelID)
	}
	query += ` ORDER BY channel_id, model_name`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("discovered model list: %w", err)
	}
	defer rows.Close()
	result := make([]domain.DiscoveredModel, 0)
	for rows.Next() {
		var model domain.DiscoveredModel
		var available int
		if err := rows.Scan(&model.ID, &model.ChannelID, &model.ModelName, &available, &model.Source, &model.LatencyMs, scanTime(&model.CheckedAt)); err != nil {
			return nil, fmt.Errorf("discovered model scan: %w", err)
		}
		model.Available = available != 0
		result = append(result, model)
	}
	return result, rows.Err()
}

// Reconcile replaces one channel's successful snapshot and updates automatic routing atomically.
func (s *DiscoveredModelStore) Reconcile(ctx context.Context, input ReconcileInput) (result ReconcileResult, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, fmt.Errorf("discovery reconcile begin: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	var priority, weight int
	var syncMode string
	if err = tx.QueryRowContext(ctx, `SELECT priority, weight, model_sync_mode FROM channels WHERE id = ?`, input.ChannelID).Scan(&priority, &weight, &syncMode); err != nil {
		return result, fmt.Errorf("discovery reconcile channel: %w", err)
	}

	oldModels := make([]string, 0)
	rows, queryErr := tx.QueryContext(ctx, `SELECT model_name FROM discovered_models WHERE channel_id = ? ORDER BY model_name`, input.ChannelID)
	if queryErr != nil {
		return result, fmt.Errorf("discovery reconcile old snapshot: %w", queryErr)
	}
	for rows.Next() {
		var model string
		if scanErr := rows.Scan(&model); scanErr != nil {
			_ = rows.Close()
			return result, fmt.Errorf("discovery reconcile old model: %w", scanErr)
		}
		oldModels = append(oldModels, model)
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return result, fmt.Errorf("discovery reconcile old models: %w", err)
	}
	_ = rows.Close()

	if err = recordModelChanges(tx, input, oldModels); err != nil {
		return result, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM discovered_models WHERE channel_id = ?`, input.ChannelID); err != nil {
		return result, fmt.Errorf("discovery reconcile clear snapshot: %w", err)
	}
	checkedAt := input.CheckedAt.UTC().Format(time.RFC3339Nano)
	for _, model := range input.Models {
		if _, err = tx.ExecContext(ctx, `INSERT INTO discovered_models (channel_id, model_name, available, source, latency_ms, checked_at) VALUES (?, ?, 1, ?, ?, ?)`, input.ChannelID, model, input.Source, input.LatencyMs, checkedAt); err != nil {
			return result, fmt.Errorf("discovery reconcile insert snapshot: %w", err)
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE channels SET models_csv = ?, updated_at = datetime('now') WHERE id = ?`, strings.Join(input.Models, ","), input.ChannelID); err != nil {
		return result, fmt.Errorf("discovery reconcile channel models: %w", err)
	}

	// Persist per-credential visibility for this successful snapshot. Keys that
	// failed to list models in this pass keep their previously recorded set
	// (stale-but-known beats empty); keys absent from the map are untouched.
	for credentialID, models := range input.CredentialModels {
		if _, err = tx.ExecContext(ctx, `DELETE FROM credential_models WHERE credential_id = ?`, credentialID); err != nil {
			return result, fmt.Errorf("discovery reconcile clear credential models: %w", err)
		}
		for _, model := range models {
			if _, err = tx.ExecContext(ctx, `INSERT INTO credential_models (credential_id, model_name, checked_at) VALUES (?, ?, ?)`, credentialID, model, checkedAt); err != nil {
				return result, fmt.Errorf("discovery reconcile insert credential model: %w", err)
			}
		}
	}

	for _, model := range input.Models {
		// Manual-sync channels keep the snapshot as the adoption candidate
		// list only: routes/members are created on demand from the channel
		// models panel. Auto-sync channels adopt every probed model.
		if domain.NormalizeModelSyncMode(syncMode) != domain.ModelSyncModeAuto {
			continue
		}
		var routeID int64
		err = tx.QueryRowContext(ctx, `SELECT id FROM routes WHERE model_pattern = ?`, model).Scan(&routeID)
		if err == sql.ErrNoRows {
			var created sql.Result
			created, err = tx.ExecContext(ctx, `INSERT INTO routes (model_pattern, enabled) VALUES (?, 1)`, model)
			if err != nil {
				return result, fmt.Errorf("discovery reconcile create route: %w", err)
			}
			routeID, err = created.LastInsertId()
			if err != nil {
				return result, fmt.Errorf("discovery reconcile route id: %w", err)
			}
			result.CreatedRoutes++
		} else if err != nil {
			return result, fmt.Errorf("discovery reconcile route: %w", err)
		}

		var replaced int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM model_change_remaps x JOIN route_members m ON m.id=x.member_id WHERE m.route_id=? AND x.source_channel_id=?`, routeID, input.ChannelID).Scan(&replaced); err != nil {
			return result, err
		}
		if replaced > 0 {
			continue
		}
		var memberID int64
		err = tx.QueryRowContext(ctx, `SELECT id FROM route_members WHERE route_id = ? AND channel_id = ?`, routeID, input.ChannelID).Scan(&memberID)
		if err == sql.ErrNoRows {
			_, err = tx.ExecContext(ctx, `INSERT INTO route_members (route_id, channel_id, priority, weight, enabled, auto, manual_override) VALUES (?, ?, ?, ?, 1, 1, 0)`, routeID, input.ChannelID, priority, weight)
			if err != nil {
				return result, fmt.Errorf("discovery reconcile create member: %w", err)
			}
			result.CreatedMembers++
		} else if err != nil {
			return result, fmt.Errorf("discovery reconcile member: %w", err)
		}
		// An existing member's enabled flag is left alone: whether a member is
		// off belongs to the operator (or the probe that disabled it), not to a
		// snapshot refresh. Reconcile only creates members.
	}

	// Retain disappeared bindings and routes, including automatic ones, so an
	// operator can remap them without losing IDs, overrides, or health state.

	if err = tx.Commit(); err != nil {
		return result, fmt.Errorf("discovery reconcile commit: %w", err)
	}
	// Offline cache of the per-key model sets changed inside this transaction;
	// drop the stale cached copy so the next relay sees the fresh sets.
	for credentialID := range input.CredentialModels {
		s.credential.InvalidateModelSetFor(credentialID)
	}
	return result, nil
}

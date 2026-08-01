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
}

type ReconcileInput struct {
	ChannelID int64
	Models    []string
	Source    string
	LatencyMs int
	CheckedAt time.Time
}

type ReconcileResult struct {
	CreatedRoutes   int `json:"created_routes"`
	CreatedMembers  int `json:"created_members"`
	EnabledMembers  int `json:"enabled_members"`
	DeletedMembers  int `json:"deleted_members"`
	DeletedRoutes   int `json:"deleted_routes"`
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
	if err = tx.QueryRowContext(ctx, `SELECT priority, weight FROM channels WHERE id = ?`, input.ChannelID).Scan(&priority, &weight); err != nil {
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

	current := make(map[string]struct{}, len(input.Models))
	for _, model := range input.Models {
		current[model] = struct{}{}
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

		var memberID int64
		var enabled, auto, manualOverride int
		err = tx.QueryRowContext(ctx, `SELECT id, enabled, auto, manual_override FROM route_members WHERE route_id = ? AND channel_id = ?`, routeID, input.ChannelID).Scan(&memberID, &enabled, &auto, &manualOverride)
		if err == sql.ErrNoRows {
			_, err = tx.ExecContext(ctx, `INSERT INTO route_members (route_id, channel_id, priority, weight, enabled, auto, manual_override) VALUES (?, ?, ?, ?, 1, 1, 0)`, routeID, input.ChannelID, priority, weight)
			if err != nil {
				return result, fmt.Errorf("discovery reconcile create member: %w", err)
			}
			result.CreatedMembers++
		} else if err != nil {
			return result, fmt.Errorf("discovery reconcile member: %w", err)
		} else if enabled == 0 && auto != 0 && manualOverride == 0 {
			if _, err = tx.ExecContext(ctx, `UPDATE route_members SET enabled = 1, updated_at = datetime('now') WHERE id = ?`, memberID); err != nil {
				return result, fmt.Errorf("discovery reconcile enable member: %w", err)
			}
			result.EnabledMembers++
		}
	}

	for _, model := range oldModels {
		if _, exists := current[model]; exists {
			continue
		}
		// Upstream models that disappeared are dropped from automatic routing:
		// auto-created members are removed (manual_override=1 members survive),
		// and the route itself is deleted once no member references it anymore.
		res, execErr := tx.ExecContext(ctx, `DELETE FROM route_members WHERE channel_id = ? AND auto = 1 AND manual_override = 0 AND route_id = (SELECT id FROM routes WHERE model_pattern = ?)`, input.ChannelID, model)
		if execErr != nil {
			return result, fmt.Errorf("discovery reconcile remove stale member: %w", execErr)
		}
		if count, countErr := res.RowsAffected(); countErr == nil {
			result.DeletedMembers += int(count)
		}
		var routeID int64
		rowErr := tx.QueryRowContext(ctx, `SELECT id FROM routes WHERE model_pattern = ?`, model).Scan(&routeID)
		if rowErr == nil {
			var remaining int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM route_members WHERE route_id = ?`, routeID).Scan(&remaining); err != nil {
				return result, fmt.Errorf("discovery reconcile count route members: %w", err)
			}
			if remaining == 0 {
				if _, delErr := tx.ExecContext(ctx, `DELETE FROM routes WHERE id = ?`, routeID); delErr != nil {
					return result, fmt.Errorf("discovery reconcile remove empty route: %w", delErr)
				}
				result.DeletedRoutes++
			}
		} else if rowErr != sql.ErrNoRows {
			return result, fmt.Errorf("discovery reconcile lookup route: %w", rowErr)
		}
	}

	if err = tx.Commit(); err != nil {
		return result, fmt.Errorf("discovery reconcile commit: %w", err)
	}
	return result, nil
}

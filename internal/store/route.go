package store

import (
	"database/sql"
	"fmt"

	"github.com/lan/meta-gateway/internal/domain"
)

// RouteStore provides CRUD operations for routes.
type RouteStore struct {
	db *sql.DB
}

func scanRoute(scanner interface {
	Scan(dest ...any) error
}, r *domain.Route) error {
	var enabled int
	if err := scanner.Scan(&r.ID, &r.ModelPattern, &enabled, &r.MappingJSON, &r.Notes, scanTime(&r.CreatedAt), scanTime(&r.UpdatedAt)); err != nil {
		return err
	}
	r.Enabled = enabled != 0
	return nil
}

func (s *RouteStore) List() ([]domain.Route, error) {
	rows, err := s.db.Query(`SELECT id, model_pattern, enabled, mapping_json, notes, created_at, updated_at FROM routes ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("route list: %w", err)
	}
	defer rows.Close()

	var result []domain.Route
	for rows.Next() {
		var r domain.Route
		if err := scanRoute(rows, &r); err != nil {
			return nil, fmt.Errorf("route scan: %w", err)
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func (s *RouteStore) GetByID(id int64) (*domain.Route, error) {
	row := s.db.QueryRow(`SELECT id, model_pattern, enabled, mapping_json, notes, created_at, updated_at FROM routes WHERE id = ?`, id)
	var r domain.Route
	if err := scanRoute(row, &r); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("route get: %w", err)
	}
	return &r, nil
}

// GetByModel returns the first enabled route matching the given model.
func (s *RouteStore) GetByModel(model string) (*domain.Route, error) {
	row := s.db.QueryRow(`SELECT id, model_pattern, enabled, mapping_json, notes, created_at, updated_at FROM routes WHERE model_pattern = ? AND enabled = 1 LIMIT 1`, model)
	var r domain.Route
	if err := scanRoute(row, &r); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("route by model: %w", err)
	}
	return &r, nil
}

func (s *RouteStore) Create(r *domain.Route) (int64, error) {
	enabled := 0
	if r.Enabled {
		enabled = 1
	}
	res, err := s.db.Exec(`INSERT INTO routes (model_pattern, enabled, mapping_json, notes) VALUES (?, ?, ?, ?)`,
		r.ModelPattern, enabled, r.MappingJSON, r.Notes)
	if err != nil {
		return 0, fmt.Errorf("route create: %w", err)
	}
	return res.LastInsertId()
}

func (s *RouteStore) Update(r *domain.Route) error {
	enabled := 0
	if r.Enabled {
		enabled = 1
	}
	_, err := s.db.Exec(`UPDATE routes SET model_pattern=?, enabled=?, mapping_json=?, notes=?, updated_at=datetime('now') WHERE id=?`,
		r.ModelPattern, enabled, r.MappingJSON, r.Notes, r.ID)
	if err != nil {
		return fmt.Errorf("route update: %w", err)
	}
	return nil
}

func (s *RouteStore) Delete(id int64) error {
	_, err := s.db.Exec(`DELETE FROM routes WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("route delete: %w", err)
	}
	return nil
}

// RouteMemberStore provides CRUD operations for route members.
type RouteMemberStore struct {
	db *sql.DB
}

func scanRouteMember(scanner interface {
	Scan(dest ...any) error
}, r *domain.RouteMember) error {
	var enabled, auto, manual int
	if err := scanner.Scan(
		&r.ID,
		&r.RouteID,
		&r.ChannelID,
		&r.Priority,
		&r.Weight,
		&enabled,
		&auto,
		&manual,
		&r.FailCount,
		scanNullTime(&r.CooldownUntil),
		&r.LastError,
		scanTime(&r.CreatedAt),
		scanTime(&r.UpdatedAt),
	); err != nil {
		return err
	}
	r.Enabled = enabled != 0
	r.Auto = auto != 0
	r.ManualOverride = manual != 0
	return nil
}

func (s *RouteMemberStore) ListByRoute(routeID int64) ([]domain.RouteMember, error) {
	rows, err := s.db.Query(`SELECT id, route_id, channel_id, priority, weight, enabled, auto, manual_override, fail_count, cooldown_until, last_error, created_at, updated_at FROM route_members WHERE route_id = ? ORDER BY priority, weight DESC`, routeID)
	if err != nil {
		return nil, fmt.Errorf("route member list: %w", err)
	}
	defer rows.Close()

	var result []domain.RouteMember
	for rows.Next() {
		var r domain.RouteMember
		if err := scanRouteMember(rows, &r); err != nil {
			return nil, fmt.Errorf("route member scan: %w", err)
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func (s *RouteMemberStore) GetByID(id int64) (*domain.RouteMember, error) {
	row := s.db.QueryRow(`SELECT id, route_id, channel_id, priority, weight, enabled, auto, manual_override, fail_count, cooldown_until, last_error, created_at, updated_at FROM route_members WHERE id = ?`, id)
	var r domain.RouteMember
	if err := scanRouteMember(row, &r); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("route member get: %w", err)
	}
	return &r, nil
}

func (s *RouteMemberStore) Create(r *domain.RouteMember) (int64, error) {
	enabled, auto, manual := boolInt(r.Enabled), boolInt(r.Auto), boolInt(r.ManualOverride)
	res, err := s.db.Exec(`INSERT INTO route_members (route_id, channel_id, priority, weight, enabled, auto, manual_override) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		r.RouteID, r.ChannelID, r.Priority, r.Weight, enabled, auto, manual)
	if err != nil {
		return 0, fmt.Errorf("route member create: %w", err)
	}
	return res.LastInsertId()
}

func (s *RouteMemberStore) Update(r *domain.RouteMember) error {
	enabled, auto, manual := boolInt(r.Enabled), boolInt(r.Auto), boolInt(r.ManualOverride)
	_, err := s.db.Exec(`UPDATE route_members SET priority=?, weight=?, enabled=?, auto=?, manual_override=?, fail_count=?, cooldown_until=?, last_error=?, updated_at=datetime('now') WHERE id=?`,
		r.Priority, r.Weight, enabled, auto, manual, r.FailCount, r.CooldownUntil, r.LastError, r.ID)
	if err != nil {
		return fmt.Errorf("route member update: %w", err)
	}
	return nil
}

func (s *RouteMemberStore) Delete(id int64) error {
	_, err := s.db.Exec(`DELETE FROM route_members WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("route member delete: %w", err)
	}
	return nil
}

// DeleteByRoute deletes all members for a given route.
func (s *RouteMemberStore) DeleteByRoute(routeID int64) error {
	_, err := s.db.Exec(`DELETE FROM route_members WHERE route_id = ?`, routeID)
	if err != nil {
		return fmt.Errorf("route member delete by route: %w", err)
	}
	return nil
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

package store

import (
	"database/sql"
	"fmt"
	"time"

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
	rows, err := s.db.Query(`SELECT id, route_id, channel_id, priority, weight, enabled, auto, manual_override, fail_count, cooldown_until, last_error, created_at, updated_at FROM route_members WHERE route_id = ? ORDER BY priority DESC, weight DESC, id`, routeID)
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

// RoutingCandidates loads all member and channel facts for an exact enabled route.
func (s *RouteMemberStore) RoutingCandidates(model string) (*domain.Route, []domain.RoutingCandidate, error) {
	routeRow := s.db.QueryRow(`SELECT id, model_pattern, enabled, mapping_json, notes, created_at, updated_at FROM routes WHERE model_pattern = ? AND enabled = 1`, model)
	var route domain.Route
	if err := scanRoute(routeRow, &route); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("routing route: %w", err)
	}
	rows, err := s.db.Query(`SELECT
		rm.id, rm.route_id, rm.channel_id, rm.priority, rm.weight, rm.enabled, rm.auto, rm.manual_override,
		rm.fail_count, rm.cooldown_until, rm.last_error, rm.created_at, rm.updated_at,
		c.id, c.site_id, c.credential_id, c.name, c.base_url, c.models_csv, c.group_name,
		c.priority, c.weight, c.status, c.type_hint, c.created_at, c.updated_at,
		CASE WHEN cred.id IS NOT NULL AND cred.status = 'enabled' AND cred.secret_enc <> '' THEN 1 ELSE 0 END
		FROM route_members rm JOIN channels c ON c.id = rm.channel_id
		LEFT JOIN credentials cred ON cred.id = c.credential_id
		WHERE rm.route_id = ? ORDER BY rm.priority DESC, rm.id`, route.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("routing candidates: %w", err)
	}
	defer rows.Close()
	var result []domain.RoutingCandidate
	for rows.Next() {
		var candidate domain.RoutingCandidate
		var enabled, auto, manual, credentialUsable int
		if err := rows.Scan(
			&candidate.Member.ID, &candidate.Member.RouteID, &candidate.Member.ChannelID,
			&candidate.Member.Priority, &candidate.Member.Weight, &enabled, &auto, &manual,
			&candidate.Member.FailCount, scanNullTime(&candidate.Member.CooldownUntil), &candidate.Member.LastError,
			scanTime(&candidate.Member.CreatedAt), scanTime(&candidate.Member.UpdatedAt),
			&candidate.Channel.ID, &candidate.Channel.SiteID, &candidate.Channel.CredentialID,
			&candidate.Channel.Name, &candidate.Channel.BaseURL, &candidate.Channel.ModelsCSV,
			&candidate.Channel.GroupName, &candidate.Channel.Priority, &candidate.Channel.Weight,
			&candidate.Channel.Status, &candidate.Channel.TypeHint,
			scanTime(&candidate.Channel.CreatedAt), scanTime(&candidate.Channel.UpdatedAt),
			&credentialUsable,
		); err != nil {
			return nil, nil, fmt.Errorf("routing candidate scan: %w", err)
		}
		candidate.Member.Enabled = enabled != 0
		candidate.Member.Auto = auto != 0
		candidate.Member.ManualOverride = manual != 0
		candidate.CredentialUsable = credentialUsable != 0
		result = append(result, candidate)
	}
	return &route, result, rows.Err()
}

func (s *RouteMemberStore) RecordFailure(id int64, now time.Time, cooldown time.Duration, category string) error {
	until := now.Add(cooldown).UTC().Format(time.RFC3339Nano)
	_, err := s.db.Exec(`UPDATE route_members SET fail_count=fail_count+1, cooldown_until=?, last_error=?, updated_at=datetime('now') WHERE id=?`, until, category, id)
	if err != nil {
		return fmt.Errorf("route member record failure: %w", err)
	}
	return nil
}

func (s *RouteMemberStore) RecordSuccess(id int64) error {
	_, err := s.db.Exec(`UPDATE route_members SET fail_count=0, cooldown_until=NULL, last_error='', updated_at=datetime('now') WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("route member record success: %w", err)
	}
	return nil
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
	var cooldownUntil any
	if r.CooldownUntil != nil {
		cooldownUntil = r.CooldownUntil.UTC().Format(time.RFC3339Nano)
	}
	_, err := s.db.Exec(`UPDATE route_members SET priority=?, weight=?, enabled=?, auto=?, manual_override=?, fail_count=?, cooldown_until=?, last_error=?, updated_at=datetime('now') WHERE id=?`,
		r.Priority, r.Weight, enabled, auto, manual, r.FailCount, cooldownUntil, r.LastError, r.ID)
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

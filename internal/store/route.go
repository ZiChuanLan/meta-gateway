package store

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
)

const (
	// maxCooldownBackoff caps exponential cooldown growth so repeated failures
	// can never overflow time.Duration into a past timestamp. The member simply
	// stays parked until the penalty expires or an admin clears health.
	maxCooldownBackoff = 24 * time.Hour

	// disableAfterConsecutiveFailures is the consecutive-failure threshold that
	// trips a circuit breaker: the member is disabled outright and must be
	// re-enabled by an admin (toggle or clear-health) before it is tried again.
	disableAfterConsecutiveFailures = 3
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

// GetByModel returns the best enabled route for the given model (exact, then wildcard).
func (s *RouteStore) GetByModel(model string) (*domain.Route, error) {
	row := s.db.QueryRow(`SELECT id, model_pattern, enabled, mapping_json, notes, created_at, updated_at FROM routes WHERE model_pattern = ? AND enabled = 1 LIMIT 1`, model)
	var exact domain.Route
	if err := scanRoute(row, &exact); err == nil {
		return &exact, nil
	} else if err != sql.ErrNoRows {
		return nil, fmt.Errorf("route by model: %w", err)
	}
	return findBestWildcardRoute(s.db, model)
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
	if err := s.RecoverExpired(); err != nil {
		return nil, err
	}
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

// ListRouteOverviews returns all routes and enriched members for the admin matrix.
func (s *RouteMemberStore) ListRouteOverviews() ([]domain.RouteOverview, error) {
	if err := s.RecoverExpired(); err != nil {
		return nil, err
	}
	routes, err := (&RouteStore{db: s.db}).List()
	if err != nil {
		return nil, err
	}
	result := make([]domain.RouteOverview, 0, len(routes))
	for _, route := range routes {
		members, memberErr := s.listCandidatesByRoute(route.ID)
		if memberErr != nil {
			return nil, memberErr
		}
		result = append(result, domain.RouteOverview{Route: route, Members: members})
	}
	return result, nil
}

func (s *RouteMemberStore) listCandidatesByRoute(routeID int64) ([]domain.RoutingCandidate, error) {
	rows, err := s.db.Query(`SELECT
		rm.id, rm.route_id, rm.channel_id, rm.priority, rm.weight, rm.enabled, rm.auto, rm.manual_override,
		rm.fail_count, rm.cooldown_until, rm.last_error, rm.created_at, rm.updated_at,
		c.id, c.site_id, c.credential_id, c.name, c.base_url, c.models_csv, c.group_name,
		c.priority, c.weight, c.status, c.type_hint, c.header_override, c.system_prompt, c.created_at, c.updated_at,
		CASE WHEN (
			cred.id IS NOT NULL AND cred.status = 'enabled' AND cred.secret_enc <> ''
			AND cred.site_id = c.site_id AND lower(cred.kind) = 'api_key'
		) OR EXISTS (
			SELECT 1 FROM credentials pool_cred
			WHERE pool_cred.site_id = c.site_id
			  AND pool_cred.status = 'enabled'
			  AND pool_cred.secret_enc <> ''
			  AND lower(pool_cred.kind) = 'api_key'
		) THEN 1 ELSE 0 END
		FROM route_members rm JOIN channels c ON c.id = rm.channel_id
		LEFT JOIN credentials cred ON cred.id = c.credential_id
		WHERE rm.route_id = ? ORDER BY rm.priority DESC, rm.weight DESC, rm.id`, routeID)
	if err != nil {
		return nil, fmt.Errorf("route overview members: %w", err)
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
			&candidate.Channel.HeaderOverride, &candidate.Channel.SystemPrompt,
			scanTime(&candidate.Channel.CreatedAt), scanTime(&candidate.Channel.UpdatedAt),
			&credentialUsable,
		); err != nil {
			return nil, fmt.Errorf("route overview member scan: %w", err)
		}
		candidate.Member.Enabled = enabled != 0
		candidate.Member.Auto = auto != 0
		candidate.Member.ManualOverride = manual != 0
		candidate.CredentialUsable = credentialUsable != 0
		result = append(result, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// A route without members must serialize as [] rather than null so the
	// admin UI never crashes on .some()/.length over a nil slice.
	if result == nil {
		result = []domain.RoutingCandidate{}
	}
	return result, nil
}

// RoutingCandidates loads member and channel facts for the best matching enabled route.
// Exact model_pattern wins; otherwise the longest wildcard (* or ?) match is used.
func (s *RouteMemberStore) RoutingCandidates(model string) (*domain.Route, []domain.RoutingCandidate, error) {
	routeRow := s.db.QueryRow(`SELECT id, model_pattern, enabled, mapping_json, notes, created_at, updated_at FROM routes WHERE model_pattern = ? AND enabled = 1`, model)
	var route domain.Route
	if err := scanRoute(routeRow, &route); err != nil {
		if err == sql.ErrNoRows {
			wildcard, wildErr := findBestWildcardRoute(s.db, model)
			if wildErr != nil {
				return nil, nil, wildErr
			}
			if wildcard == nil {
				return nil, nil, nil
			}
			route = *wildcard
		} else {
			return nil, nil, fmt.Errorf("routing route: %w", err)
		}
	}
	rows, err := s.db.Query(`SELECT
		rm.id, rm.route_id, rm.channel_id, rm.priority, rm.weight, rm.enabled, rm.auto, rm.manual_override,
		rm.fail_count, rm.cooldown_until, rm.last_error, rm.created_at, rm.updated_at,
		c.id, c.site_id, c.credential_id, c.name, c.base_url, c.models_csv, c.group_name,
		c.priority, c.weight, c.status, c.type_hint, c.header_override, c.system_prompt, c.created_at, c.updated_at,
		CASE WHEN (
			cred.id IS NOT NULL AND cred.status = 'enabled' AND cred.secret_enc <> ''
			AND lower(cred.kind) = 'api_key'
		) OR EXISTS (
			SELECT 1 FROM credentials pool_cred
			WHERE pool_cred.site_id = c.site_id
			  AND pool_cred.status = 'enabled'
			  AND pool_cred.secret_enc <> ''
			  AND lower(pool_cred.kind) = 'api_key'
		) THEN 1 ELSE 0 END
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
			&candidate.Channel.HeaderOverride, &candidate.Channel.SystemPrompt,
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
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("route member record failure begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var currentFailures int
	var cooldownUntil *time.Time
	if err = tx.QueryRow(`SELECT fail_count, cooldown_until FROM route_members WHERE id=?`, id).Scan(
		&currentFailures,
		scanNullTime(&cooldownUntil),
	); err != nil {
		return fmt.Errorf("route member record failure lookup: %w", err)
	}
	// Only consecutive failures within an active cooldown escalate backoff.
	// Once the previous penalty expires, start a fresh cooldown cycle.
	if cooldownUntil == nil || !cooldownUntil.After(now) {
		currentFailures = 0
	}
	nextFailures := currentFailures + 1
	backoff := cooldown
	// Double per consecutive failure, capped so the cooldown can never grow
	// past maxCooldownBackoff and overflow time.Duration into a past timestamp.
	for step := 1; step < nextFailures; step++ {
		backoff *= 2
		if backoff > maxCooldownBackoff {
			backoff = maxCooldownBackoff
		}
	}
	if nextFailures >= disableAfterConsecutiveFailures {
		// Circuit breaker: park the member outright instead of relying on an
		// ever-growing cooldown. The admin re-enables it from the model list
		// (toggle or clear-health); the next success resets fail_count.
		if _, err = tx.Exec(`UPDATE route_members SET fail_count=?, enabled=0, cooldown_until=NULL, last_error=?, updated_at=datetime('now') WHERE id=?`,
			nextFailures, category, id); err != nil {
			return fmt.Errorf("route member record failure disable: %w", err)
		}
	} else {
		until := now.Add(backoff).UTC().Format(time.RFC3339Nano)
		if _, err = tx.Exec(`UPDATE route_members SET fail_count=?, cooldown_until=?, last_error=?, updated_at=datetime('now') WHERE id=?`,
			nextFailures, until, category, id); err != nil {
			return fmt.Errorf("route member record failure: %w", err)
		}
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("route member record failure commit: %w", err)
	}
	return nil
}

// FailureCount returns the consecutive failure count for a member.
func (s *RouteMemberStore) FailureCount(id int64) (int, error) {
	var count int
	if err := s.db.QueryRow(`SELECT fail_count FROM route_members WHERE id = ?`, id).Scan(&count); err != nil {
		return 0, fmt.Errorf("route member failure count: %w", err)
	}
	return count, nil
}

func (s *RouteMemberStore) RecordSuccess(id int64) error {
	_, err := s.db.Exec(`UPDATE route_members SET fail_count=0, cooldown_until=NULL, last_error='', updated_at=datetime('now') WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("route member record success: %w", err)
	}
	return nil
}

// ClearHealth manually returns a route member to the eligible pool.
func (s *RouteMemberStore) ClearHealth(id int64) error {
	res, err := s.db.Exec(`UPDATE route_members SET fail_count=0, cooldown_until=NULL, last_error='', updated_at=datetime('now') WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("route member clear health: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("route member clear health rows: %w", err)
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// RecoverExpired returns members to a clean state after their penalty ends.
// The failure history is already preserved in proxy_logs, so an expired
// cooldown should not leave a stale red health state in the admin UI.
func (s *RouteMemberStore) RecoverExpired() error {
	_, err := s.db.Exec(`UPDATE route_members SET fail_count=0, cooldown_until=NULL, last_error='', updated_at=datetime('now') WHERE cooldown_until IS NOT NULL AND julianday(cooldown_until) <= julianday('now')`)
	if err != nil {
		return fmt.Errorf("route member recover expired: %w", err)
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

// findBestWildcardRoute selects the most specific enabled wildcard route for model.
func findBestWildcardRoute(db *sql.DB, model string) (*domain.Route, error) {
	rows, err := db.Query(`SELECT id, model_pattern, enabled, mapping_json, notes, created_at, updated_at FROM routes WHERE enabled = 1 AND (instr(model_pattern, '*') > 0 OR instr(model_pattern, '?') > 0)`)
	if err != nil {
		return nil, fmt.Errorf("route wildcard list: %w", err)
	}
	defer rows.Close()
	var matches []domain.Route
	for rows.Next() {
		var route domain.Route
		if err := scanRoute(rows, &route); err != nil {
			return nil, fmt.Errorf("route wildcard scan: %w", err)
		}
		if matchModelPattern(route.ModelPattern, model) {
			matches = append(matches, route)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		return nil, nil
	}
	sort.SliceStable(matches, func(i, j int) bool {
		left, right := matches[i], matches[j]
		if len(left.ModelPattern) != len(right.ModelPattern) {
			return len(left.ModelPattern) > len(right.ModelPattern)
		}
		return left.ID < right.ID
	})
	best := matches[0]
	return &best, nil
}

// matchModelPattern supports '*' (any run of runes) and '?' (single rune).
func matchModelPattern(pattern, model string) bool {
	pattern = strings.TrimSpace(pattern)
	model = strings.TrimSpace(model)
	if pattern == "" || (!strings.Contains(pattern, "*") && !strings.Contains(pattern, "?")) {
		return pattern == model
	}
	return matchModelPatternRunes([]rune(pattern), []rune(model))
}

func matchModelPatternRunes(pattern, model []rune) bool {
	if len(pattern) == 0 {
		return len(model) == 0
	}
	if pattern[0] == '*' {
		for consumed := 0; consumed <= len(model); consumed++ {
			if matchModelPatternRunes(pattern[1:], model[consumed:]) {
				return true
			}
		}
		return false
	}
	if len(model) == 0 {
		return false
	}
	if pattern[0] == '?' || pattern[0] == model[0] {
		return matchModelPatternRunes(pattern[1:], model[1:])
	}
	return false
}

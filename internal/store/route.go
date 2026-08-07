package store

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
)

const (
	// maxCooldownBackoff caps exponential cooldown growth so repeated failures
	// can never overflow time.Duration into a past timestamp. The member simply
	// stays parked until the penalty expires or an admin clears health.
	maxCooldownBackoff = 24 * time.Hour

	// disableAfterConsecutiveFailures is the default consecutive-failure
	// threshold that trips a circuit breaker: the member is disabled outright
	// and must be re-enabled by an admin (toggle or clear-health) before it is
	// tried again. Progressive mode (SetProgressiveCooldown) overrides this
	// with a configurable threshold.
	disableAfterConsecutiveFailures = 3
)

// RouteStore provides CRUD operations for routes.
type RouteStore struct {
	db *sql.DB
}

// RouteMemberStore provides CRUD operations for route members.
//
// Cooldown policy: by default members back off exponentially (2^n × base,
// capped) and are circuit-broken (disabled) after a consecutive-failure
// threshold. Progressive mode replaces exponential backoff with an explicit
// tier table [base, level1, level2, level3] and recovers one tier per success
// instead of clearing on the first success.
type RouteMemberStore struct {
	db *sql.DB

	// mu guards the progressive cooldown policy configuration.
	mu sync.RWMutex

	// progressive holds the tiered-cooldown configuration; nil = exponential
	// backoff with the legacy constants (default behavior).
	progressive *progressiveCooldown
}

// progressiveCooldown is the tiered failure/backoff policy.
type progressiveCooldown struct {
	base         time.Duration // fail 1 penalty
	levels       [3]time.Duration // fail 2 → levels[0], fail 3 → levels[1], fail 4 → levels[2]
	breakerCount int              // consecutive failures before disable (0 = legacy 3)
}

// SetProgressiveCooldown configures tiered cooldown with per-success decay.
// base is the fail-1 penalty; levels holds the 10m/1h/24h-style durations for
// the second, third, and fourth consecutive failures; breakerCount is the
// disable threshold (<=0 falls back to the legacy constant 3). Pass
// enabled=false to restore exponential backoff.
func (s *RouteMemberStore) SetProgressiveCooldown(enabled bool, base time.Duration, levels [3]time.Duration, breakerCount int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !enabled {
		s.progressive = nil
		return
	}
	s.progressive = &progressiveCooldown{base: base, levels: levels, breakerCount: breakerCount}
}

func scanRoute(scanner interface {
	Scan(dest ...any) error
}, r *domain.Route) error {
	var enabled int
	if err := scanner.Scan(&r.ID, &r.ModelPattern, &enabled, &r.RoutingMode, &r.MappingJSON, &r.Notes, scanTime(&r.CreatedAt), scanTime(&r.UpdatedAt)); err != nil {
		return err
	}
	r.Enabled = enabled != 0
	r.RoutingMode = domain.NormalizeRoutingMode(r.RoutingMode)
	return nil
}

func (s *RouteStore) List() ([]domain.Route, error) {
	rows, err := s.db.Query(`SELECT id, model_pattern, enabled, routing_mode, mapping_json, notes, created_at, updated_at FROM routes ORDER BY id`)
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
	row := s.db.QueryRow(`SELECT id, model_pattern, enabled, routing_mode, mapping_json, notes, created_at, updated_at FROM routes WHERE id = ?`, id)
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
	row := s.db.QueryRow(`SELECT id, model_pattern, enabled, routing_mode, mapping_json, notes, created_at, updated_at FROM routes WHERE model_pattern = ? AND enabled = 1 LIMIT 1`, model)
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
	res, err := s.db.Exec(`INSERT INTO routes (model_pattern, enabled, routing_mode, mapping_json, notes) VALUES (?, ?, ?, ?, ?)`,
		r.ModelPattern, enabled, domain.NormalizeRoutingMode(r.RoutingMode), r.MappingJSON, r.Notes)
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
	_, err := s.db.Exec(`UPDATE routes SET model_pattern=?, enabled=?, routing_mode=?, mapping_json=?, notes=?, updated_at=datetime('now') WHERE id=?`,
		r.ModelPattern, enabled, domain.NormalizeRoutingMode(r.RoutingMode), r.MappingJSON, r.Notes, r.ID)
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
		c.priority, c.weight, c.status, c.type_hint, c.header_override, c.system_prompt, c.retry_config, c.tags,
		c.stable_first, c.stable_first_requests, c.created_at, c.updated_at,
		CASE WHEN (
			cred.id IS NOT NULL AND cred.status = 'enabled' AND cred.secret_enc <> ''
			AND cred.site_id = c.site_id AND lower(cred.kind) = 'api_key'
		) OR EXISTS (
			SELECT 1 FROM credentials pool_cred
			WHERE pool_cred.site_id = c.site_id
			  AND pool_cred.status = 'enabled'
			  AND pool_cred.secret_enc <> ''
			  AND lower(pool_cred.kind) = 'api_key'
		) THEN 1 ELSE 0 END,
		COALESCE(rt.model_pattern, '')
		FROM route_members rm JOIN channels c ON c.id = rm.channel_id
		LEFT JOIN credentials cred ON cred.id = c.credential_id
		LEFT JOIN routes rt ON rt.id = rm.route_id
		WHERE rm.route_id = ? ORDER BY rm.priority DESC, rm.weight DESC, rm.id`, routeID)
	if err != nil {
		return nil, fmt.Errorf("route overview members: %w", err)
	}
	defer rows.Close()
	var result []domain.RoutingCandidate
	for rows.Next() {
		var candidate domain.RoutingCandidate
		var enabled, auto, manual, credentialUsable, stableFirst int
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
			&candidate.Channel.RetryConfig, &candidate.Channel.Tags,
			&stableFirst, &candidate.Channel.StableFirstRequests,
			scanTime(&candidate.Channel.CreatedAt), scanTime(&candidate.Channel.UpdatedAt),
			&credentialUsable, &candidate.ModelPattern,
		); err != nil {
			return nil, fmt.Errorf("route overview member scan: %w", err)
		}
		candidate.Member.Enabled = enabled != 0
		candidate.Member.Auto = auto != 0
		candidate.Member.ManualOverride = manual != 0
		candidate.CredentialUsable = credentialUsable != 0
		candidate.Channel.StableFirst = stableFirst != 0
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
	routeRow := s.db.QueryRow(`SELECT id, model_pattern, enabled, routing_mode, mapping_json, notes, created_at, updated_at FROM routes WHERE model_pattern = ? AND enabled = 1`, model)
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
		c.priority, c.weight, c.status, c.type_hint, c.header_override, c.system_prompt, c.retry_config, c.tags,
		c.stable_first, c.stable_first_requests, c.created_at, c.updated_at,
		CASE WHEN (
			cred.id IS NOT NULL AND cred.status = 'enabled' AND cred.secret_enc <> ''
			AND lower(cred.kind) = 'api_key'
		) OR EXISTS (
			SELECT 1 FROM credentials pool_cred
			WHERE pool_cred.site_id = c.site_id
			  AND pool_cred.status = 'enabled'
			  AND pool_cred.secret_enc <> ''
			  AND lower(pool_cred.kind) = 'api_key'
		) THEN 1 ELSE 0 END,
		COALESCE(rt.model_pattern, '')
		FROM route_members rm JOIN channels c ON c.id = rm.channel_id
		LEFT JOIN credentials cred ON cred.id = c.credential_id
		LEFT JOIN routes rt ON rt.id = rm.route_id
		WHERE rm.route_id = ?
		  AND (c.rate_limited_until IS NULL OR julianday(c.rate_limited_until) <= julianday('now'))
		ORDER BY rm.priority DESC, rm.id`, route.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("routing candidates: %w", err)
	}
	defer rows.Close()
	var result []domain.RoutingCandidate
	for rows.Next() {
		var candidate domain.RoutingCandidate
		var enabled, auto, manual, credentialUsable, stableFirst int
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
			&candidate.Channel.RetryConfig, &candidate.Channel.Tags,
			&stableFirst, &candidate.Channel.StableFirstRequests,
			scanTime(&candidate.Channel.CreatedAt), scanTime(&candidate.Channel.UpdatedAt),
			&credentialUsable, &candidate.ModelPattern,
		); err != nil {
			return nil, nil, fmt.Errorf("routing candidate scan: %w", err)
		}
		candidate.Member.Enabled = enabled != 0
		candidate.Member.Auto = auto != 0
		candidate.Member.ManualOverride = manual != 0
		candidate.CredentialUsable = credentialUsable != 0
		candidate.Channel.StableFirst = stableFirst != 0
		result = append(result, candidate)
	}
	if result == nil {
		result = []domain.RoutingCandidate{}
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

	policy := s.progressivePolicy()
	breaker := disableAfterConsecutiveFailures
	if policy != nil && policy.breakerCount > 0 {
		breaker = policy.breakerCount
	}
	backoff := cooldown
	if policy != nil {
		// Progressive tiers keep the caller's base as the fail-1 penalty only
		// when the caller passes it; the tier table covers fails 2-4.
		backoff = tieredBackoff(cooldown, nextFailures, policy.levels)
	} else {
		// Double per consecutive failure, capped so the cooldown can never grow
		// past maxCooldownBackoff and overflow time.Duration into a past timestamp.
		for step := 1; step < nextFailures; step++ {
			backoff *= 2
			if backoff > maxCooldownBackoff {
				backoff = maxCooldownBackoff
			}
		}
	}
	if backoff > maxCooldownBackoff {
		backoff = maxCooldownBackoff
	}
	if nextFailures >= breaker {
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

func (s *RouteMemberStore) RecordSuccess(id int64, now time.Time) error {
	policy := s.progressivePolicy()
	if policy == nil {
		_, err := s.db.Exec(`UPDATE route_members SET fail_count=0, cooldown_until=NULL, last_error='', updated_at=datetime('now') WHERE id=?`, id)
		if err != nil {
			return fmt.Errorf("route member record success: %w", err)
		}
		return nil
	}
	// Progressive recovery: step down one tier per success instead of clearing
	// the whole penalty on the first success (metapi-style decay).
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("route member record success begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var currentFailures int
	var cooldownUntil *time.Time
	if err = tx.QueryRow(`SELECT fail_count, cooldown_until FROM route_members WHERE id=?`, id).Scan(
		&currentFailures,
		scanNullTime(&cooldownUntil),
	); err != nil {
		return fmt.Errorf("route member record success lookup: %w", err)
	}
	if currentFailures <= 0 {
		_, err = tx.Exec(`UPDATE route_members SET fail_count=0, cooldown_until=NULL, last_error='', updated_at=datetime('now') WHERE id=?`, id)
	} else if currentFailures == 1 {
		_, err = tx.Exec(`UPDATE route_members SET fail_count=0, cooldown_until=NULL, last_error='', updated_at=datetime('now') WHERE id=?`, id)
	} else {
		nextFailures := currentFailures - 1
		backoff := tieredBackoff(policy.base, nextFailures, policy.levels)
		until := now.Add(backoff).UTC().Format(time.RFC3339Nano)
		_, err = tx.Exec(`UPDATE route_members SET fail_count=?, cooldown_until=?, last_error='', updated_at=datetime('now') WHERE id=?`,
			nextFailures, until, id)
	}
	if err != nil {
		return fmt.Errorf("route member record success: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("route member record success commit: %w", err)
	}
	return nil
}

// progressivePolicy returns the configured tiered policy, or nil for the
// legacy exponential backoff. Guarded by the store mutex.
func (s *RouteMemberStore) progressivePolicy() *progressiveCooldown {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.progressive
}

// tieredBackoff maps a consecutive-failure count to its cooldown tier:
// fail 1 → base, fail 2 → levels[0], fail 3 → levels[1], fail >= 4 → levels[2].
func tieredBackoff(base time.Duration, failures int, levels [3]time.Duration) time.Duration {
	switch {
	case failures <= 1:
		return base
	case failures == 2:
		return levels[0]
	case failures == 3:
		return levels[1]
	default:
		return levels[2]
	}
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
	rows, err := db.Query(`SELECT id, model_pattern, enabled, routing_mode, mapping_json, notes, created_at, updated_at FROM routes WHERE enabled = 1 AND (instr(model_pattern, '*') > 0 OR instr(model_pattern, '?') > 0)`)
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

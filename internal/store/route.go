package store

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
)

// sqlExecutor is the subset of *sql.DB and *sql.Tx used by store helpers.
// Helpers written against it run either standalone or inside a caller-owned
// transaction, which is what lets unification mutate routes, members and its
// own change log atomically.
type sqlExecutor interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

// RouteStore provides CRUD operations for routes.
type RouteStore struct {
	db *sql.DB
}

const routeSelectColumns = `id, model_pattern, enabled, routing_mode, mapping_json, notes,
	single_member_id, retry_times, channel_retry_times,
	max_reasoning_effort, max_concurrent, proxy_url, header_override, system_prompt,
	retry_config, payload_rules, stable_first, stable_first_denominator,
	stable_first_promote_requests, stable_first_requests, model_group, created_at, updated_at`

// RouteMemberStore provides CRUD operations for route members.
//
// Cooldown policy: each failed attempt parks the member for the configured
// fixed cooldown (COOLDOWN_SECONDS). The consecutive-failure count resets when
// the previous penalty expires, so a transient blip never escalates. Members
// are never permanently parked; isolation of a persistently failing channel is
// handled by the channel-level auto-disable + recovery probe instead.
type RouteMemberStore struct {
	db *sql.DB
}

func scanRoute(scanner interface {
	Scan(dest ...any) error
}, r *domain.Route) error {
	var enabled int
	var retryTimes, channelRetryTimes sql.NullInt64
	var singleMemberID sql.NullInt64
	var maxConcurrent, stableFirst, stableFirstDenominator, stableFirstPromote sql.NullInt64
	var stableFirstRequests int
	var maxReasoning, proxyURL, headerOverride, systemPrompt, retryConfig, payloadRules sql.NullString
	if err := scanner.Scan(&r.ID, &r.ModelPattern, &enabled, &r.RoutingMode, &r.MappingJSON, &r.Notes, &singleMemberID, &retryTimes, &channelRetryTimes,
		&maxReasoning, &maxConcurrent, &proxyURL, &headerOverride, &systemPrompt, &retryConfig, &payloadRules,
		&stableFirst, &stableFirstDenominator, &stableFirstPromote, &stableFirstRequests, &r.ModelGroup, scanTime(&r.CreatedAt), scanTime(&r.UpdatedAt)); err != nil {
		return err
	}
	r.Enabled = enabled != 0
	r.RoutingMode = domain.NormalizeRoutingMode(r.RoutingMode)
	if singleMemberID.Valid {
		v := singleMemberID.Int64
		r.SingleMemberID = &v
	}
	if retryTimes.Valid {
		v := int(retryTimes.Int64)
		r.RetryTimes = &v
	}
	if channelRetryTimes.Valid {
		v := int(channelRetryTimes.Int64)
		r.ChannelRetryTimes = &v
	}
	if maxReasoning.Valid {
		r.MaxReasoningEffort = &maxReasoning.String
	}
	if maxConcurrent.Valid {
		v := int(maxConcurrent.Int64)
		r.MaxConcurrent = &v
	}
	if proxyURL.Valid {
		r.ProxyURL = &proxyURL.String
	}
	if headerOverride.Valid {
		r.HeaderOverride = &headerOverride.String
	}
	if systemPrompt.Valid {
		r.SystemPrompt = &systemPrompt.String
	}
	if retryConfig.Valid {
		r.RetryConfig = &retryConfig.String
	}
	if payloadRules.Valid {
		r.PayloadRules = &payloadRules.String
	}
	if stableFirst.Valid {
		v := stableFirst.Int64 != 0
		r.StableFirst = &v
	}
	if stableFirstDenominator.Valid {
		v := int(stableFirstDenominator.Int64)
		r.StableFirstDenominator = &v
	}
	if stableFirstPromote.Valid {
		v := int(stableFirstPromote.Int64)
		r.StableFirstPromoteRequests = &v
	}
	r.StableFirstRequests = stableFirstRequests
	return nil
}

func (s *RouteStore) List() ([]domain.Route, error) {
	rows, err := s.db.Query(`SELECT ` + routeSelectColumns + ` FROM routes ORDER BY id`)
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

// ListEnabledPatterns returns the model_pattern of every enabled route.
func (s *RouteStore) ListEnabledPatterns() ([]string, error) {
	rows, err := s.db.Query(`SELECT model_pattern FROM routes WHERE enabled = 1`)
	if err != nil {
		return nil, fmt.Errorf("route enabled patterns: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var pattern string
		if err := rows.Scan(&pattern); err != nil {
			return nil, fmt.Errorf("route enabled patterns scan: %w", err)
		}
		out = append(out, pattern)
	}
	return out, rows.Err()
}

func (s *RouteStore) GetByID(id int64) (*domain.Route, error) {
	row := s.db.QueryRow(`SELECT `+routeSelectColumns+` FROM routes WHERE id = ?`, id)
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
	exact, err := getExactRoute(s.db, model, true)
	if err != nil {
		return nil, err
	}
	if exact != nil {
		return exact, nil
	}
	return findBestWildcardRoute(s.db, model)
}

// GetByModelAny looks up an exact route regardless of its enabled state.
// Anything that must not create a second row for a name that already exists
// needs this: a disabled route is invisible to GetByModel, so creating
// "because it is missing" would leave two rows with the same model_pattern and
// make ListEnabledPatterns return duplicates.
func (s *RouteStore) GetByModelAny(model string) (*domain.Route, error) {
	return getExactRoute(s.db, model, false)
}

// GetByModelAnyTx is the transactional form of GetByModelAny.
func (s *RouteStore) GetByModelAnyTx(tx *sql.Tx, model string) (*domain.Route, error) {
	return getExactRoute(tx, model, false)
}

func getExactRoute(ex sqlExecutor, model string, onlyEnabled bool) (*domain.Route, error) {
	query := `SELECT ` + routeSelectColumns + ` FROM routes WHERE model_pattern = ?`
	if onlyEnabled {
		query += ` AND enabled = 1`
	}
	var r domain.Route
	if err := scanRoute(ex.QueryRow(query+` LIMIT 1`, model), &r); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("route by model: %w", err)
	}
	return &r, nil
}

func (s *RouteStore) Create(r *domain.Route) (int64, error) {
	return createRoute(s.db, r)
}

// CreateTx is Create inside a caller-owned transaction.
func (s *RouteStore) CreateTx(tx *sql.Tx, r *domain.Route) (int64, error) {
	return createRoute(tx, r)
}

// SetEnabledTx flips a route's enabled flag inside a caller-owned transaction.
// Archiving — rather than deleting — is how unification retires a superseded
// name: the row, its members and every model-level override survive, so
// restoring is a single flag flip instead of a rebuild.
func (s *RouteStore) SetEnabledTx(tx *sql.Tx, id int64, enabled bool) error {
	if _, err := tx.Exec(`UPDATE routes SET enabled = ?, updated_at = datetime('now') WHERE id = ?`, boolInt(enabled), id); err != nil {
		return fmt.Errorf("route set enabled: %w", err)
	}
	return nil
}

func createRoute(ex sqlExecutor, r *domain.Route) (int64, error) {
	enabled := 0
	if r.Enabled {
		enabled = 1
	}
	res, err := ex.Exec(`INSERT INTO routes (model_pattern, enabled, routing_mode, mapping_json, notes, single_member_id, retry_times, channel_retry_times, max_reasoning_effort, max_concurrent, proxy_url, header_override, system_prompt, retry_config, payload_rules, stable_first, stable_first_denominator, stable_first_promote_requests, stable_first_requests, model_group) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ModelPattern, enabled, domain.NormalizeRoutingMode(r.RoutingMode), r.MappingJSON, r.Notes, nullableInt64Ptr(r.SingleMemberID), nullableInt(r.RetryTimes), nullableInt(r.ChannelRetryTimes), nullableString(r.MaxReasoningEffort), nullableInt(r.MaxConcurrent), nullableString(r.ProxyURL), nullableString(r.HeaderOverride), nullableString(r.SystemPrompt), nullableString(r.RetryConfig), nullableString(r.PayloadRules), nullableBool(r.StableFirst), nullableInt(r.StableFirstDenominator), nullableInt(r.StableFirstPromoteRequests), r.StableFirstRequests, strings.TrimSpace(r.ModelGroup))
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
	_, err := s.db.Exec(`UPDATE routes SET model_pattern=?, enabled=?, routing_mode=?, mapping_json=?, notes=?, single_member_id=?, retry_times=NULLIF(?, ''), channel_retry_times=NULLIF(?, ''), max_reasoning_effort=?, max_concurrent=?, proxy_url=?, header_override=?, system_prompt=?, retry_config=?, payload_rules=?, stable_first=?, stable_first_denominator=?, stable_first_promote_requests=?, stable_first_requests=CASE WHEN ? = 1 AND stable_first IS NOT 1 THEN 0 ELSE ? END, model_group=?, updated_at=datetime('now') WHERE id=?`,
		r.ModelPattern, enabled, domain.NormalizeRoutingMode(r.RoutingMode), r.MappingJSON, r.Notes, nullableInt64Ptr(r.SingleMemberID), nullableInt(r.RetryTimes), nullableInt(r.ChannelRetryTimes), nullableString(r.MaxReasoningEffort), nullableInt(r.MaxConcurrent), nullableString(r.ProxyURL), nullableString(r.HeaderOverride), nullableString(r.SystemPrompt), nullableString(r.RetryConfig), nullableString(r.PayloadRules), nullableBool(r.StableFirst), nullableInt(r.StableFirstDenominator), nullableInt(r.StableFirstPromoteRequests), boolInt(r.StableFirst != nil && *r.StableFirst), r.StableFirstRequests, strings.TrimSpace(r.ModelGroup), r.ID)
	if err != nil {
		return fmt.Errorf("route update: %w", err)
	}
	return nil
}

func (s *RouteStore) Delete(id int64) error {
	return deleteRoute(s.db, id)
}

// DeleteTx is Delete inside a caller-owned transaction. The route_members
// foreign key is ON DELETE CASCADE and foreign_keys is on, so members go with
// the route; the explicit delete below keeps the behaviour identical if the
// pragma is ever off.
func (s *RouteStore) DeleteTx(tx *sql.Tx, id int64) error {
	if _, err := tx.Exec(`DELETE FROM route_members WHERE route_id = ?`, id); err != nil {
		return fmt.Errorf("route delete members: %w", err)
	}
	return deleteRoute(tx, id)
}

func deleteRoute(ex sqlExecutor, id int64) error {
	if _, err := ex.Exec(`DELETE FROM routes WHERE id = ?`, id); err != nil {
		return fmt.Errorf("route delete: %w", err)
	}
	return nil
}

// RecordGraySuccess counts a successful relay for a model-level stable-first
// pool and atomically promotes the route when its threshold is reached.
func (s *RouteStore) RecordGraySuccess(id int64, threshold int) (promoted bool, err error) {
	if id <= 0 || threshold <= 0 {
		return false, nil
	}
	var count, stable int
	err = s.db.QueryRow(`
		UPDATE routes
		SET stable_first_requests = stable_first_requests + 1,
		    stable_first = CASE
		      WHEN stable_first_requests + 1 >= COALESCE(stable_first_promote_requests, ?)
		      THEN 0 ELSE stable_first END,
		    updated_at = datetime('now')
		WHERE id = ? AND stable_first = 1
		RETURNING stable_first_requests, stable_first`, threshold, id).Scan(&count, &stable)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("route gray success: %w", err)
	}
	return stable == 0 && count >= threshold, nil
}

func scanRouteMember(scanner interface {
	Scan(dest ...any) error
}, r *domain.RouteMember) error {
	var enabled, auto, manual, autoDisabled int
	if err := scanner.Scan(
		&r.ID,
		&r.RouteID,
		&r.ChannelID,
		&r.Priority,
		&r.Weight,
		&enabled,
		&auto,
		&manual,
		&autoDisabled,
		&r.MappingJSON,
		&r.GroupName,
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
	r.AutoDisabled = autoDisabled != 0
	return nil
}

func (s *RouteMemberStore) ListByRoute(routeID int64) ([]domain.RouteMember, error) {
	if err := s.RecoverExpired(); err != nil {
		return nil, err
	}
	return listRouteMembers(s.db, routeID)
}

// ListByRouteTx lists members inside a caller-owned transaction. It skips the
// expiry sweep ListByRoute does, since that would need a second connection.
func (s *RouteMemberStore) ListByRouteTx(tx *sql.Tx, routeID int64) ([]domain.RouteMember, error) {
	return listRouteMembers(tx, routeID)
}

func listRouteMembers(ex sqlExecutor, routeID int64) ([]domain.RouteMember, error) {
	rows, err := ex.Query(`SELECT id, route_id, channel_id, priority, weight, enabled, auto, manual_override, auto_disabled, mapping_json, group_name, fail_count, cooldown_until, last_error, created_at, updated_at FROM route_members WHERE route_id = ? ORDER BY priority DESC, weight DESC, id`, routeID)
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
		members, memberErr := s.listCandidatesByRoute(route)
		if memberErr != nil {
			return nil, memberErr
		}
		result = append(result, domain.RouteOverview{Route: route, Members: members})
	}
	return result, nil
}

func (s *RouteMemberStore) listCandidatesByRoute(route domain.Route) ([]domain.RoutingCandidate, error) {
	routeID := route.ID
	rows, err := s.db.Query(`SELECT
			rm.id, rm.route_id, rm.channel_id, rm.priority, rm.weight, rm.enabled, rm.auto, rm.manual_override, rm.auto_disabled,
			rm.mapping_json, rm.group_name, rm.fail_count, rm.cooldown_until, rm.last_error, rm.created_at, rm.updated_at,
		c.id, c.site_id, c.credential_id, c.name, c.base_url, c.models_csv, c.group_name,
    c.priority, c.weight, c.status, c.type_hint, c.max_reasoning_effort, c.payload_rules, c.max_concurrent, c.proxy_url, c.header_override, c.system_prompt, c.retry_config,
		c.stable_first, c.stable_first_requests, c.created_at, c.updated_at,
		CASE WHEN (
			cred.id IS NOT NULL AND cred.status = 'enabled' AND cred.secret_enc <> ''
			AND cred.site_id = c.site_id AND lower(cred.kind) IN ('api_key','session','access_token')
		) OR EXISTS (
			SELECT 1 FROM credentials pool_cred
			WHERE pool_cred.site_id = c.site_id
			  AND pool_cred.status = 'enabled'
			  AND pool_cred.secret_enc <> ''
			  AND lower(pool_cred.kind) IN ('api_key','session','access_token')
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
		var enabled, auto, manual, autoDisabled, credentialUsable, stableFirst int
		if err := rows.Scan(
			&candidate.Member.ID, &candidate.Member.RouteID, &candidate.Member.ChannelID,
			&candidate.Member.Priority, &candidate.Member.Weight, &enabled, &auto, &manual, &autoDisabled,
			&candidate.Member.MappingJSON, &candidate.Member.GroupName, &candidate.Member.FailCount, scanNullTime(&candidate.Member.CooldownUntil), &candidate.Member.LastError,
			scanTime(&candidate.Member.CreatedAt), scanTime(&candidate.Member.UpdatedAt),
			&candidate.Channel.ID, &candidate.Channel.SiteID, &candidate.Channel.CredentialID,
			&candidate.Channel.Name, &candidate.Channel.BaseURL, &candidate.Channel.ModelsCSV,
			&candidate.Channel.GroupName, &candidate.Channel.Priority, &candidate.Channel.Weight,
			&candidate.Channel.Status, &candidate.Channel.TypeHint,
			&candidate.Channel.MaxReasoningEffort,
			&candidate.Channel.PayloadRules,
			&candidate.Channel.MaxConcurrent,
			&candidate.Channel.ProxyURL,
			&candidate.Channel.HeaderOverride, &candidate.Channel.SystemPrompt,
			&candidate.Channel.RetryConfig,
			&stableFirst, &candidate.Channel.StableFirstRequests,
			scanTime(&candidate.Channel.CreatedAt), scanTime(&candidate.Channel.UpdatedAt),
			&credentialUsable, &candidate.ModelPattern,
		); err != nil {
			return nil, fmt.Errorf("route overview member scan: %w", err)
		}
		candidate.Member.Enabled = enabled != 0
		candidate.Member.Auto = auto != 0
		candidate.Member.ManualOverride = manual != 0
		candidate.Member.AutoDisabled = autoDisabled != 0
		candidate.CredentialUsable = credentialUsable != 0
		candidate.Channel.StableFirst = stableFirst != 0
		applyRouteModelOverrides(&candidate.Channel, route)
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

func applyRouteModelOverrides(channel *domain.Channel, route domain.Route) {
	if route.MaxReasoningEffort != nil {
		channel.MaxReasoningEffort = strings.TrimSpace(*route.MaxReasoningEffort)
	}
	if route.MaxConcurrent != nil {
		channel.MaxConcurrent = *route.MaxConcurrent
	}
	if route.ProxyURL != nil {
		channel.ProxyURL = strings.TrimSpace(*route.ProxyURL)
	}
	if route.HeaderOverride != nil {
		channel.HeaderOverride = strings.TrimSpace(*route.HeaderOverride)
	}
	if route.SystemPrompt != nil {
		channel.SystemPrompt = *route.SystemPrompt
	}
	if route.RetryConfig != nil {
		channel.RetryConfig = strings.TrimSpace(*route.RetryConfig)
	}
	if route.PayloadRules != nil {
		channel.PayloadRules = strings.TrimSpace(*route.PayloadRules)
	}
	// StableFirst is a route-level switch consumed by the selector. Channel
	// stable_first flags remain per-candidate so a model override does not turn
	// every member into a gray candidate.
}

// RoutingCandidates loads member and channel facts for the best matching enabled route.
// Exact model_pattern wins; otherwise the longest wildcard (* or ?) match is used.
// group narrows the pool to one route group: when non-empty and the route has
// members in that group they win, otherwise the 'default' group is used, and
// when that is empty too the full member list applies (legacy behavior).
func (s *RouteMemberStore) RoutingCandidates(model, group string) (*domain.Route, []domain.RoutingCandidate, error) {
	routeRow := s.db.QueryRow(`SELECT `+routeSelectColumns+` FROM routes WHERE model_pattern = ? AND enabled = 1`, model)
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
		rm.id, rm.route_id, rm.channel_id, rm.priority, rm.weight, rm.enabled, rm.auto, rm.manual_override, rm.auto_disabled,
		rm.mapping_json, rm.group_name, rm.fail_count, rm.cooldown_until, rm.last_error, rm.created_at, rm.updated_at,
		c.id, c.site_id, c.credential_id, c.name, c.base_url, c.models_csv, c.group_name,
    c.priority, c.weight, c.status, c.type_hint, c.max_reasoning_effort, c.payload_rules, c.max_concurrent, c.proxy_url, c.header_override, c.system_prompt, c.retry_config,
		c.stable_first, c.stable_first_requests, c.created_at, c.updated_at,
		CASE WHEN (
			cred.id IS NOT NULL AND cred.status = 'enabled' AND cred.secret_enc <> ''
			AND cred.site_id = c.site_id
			AND lower(cred.kind) IN ('api_key','session','access_token')
		) OR EXISTS (
			SELECT 1 FROM credentials pool_cred
			WHERE pool_cred.site_id = c.site_id
			  AND pool_cred.status = 'enabled'
			  AND pool_cred.secret_enc <> ''
			  AND lower(pool_cred.kind) IN ('api_key','session','access_token')
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
		var enabled, auto, manual, autoDisabled, credentialUsable, stableFirst int
		if err := rows.Scan(
			&candidate.Member.ID, &candidate.Member.RouteID, &candidate.Member.ChannelID,
			&candidate.Member.Priority, &candidate.Member.Weight, &enabled, &auto, &manual, &autoDisabled,
			&candidate.Member.MappingJSON, &candidate.Member.GroupName, &candidate.Member.FailCount, scanNullTime(&candidate.Member.CooldownUntil), &candidate.Member.LastError,
			scanTime(&candidate.Member.CreatedAt), scanTime(&candidate.Member.UpdatedAt),
			&candidate.Channel.ID, &candidate.Channel.SiteID, &candidate.Channel.CredentialID,
			&candidate.Channel.Name, &candidate.Channel.BaseURL, &candidate.Channel.ModelsCSV,
			&candidate.Channel.GroupName, &candidate.Channel.Priority, &candidate.Channel.Weight,
			&candidate.Channel.Status, &candidate.Channel.TypeHint,
			&candidate.Channel.MaxReasoningEffort,
			&candidate.Channel.PayloadRules,
			&candidate.Channel.MaxConcurrent,
			&candidate.Channel.ProxyURL,
			&candidate.Channel.HeaderOverride, &candidate.Channel.SystemPrompt,
			&candidate.Channel.RetryConfig,
			&stableFirst, &candidate.Channel.StableFirstRequests,
			scanTime(&candidate.Channel.CreatedAt), scanTime(&candidate.Channel.UpdatedAt),
			&credentialUsable, &candidate.ModelPattern,
		); err != nil {
			return nil, nil, fmt.Errorf("routing candidate scan: %w", err)
		}
		candidate.Member.Enabled = enabled != 0
		candidate.Member.Auto = auto != 0
		candidate.Member.ManualOverride = manual != 0
		candidate.Member.AutoDisabled = autoDisabled != 0
		candidate.CredentialUsable = credentialUsable != 0
		candidate.Channel.StableFirst = stableFirst != 0
		applyRouteModelOverrides(&candidate.Channel, route)
		result = append(result, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	result = filterCandidatesByGroup(result, group)
	if result == nil {
		result = []domain.RoutingCandidate{}
	}
	return &route, result, nil
}

// filterCandidatesByGroup narrows a route's candidate pool to one group:
// the requested group when the route defines it, else 'default', else the
// whole pool (legacy routes keep working unchanged).
func filterCandidatesByGroup(candidates []domain.RoutingCandidate, group string) []domain.RoutingCandidate {
	if group == "" {
		return candidates
	}
	membersOf := func(name string) []domain.RoutingCandidate {
		var out []domain.RoutingCandidate
		for _, candidate := range candidates {
			if candidate.Member.GroupName == name {
				out = append(out, candidate)
			}
		}
		return out
	}
	if filtered := membersOf(group); len(filtered) > 0 {
		return filtered
	}
	if filtered := membersOf(domain.DefaultRouteGroup); len(filtered) > 0 {
		return filtered
	}
	return candidates
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
	// Only consecutive failures within an active cooldown escalate the counter.
	// Once the previous penalty expires, start a fresh cooldown cycle — a
	// transient blip never accumulates into a long penalty.
	if cooldownUntil == nil || !cooldownUntil.After(now) {
		currentFailures = 0
	}
	nextFailures := currentFailures + 1
	until := now.Add(cooldown).UTC().Format(time.RFC3339Nano)
	if _, err = tx.Exec(`UPDATE route_members SET fail_count=?, cooldown_until=?, last_error=?, updated_at=datetime('now') WHERE id=?`,
		nextFailures, until, category, id); err != nil {
		return fmt.Errorf("route member record failure: %w", err)
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
	_, err := s.db.Exec(`UPDATE route_members SET fail_count=0, cooldown_until=NULL, last_error='', updated_at=datetime('now') WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("route member record success: %w", err)
	}
	return nil
}

// ClearHealth manually resets a route member's failure state so it is
// immediately eligible again.
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
	row := s.db.QueryRow(`SELECT id, route_id, channel_id, priority, weight, enabled, auto, manual_override, auto_disabled, mapping_json, group_name, fail_count, cooldown_until, last_error, created_at, updated_at FROM route_members WHERE id = ?`, id)
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
	return createRouteMember(s.db, r)
}

// CreateTx is Create inside a caller-owned transaction.
func (s *RouteMemberStore) CreateTx(tx *sql.Tx, r *domain.RouteMember) (int64, error) {
	return createRouteMember(tx, r)
}

func createRouteMember(ex sqlExecutor, r *domain.RouteMember) (int64, error) {
	enabled, auto, manual := boolInt(r.Enabled), boolInt(r.Auto), boolInt(r.ManualOverride)
	res, err := ex.Exec(`INSERT INTO route_members (route_id, channel_id, priority, weight, enabled, auto, manual_override, mapping_json, group_name) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.RouteID, r.ChannelID, r.Priority, r.Weight, enabled, auto, manual, r.MappingJSON, NormalizeMemberGroup(r.GroupName))
	if err != nil {
		return 0, fmt.Errorf("route member create: %w", err)
	}
	return res.LastInsertId()
}

// NormalizeMemberGroup trims a route member group name; empty becomes the
// built-in 'default' group.
func NormalizeMemberGroup(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return domain.DefaultRouteGroup
	}
	return name
}

// RenameMemberGroup moves every member of one group to another name.
func (s *RouteMemberStore) RenameMemberGroup(routeID int64, from, to string) (int, error) {
	from = NormalizeMemberGroup(from)
	to = NormalizeMemberGroup(to)
	if from == to {
		return 0, nil
	}
	res, err := s.db.Exec(`UPDATE route_members SET group_name = ?, updated_at = datetime('now') WHERE route_id = ? AND group_name = ?`, to, routeID, from)
	if err != nil {
		return 0, fmt.Errorf("route member group rename: %w", err)
	}
	changed, err := res.RowsAffected()
	return int(changed), err
}

// DeleteMemberGroup removes every member of one group of a route.
func (s *RouteMemberStore) DeleteMemberGroup(routeID int64, name string) (int, error) {
	res, err := s.db.Exec(`DELETE FROM route_members WHERE route_id = ? AND group_name = ?`, routeID, NormalizeMemberGroup(name))
	if err != nil {
		return 0, fmt.Errorf("route member group delete: %w", err)
	}
	changed, err := res.RowsAffected()
	return int(changed), err
}

// CopyMemberGroup clones every member of one group into another. Members whose
// channel already exists in the destination group are skipped (partial unique
// index on route_id+channel_id+group_name), and failure state is not carried
// over — the copy starts clean. Only non-alias members are copied.
func (s *RouteMemberStore) CopyMemberGroup(routeID int64, from, to string) (int, error) {
	from = NormalizeMemberGroup(from)
	to = NormalizeMemberGroup(to)
	if from == to {
		return 0, nil
	}
	res, err := s.db.Exec(`INSERT OR IGNORE INTO route_members (route_id, channel_id, priority, weight, enabled, auto, manual_override, mapping_json, group_name, fail_count, cooldown_until, last_error, created_at, updated_at) SELECT route_id, channel_id, priority, weight, enabled, auto, manual_override, mapping_json, ?, 0, NULL, '', datetime('now'), datetime('now') FROM route_members WHERE route_id = ? AND group_name = ? AND mapping_json = ''`,
		to, routeID, from)
	if err != nil {
		return 0, fmt.Errorf("route member group copy: %w", err)
	}
	changed, err := res.RowsAffected()
	return int(changed), err
}

// ListRouteGroupNames returns every distinct member group name across all
// routes, sorted, so callers can offer a pick list.
func (s *RouteMemberStore) ListRouteGroupNames() ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT group_name FROM route_members ORDER BY group_name`)
	if err != nil {
		return nil, fmt.Errorf("route member group names: %w", err)
	}
	defer rows.Close()
	names := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("route member group names scan: %w", err)
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

func (s *RouteMemberStore) Update(r *domain.RouteMember) error {
	enabled, auto, manual := boolInt(r.Enabled), boolInt(r.Auto), boolInt(r.ManualOverride)
	var cooldownUntil any
	if r.CooldownUntil != nil {
		cooldownUntil = r.CooldownUntil.UTC().Format(time.RFC3339Nano)
	}
	_, err := s.db.Exec(`UPDATE route_members SET priority=?, weight=?, enabled=?, auto=?, manual_override=?, mapping_json=?, group_name=?, fail_count=?, cooldown_until=?, last_error=?, updated_at=datetime('now') WHERE id=?`,
		r.Priority, r.Weight, enabled, auto, manual, r.MappingJSON, NormalizeMemberGroup(r.GroupName), r.FailCount, cooldownUntil, r.LastError, r.ID)
	if err != nil {
		return fmt.Errorf("route member update: %w", err)
	}
	return nil
}

// AutoDisableByPair disables every enabled member that serves this model on
// this channel and marks it auto-disabled, so recovery can later tell a member
// the probe turned off from one an operator turned off by hand.
//
// Members pinned as a route's single_member_id are skipped: taking one of those
// away would leave the model with no route at all, and a preventive system
// should not be able to make a model completely unavailable.
//
// It returns the number of members disabled.
func (s *RouteMemberStore) AutoDisableByPair(channelID int64, model, reason string) (int, error) {
	res, err := s.db.Exec(`UPDATE route_members SET enabled = 0, auto_disabled = 1,
			last_error = ?, updated_at = datetime('now')
		WHERE channel_id = ? AND enabled = 1
			AND id NOT IN (SELECT single_member_id FROM routes WHERE single_member_id IS NOT NULL)
			AND route_id IN (SELECT id FROM routes WHERE model_pattern = ? AND enabled = 1)`,
		reason, channelID, model)
	if err != nil {
		return 0, fmt.Errorf("route member auto disable: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("route member auto disable rows: %w", err)
	}
	return int(affected), nil
}

// AutoRecoverByPair re-enables members a probe previously disabled. The
// auto_disabled guard is what keeps this from resurrecting a member an
// operator disabled on purpose.
//
// It returns the number of members re-enabled.
func (s *RouteMemberStore) AutoRecoverByPair(channelID int64, model string) (int, error) {
	res, err := s.db.Exec(`UPDATE route_members SET enabled = 1, auto_disabled = 0,
			last_error = '', updated_at = datetime('now')
		WHERE channel_id = ? AND enabled = 0 AND auto_disabled = 1
			AND route_id IN (SELECT id FROM routes WHERE model_pattern = ? AND enabled = 1)`,
		channelID, model)
	if err != nil {
		return 0, fmt.Errorf("route member auto recover: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("route member auto recover rows: %w", err)
	}
	return int(affected), nil
}

// ApplyManualIntent records what an operator just did to a member, so the
// flag state matches their decision rather than whatever the probe last left
// behind. Enabling overrides the probe's negative verdict; disabling resets
// the health fields, keeping the channel-recovery invariant that a manually
// disabled member carries no fail_count mark and is therefore never
// resurrected by an automatic recovery.
func (s *RouteMemberStore) ApplyManualIntent(id int64, enabled bool) error {
	if enabled {
		if _, err := s.db.Exec(`UPDATE route_members SET auto_disabled = 0, updated_at = datetime('now') WHERE id = ?`, id); err != nil {
			return fmt.Errorf("route member manual enable: %w", err)
		}
		return nil
	}
	if _, err := s.db.Exec(`UPDATE route_members SET auto_disabled = 0, fail_count = 0, cooldown_until = NULL, last_error = '', updated_at = datetime('now') WHERE id = ?`, id); err != nil {
		return fmt.Errorf("route member manual disable: %w", err)
	}
	return nil
}

// CountAutoDisabled reports how many members are currently disabled by
// probing, for the settings panel to surface as an at-a-glance number.
func (s *RouteMemberStore) CountAutoDisabled() (int, error) {
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM route_members WHERE auto_disabled = 1`).Scan(&count); err != nil {
		return 0, fmt.Errorf("route member count auto disabled: %w", err)
	}
	return count, nil
}

func (s *RouteMemberStore) Delete(id int64) error {
	return deleteRouteMember(s.db, id)
}

// DeleteTx is Delete inside a caller-owned transaction.
func (s *RouteMemberStore) DeleteTx(tx *sql.Tx, id int64) error {
	return deleteRouteMember(tx, id)
}

func deleteRouteMember(ex sqlExecutor, id int64) error {
	// Deleting a pinned single-mode member exits that mode: the pin would be
	// dangling otherwise. auto is the safe fallback (full candidate pool).
	if _, err := ex.Exec(`UPDATE routes SET routing_mode='auto', single_member_id=NULL, updated_at=datetime('now') WHERE single_member_id = ?`, id); err != nil {
		return fmt.Errorf("route member delete unpin: %w", err)
	}
	if _, err := ex.Exec(`DELETE FROM route_members WHERE id = ?`, id); err != nil {
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

// nullableInt converts a *int override to a SQL parameter: nil becomes NULL
// (used with NULLIF(?, ”) so an unset override stays NULL = follow global).
func nullableInt(v *int) any {
	if v == nil {
		return nil
	}
	return *v
}

// nullableInt64Ptr converts a *int64 id to a SQL parameter; nil stays NULL.
func nullableInt64Ptr(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}

func nullableString(v *string) any {
	if v == nil {
		return nil
	}
	return *v
}

func nullableBool(v *bool) any {
	if v == nil {
		return nil
	}
	return boolInt(*v)
}

// findBestWildcardRoute selects the most specific enabled wildcard route for model.
func findBestWildcardRoute(db *sql.DB, model string) (*domain.Route, error) {
	rows, err := db.Query(`SELECT ` + routeSelectColumns + ` FROM routes WHERE enabled = 1 AND (instr(model_pattern, '*') > 0 OR instr(model_pattern, '?') > 0)`)
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
	// Dynamic programming keeps wildcard matching O(pattern*model). The old
	// recursive '*' branch explored every split and could become exponential
	// for attacker-controlled patterns such as *a*a*a*.
	matched := make([]bool, len(model)+1)
	matched[0] = true
	for _, token := range pattern {
		next := make([]bool, len(model)+1)
		if token == '*' {
			// '*' may consume zero characters (the old state) or extend a
			// previously matched prefix by one character.
			for index := 0; index <= len(model); index++ {
				next[index] = matched[index]
				if index > 0 && next[index-1] {
					next[index] = true
				}
			}
		} else {
			for index := 1; index <= len(model); index++ {
				if matched[index-1] && (token == '?' || token == model[index-1]) {
					next[index] = true
				}
			}
		}
		matched = next
	}
	return matched[len(model)]
}

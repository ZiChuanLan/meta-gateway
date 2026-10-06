package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/lan/meta-gateway/internal/domain"
)

var ErrModelAliasConflict = errors.New("model alias conflicts with existing bindings")

// SetChannelModelAlias moves existing bindings rather than deleting/recreating
// them. IDs, groups, prices, health and manual overrides belong to the upstream
// binding and must survive a change to its public name.
func (s *RouteMemberStore) SetChannelModelAlias(channelID int64, real, alias string) (int64, error) {
	real, alias = strings.TrimSpace(real), strings.TrimSpace(alias)
	if real == "" || alias == "" || len(real) > 256 || len(alias) > 256 || strings.ContainsAny(alias+real, "*?") {
		return 0, fmt.Errorf("invalid model alias")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var priority, weight int
	if err = tx.QueryRow(`SELECT priority, weight FROM channels WHERE id=?`, channelID).Scan(&priority, &weight); err != nil {
		return 0, err
	}
	type binding struct {
		id, route int64
		group     string
	}
	rows, err := tx.Query(`SELECT m.id,m.route_id,m.group_name,m.mapping_json,r.mapping_json,r.model_pattern
		FROM route_members m JOIN routes r ON r.id=m.route_id WHERE m.channel_id=? ORDER BY m.id`, channelID)
	if err != nil {
		return 0, err
	}
	var bindings []binding
	groups := map[string]bool{}
	for rows.Next() {
		var b binding
		var memberMap, routeMap, pattern string
		if err = rows.Scan(&b.id, &b.route, &b.group, &memberMap, &routeMap, &pattern); err != nil {
			rows.Close()
			return 0, err
		}
		model := domain.ResolveUpstreamModel(pattern, memberMap, routeMap)
		if model != real {
			continue
		}
		b.group = NormalizeMemberGroup(b.group)
		if groups[b.group] {
			rows.Close()
			return 0, fmt.Errorf("%w: duplicate upstream bindings in group %q; resolve them before renaming", ErrModelAliasConflict, b.group)
		}
		groups[b.group] = true
		bindings = append(bindings, b)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	var targetID int64
	err = tx.QueryRow(`SELECT id FROM routes WHERE model_pattern=?`, alias).Scan(&targetID)
	if err != nil && err != sql.ErrNoRows {
		return 0, err
	}
	moving := map[int64]bool{}
	sourceRoutes := map[int64]int{}
	for _, b := range bindings {
		moving[b.id] = true
		sourceRoutes[b.route]++
	}
	if targetID == 0 {
		route := domain.Route{ModelPattern: alias, Enabled: true}
		if len(bindings) > 0 {
			if err = scanRoute(tx.QueryRow(`SELECT `+routeSelectColumns+` FROM routes WHERE id=?`, bindings[0].route), &route); err != nil {
				return 0, err
			}
			var count int
			if err = tx.QueryRow(`SELECT count(*) FROM route_members WHERE route_id=?`, route.ID).Scan(&count); err != nil {
				return 0, err
			}
			if len(sourceRoutes) == 1 && count == len(bindings) {
				// The whole route moves; preserve route-level overrides and ID too.
				targetID = route.ID
				if _, err = tx.Exec(`UPDATE routes SET model_pattern=?,updated_at=datetime('now') WHERE id=?`, alias, targetID); err != nil {
					return 0, err
				}
			} else {
				route.ModelPattern = alias
				if route.SingleMemberID != nil && !moving[*route.SingleMemberID] {
					route.SingleMemberID = nil
					route.RoutingMode = domain.RoutingModeAuto
				}
			}
		}
		if targetID == 0 {
			targetID, err = createRoute(tx, &route)
			if err != nil {
				return 0, err
			}
		}
	}
	mapping, err := jsonMarshalRealName(real)
	if err != nil {
		return 0, err
	}
	// Explicit identity mappings also protect against a destination route's
	// historical route-level alias; the UI identifies aliases by public name.
	if len(bindings) == 0 {
		_, err = createRouteMember(tx, &domain.RouteMember{RouteID: targetID, ChannelID: channelID, Priority: priority, Weight: weight, Enabled: true, Auto: true, MappingJSON: mapping})
		if err != nil {
			return 0, err
		}
	}
	for routeID, countMoving := range sourceRoutes {
		if routeID == targetID {
			continue
		}
		var pin sql.NullInt64
		var count int
		if err = tx.QueryRow(`SELECT single_member_id,(SELECT count(*) FROM route_members WHERE route_id=routes.id) FROM routes WHERE id=?`, routeID).Scan(&pin, &count); err != nil {
			return 0, err
		}
		if pin.Valid && moving[pin.Int64] && count > countMoving {
			return 0, fmt.Errorf("%w: source route pins a moved member; change its routing policy first", ErrModelAliasConflict)
		}
	}
	for _, b := range bindings {
		if _, err = tx.Exec(`UPDATE route_members SET route_id=?,mapping_json=?,updated_at=datetime('now') WHERE id=?`, targetID, mapping, b.id); err != nil {
			return 0, err
		}
	}
	for routeID := range sourceRoutes {
		if routeID == targetID {
			continue
		}
		if _, err = tx.Exec(`DELETE FROM routes WHERE id=? AND NOT EXISTS(SELECT 1 FROM route_members WHERE route_id=?)`, routeID, routeID); err != nil {
			return 0, err
		}
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return targetID, nil
}

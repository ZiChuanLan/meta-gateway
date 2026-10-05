package store

import (
	"encoding/json"
	"errors"

	"github.com/lan/meta-gateway/internal/domain"
)

// ResolveTeamAccess deliberately bypasses the key cache. Mode, account and
// grants are checked for every new team request; changes cannot leave stale
// authorization in the legacy key cache.
func (s *DownstreamKeyStore) ResolveTeamAccess(key *domain.DownstreamKey) (*domain.TeamAccess, error) {
	if key.UserID == 0 {
		return nil, nil
	}
	var models, members string
	var all, canRoute int
	var canConfigure bool
	var preferencesJSON string
	a := &domain.TeamAccess{UserID: key.UserID, MemberIDs: make(map[int64]bool)}
	err := s.db.QueryRow(`SELECT p.models_json, p.members_json, p.all_models, p.rpm, p.allow_routing,p.allow_request_preferences,u.preferences_json,u.quota_total_tokens,u.quota_used_tokens,u.quota_total_cost,u.quota_used_cost
		FROM team_users u JOIN team_policies p ON p.id=u.policy_id JOIN team_settings s ON s.id=1
		WHERE u.id=? AND u.status='active' AND s.mode='team'`, key.UserID).
		Scan(&models, &members, &all, &a.RPM, &canRoute, &canConfigure, &preferencesJSON, &a.QuotaTotalTokens, &a.QuotaUsedTokens, &a.QuotaTotalCost, &a.QuotaUsedCost)
	if err != nil {
		return nil, errors.New("team access unavailable")
	}
	var ids []int64
	if json.Unmarshal([]byte(models), &a.Models) != nil || json.Unmarshal([]byte(members), &ids) != nil {
		return nil, errors.New("invalid team policy")
	}
	a.AllModels = all != 0
	if canConfigure {
		var preferences domain.UserRequestPreferences
		if json.Unmarshal([]byte(preferencesJSON), &preferences) != nil || !preferences.Valid() {
			return nil, errors.New("invalid user request preferences")
		}
		a.DisableFailover = preferences.Failover == "off"
		a.RetryLimit = preferences.MaxRetries
	}
	for _, id := range ids {
		a.MemberIDs[id] = true
	}
	planID := key.TeamPlanID
	if planID == 0 {
		// Every user has at most one default arrangement (partial unique index),
		// and keys that never chose a plan ride it. Its absence is the normal
		// case for a member who never rearranged anything, not an error.
		_ = s.db.QueryRow(`SELECT id FROM team_route_plans WHERE user_id=? AND is_default=1`, key.UserID).Scan(&planID)
	}
	if planID != 0 {
		if canRoute == 0 {
			return nil, errors.New("personal routing permission revoked")
		}
		var raw string
		if err := s.db.QueryRow(`SELECT members_json FROM team_route_plans WHERE id=? AND user_id=?`, planID, key.UserID).Scan(&raw); err != nil {
			if key.TeamPlanID == 0 {
				return a, nil
			}
			return nil, errors.New("route plan unavailable")
		}
		var arrangement domain.TeamRouteArrangement
		if json.Unmarshal([]byte(raw), &arrangement) != nil || !arrangement.Valid() {
			return nil, errors.New("invalid route plan")
		}
		a.Plan = make(map[int64]domain.TeamRouteMember)
		a.PlanModels = make(map[string]bool, len(arrangement.Routes))
		for model, members := range arrangement.Routes {
			if len(members) == 0 {
				continue
			}
			a.PlanModels[model] = true
			for index, member := range members {
				if member.Disabled || member.Weight <= 0 {
					continue
				}
				a.Plan[member.ID] = domain.TeamRouteMember{
					ID:       member.ID,
					Priority: domain.ArrangementPriority(index),
					Weight:   member.Weight,
				}
			}
		}
	}
	return a, nil
}

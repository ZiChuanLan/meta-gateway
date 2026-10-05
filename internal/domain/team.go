package domain

import "strings"

// TeamAccess is an immutable, request-scoped authorization snapshot.
// A nil snapshot means a legacy system key; empty grants on a team key deny all.
type TeamAccess struct {
	UserID    int64
	Models    []string
	AllModels bool
	MemberIDs map[int64]bool
	// Plan holds the member overrides from the key's route arrangement, and
	// PlanModels names the models that arrangement actually covers. A model
	// outside PlanModels keeps the site's own members and order — the user only
	// rearranged the models they touched.
	Plan            map[int64]TeamRouteMember
	PlanModels      map[string]bool
	RPM             int
	DisableFailover bool
	RetryLimit      *int
	// QuotaTotalTokens is the account-level credit pool in tokens (0 =
	// unlimited). It is a SECOND, independent limit next to the key's own
	// quota: a key says "this credential may spend X", the pool says "this
	// person may spend Y". Either one exhausted stops the request.
	QuotaTotalTokens int64
	QuotaUsedTokens  int64
	// QuotaTotalCost/QuotaUsedCost are the same pool counted in money. Both
	// ladders are enforced; neither replaces the other.
	QuotaTotalCost float64
	QuotaUsedCost  float64
}

// QuotaExceeded reports whether the account pool is spent. A nil snapshot (a
// legacy system key) has no pool and is never blocked by this one.
func (a *TeamAccess) QuotaExceeded() bool {
	if a == nil {
		return false
	}
	if a.QuotaTotalTokens > 0 && a.QuotaUsedTokens >= a.QuotaTotalTokens {
		return true
	}
	return a.QuotaTotalCost > 0 && a.QuotaUsedCost >= a.QuotaTotalCost
}

// TeamRouteArrangement is one plan: the models its owner arranged, each with
// the ordered upstream list they want. Array order is the failover order — the
// head is tried first — so no separate priority number is stored.
type TeamRouteArrangement struct {
	Routes map[string][]TeamRouteEntry `json:"routes"`
}

// TeamRouteEntry is one upstream inside an arrangement. Disabled keeps the row
// in the list (the user can see and re-enable it) while taking it out of
// routing; an absent field means enabled, so hand-written JSON cannot disable a
// member by omission.
type TeamRouteEntry struct {
	ID       int64 `json:"id"`
	Weight   int   `json:"weight"`
	Disabled bool  `json:"disabled,omitempty"`
}

// ArrangementPriority turns a position in the user's list into the priority the
// selector sorts by. The range sits far above any operator-set priority (0-100
// in the console) so a user's order is never interleaved with the site's.
func ArrangementPriority(index int) int {
	const base = 100000
	if index < 0 {
		index = 0
	}
	if index >= base {
		return 1
	}
	return base - index
}

func (a TeamRouteArrangement) Valid() bool {
	if len(a.Routes) > 500 {
		return false
	}
	for model, members := range a.Routes {
		if strings.TrimSpace(model) == "" || len(model) > 200 || len(members) > 200 {
			return false
		}
		seen := make(map[int64]bool, len(members))
		for _, member := range members {
			if member.ID <= 0 || seen[member.ID] || member.Weight < 1 || member.Weight > 1000 {
				return false
			}
			seen[member.ID] = true
		}
	}
	return true
}

// Arranged reports whether the user arranged anything for a model.
func (a *TeamAccess) Arranged(model string) bool {
	return a != nil && a.PlanModels[strings.TrimSpace(model)]
}

type UserRequestPreferences struct {
	Failover   string `json:"failover"`
	MaxRetries *int   `json:"max_retries"`
}

func (p UserRequestPreferences) Valid() bool {
	return (p.Failover == "" || p.Failover == "inherit" || p.Failover == "on" || p.Failover == "off") &&
		(p.MaxRetries == nil || (*p.MaxRetries >= 0 && *p.MaxRetries <= 100))
}

// LimitRetries can only narrow an already resolved site/model budget.
func (a *TeamAccess) LimitRetries(limit int) int {
	if a == nil {
		return limit
	}
	if a.DisableFailover {
		return 0
	}
	if a.RetryLimit != nil && *a.RetryLimit < limit {
		return *a.RetryLimit
	}
	return limit
}

type TeamRouteMember struct {
	ID       int64 `json:"id"`
	Priority int   `json:"priority"`
	Weight   int   `json:"weight"`
}

func (a *TeamAccess) AllowsModel(model string) bool {
	if a == nil || a.AllModels {
		return true
	}
	for _, allowed := range a.Models {
		if allowed == strings.TrimSpace(model) {
			return true
		}
	}
	return false
}

// AllowsGrant reports whether the policy granted this route member, ignoring
// any personal arrangement. It is the authorization half of AllowsMember, for
// the views that list what a user MAY arrange — an upstream they disabled has
// to stay visible in the editor, or they could never turn it back on.
func (a *TeamAccess) AllowsGrant(id int64) bool {
	return a == nil || a.MemberIDs[id]
}

// AllowsMember reports whether the model may run on this route member. A model
// the user never arranged runs on whatever the site granted; once they arrange
// it, only the upstreams they kept stay in play.
func (a *TeamAccess) AllowsMember(model string, id int64) bool {
	if a == nil {
		return true
	}
	if !a.AllowsGrant(id) {
		return false
	}
	if a.PlanModels == nil || !a.PlanModels[strings.TrimSpace(model)] {
		return true
	}
	_, ok := a.Plan[id]
	return ok
}

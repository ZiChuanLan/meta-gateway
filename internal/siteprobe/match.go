package siteprobe

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/lan/meta-gateway/internal/store"
)

// How a site's model name was tied to one of our routes. The kind is shown in
// the UI because a fuzzy hit must be reviewable before it can disable anything.
const (
	// MatchExact: the site's name equals a route pattern.
	MatchExact = "exact"
	// MatchPattern: the site's name matched a wildcard route pattern (gpt-*).
	MatchPattern = "pattern"
	// MatchMemberReal: a route member already maps this exact upstream name
	// (mapping_json.real), which is the authoritative link.
	MatchMemberReal = "member"
	// MatchNormalized: the names became equal after dropping a vendor prefix
	// (z-ai/glm-5.2 -> glm-5.2) or spaces.
	MatchNormalized = "normalized"
	// MatchNamespace: the names became equal after dropping the catalog
	// namespace (cn:glm-5.2 / global:glm-5.2 -> glm-5.2). Our model catalog names
	// every alias with a namespace, while a site's price table lists the bare
	// upstream name, so without this fold almost nothing ever matches.
	MatchNamespace = "namespace"
)

// MemberRef is one route member we may act on.
type MemberRef struct {
	MemberID     int64
	RouteID      int64
	Route        string
	ChannelID    int64
	ChannelName  string
	SiteID       int64
	GroupName    string
	Enabled      bool
	AutoDisabled bool
	RealName     string
	// HasPrice marks a member that already carries a price in the billing
	// layer, which decides whether adopting an observed price changes anything.
	HasPrice bool
	// SingleMember marks a route pinned to this one member: the store refuses to
	// auto-disable it, so the UI should say so instead of offering it.
	SingleMember bool
}

// Resolver ties a site's model names to route patterns and knows which members
// live on which site.
type Resolver struct {
	members  []MemberRef
	patterns []string
	// byMemberReal maps a member's exact upstream name (mapping_json.real) to its
	// route: that mapping is the authoritative link and outranks name folding.
	byMemberReal map[string]string
	// byMemberNormalized is the same index keyed by the folded name, used only
	// after the literal passes have failed.
	byMemberNormalized map[string]string
	// byMemberCore is the same index keyed by the name with both its vendor
	// prefix and its catalog namespace stripped.
	byMemberCore map[string]string
}

// BuildResolver reads routes and channels and returns the matching index.
func BuildResolver(db *store.DB) (*Resolver, error) {
	overviews, err := db.RouteMember.ListRouteOverviews()
	if err != nil {
		return nil, err
	}
	channels, err := db.Channel.List()
	if err != nil {
		return nil, err
	}
	names := make(map[int64]string, len(channels))
	for _, channel := range channels {
		names[channel.ID] = channel.Name
	}
	resolver := &Resolver{
		byMemberReal:       make(map[string]string),
		byMemberNormalized: make(map[string]string),
		byMemberCore:       make(map[string]string),
	}
	seenRoute := make(map[string]bool)
	for _, overview := range overviews {
		pattern := strings.TrimSpace(overview.Route.ModelPattern)
		if pattern == "" || !overview.Route.Enabled {
			continue
		}
		if !seenRoute[pattern] {
			seenRoute[pattern] = true
			resolver.patterns = append(resolver.patterns, pattern)
		}
		for _, candidate := range overview.Members {
			member := candidate.Member
			real := realNameOf(member.MappingJSON)
			siteID := int64(0)
			if candidate.Channel.SiteID != nil {
				siteID = *candidate.Channel.SiteID
			}
			singleMember := overview.Route.SingleMemberID != nil && *overview.Route.SingleMemberID == member.ID
			ref := MemberRef{
				MemberID:     member.ID,
				RouteID:      overview.Route.ID,
				Route:        pattern,
				ChannelID:    member.ChannelID,
				ChannelName:  names[member.ChannelID],
				SiteID:       siteID,
				GroupName:    member.GroupName,
				Enabled:      member.Enabled,
				AutoDisabled: member.AutoDisabled,
				RealName:     real,
				HasPrice: member.PricePromptPer1k > 0 || member.PriceCompletionPer1k > 0 ||
					member.PriceCachePer1k > 0 || member.PricePerRequest > 0,
				SingleMember: singleMember,
			}
			resolver.members = append(resolver.members, ref)
			if real != "" {
				resolver.byMemberReal[real] = pattern
				resolver.byMemberNormalized[normalizeModelName(real)] = pattern
				resolver.byMemberCore[coreModelName(real)] = pattern
			}
		}
	}
	sort.Strings(resolver.patterns)
	return resolver, nil
}

// realNameOf reads the {"real": "<upstream model name>"} mapping a route member
// carries when it forwards to a differently named upstream model.
func realNameOf(mappingJSON string) string {
	trimmed := strings.TrimSpace(mappingJSON)
	if trimmed == "" || trimmed == "{}" {
		return ""
	}
	var mapping struct {
		Real string `json:"real"`
	}
	if err := json.Unmarshal([]byte(trimmed), &mapping); err != nil {
		return ""
	}
	return strings.TrimSpace(mapping.Real)
}

// ModelMatch is one route a site's model name resolves to, with the evidence
// behind the hit.
type ModelMatch struct {
	Route string
	Kind  string
}

// Match resolves a site's model name to the best route pattern. It is the first
// entry of MatchAll, which is what callers that can only show one row want.
// RoutesFor returns the routes this site serves, i.e. those with at least one
// member on it. It is what makes a row exist for a model a site serves while
// publishing nothing about it — the case where an operator most needs the
// catalog's reference price.
func (r *Resolver) RoutesFor(siteID int64) []string {
	seen := map[string]bool{}
	out := make([]string, 0, 8)
	for _, member := range r.members {
		if member.SiteID != siteID || seen[member.Route] {
			continue
		}
		seen[member.Route] = true
		out = append(out, member.Route)
	}
	sort.Strings(out)
	return out
}

func (r *Resolver) Match(raw string) (route string, kind string, ok bool) {
	matches := r.MatchAll(raw)
	if len(matches) == 0 {
		return "", "", false
	}
	return matches[0].Route, matches[0].Kind, true
}

// MatchAll resolves a site's model name to every route pattern it can serve.
//
// More than one is normal in this repo: the catalog publishes namespace aliases
// (cn:glm-5.2 and global:glm-5.2) that a site's price table lists once, as the
// bare upstream name. Each alias is a real route with its own members, so the
// caller gets one match per alias instead of an arbitrary winner.
func (r *Resolver) MatchAll(raw string) []ModelMatch {
	name := strings.TrimSpace(raw)
	if name == "" {
		return nil
	}
	strategy := []struct {
		kind  string
		has   func(pattern string) bool
		index map[string]string
	}{
		// 1. the site calls the model exactly what we do
		{MatchExact, func(pattern string) bool { return name == pattern }, nil},
		// 2. a wildcard route pattern (gpt-*)
		{MatchPattern, func(pattern string) bool {
			if !strings.HasSuffix(pattern, "*") {
				return false
			}
			prefix := strings.TrimSuffix(pattern, "*")
			return prefix != "" && strings.HasPrefix(name, prefix)
		}, nil},
		// 3. a member already maps this exact upstream name
		{MatchMemberReal, nil, r.byMemberReal},
		// 4. same name after dropping a vendor prefix / spaces / case
		{MatchNormalized, func(pattern string) bool {
			return patternMatchesNormalized(pattern, normalizeModelName(name))
		}, r.byMemberNormalized},
		// 5. same name after also dropping the catalog namespace
		{MatchNamespace, func(pattern string) bool {
			return patternMatchesCore(pattern, coreModelName(name))
		}, r.byMemberCore},
	}
	out := []ModelMatch{}
	seen := map[string]bool{}
	for _, step := range strategy {
		var hits []string
		if step.has != nil {
			for _, pattern := range r.patterns {
				if step.has(pattern) {
					hits = append(hits, pattern)
				}
			}
		} else if step.index != nil {
			if route, found := step.index[nameOf(raw, step.kind)]; found {
				hits = append(hits, route)
			}
		}
		for _, route := range hits {
			if seen[route] {
				continue
			}
			seen[route] = true
			out = append(out, ModelMatch{Route: route, Kind: step.kind})
		}
	}
	return out
}

// nameOf picks the key an index lookup by kind needs.
func nameOf(raw, kind string) string {
	name := strings.TrimSpace(raw)
	switch kind {
	case MatchMemberReal:
		return name
	case MatchNormalized:
		return normalizeModelName(name)
	default:
		return coreModelName(name)
	}
}

// patternMatchesNormalized compares a route pattern with an already normalized
// model name, honoring a trailing wildcard. It does NOT fold the catalog
// namespace: that is a separate, weaker pass (see patternMatchesCore).
func patternMatchesNormalized(pattern, normalized string) bool {
	folded := normalizeModelName(pattern)
	if strings.HasSuffix(folded, "*") {
		prefix := strings.TrimSuffix(folded, "*")
		return prefix != "" && strings.HasPrefix(normalized, prefix)
	}
	return folded == normalized
}

// patternMatchesCore compares a route pattern with an already core-folded model
// name, i.e. one that has had both its vendor prefix and its catalog namespace
// stripped.
func patternMatchesCore(pattern, core string) bool {
	folded := coreModelName(pattern)
	if strings.HasSuffix(folded, "*") {
		prefix := strings.TrimSuffix(folded, "*")
		return prefix != "" && strings.HasPrefix(core, prefix)
	}
	return folded == core
}

// MembersFor returns the members of a route that live on one site.
func (r *Resolver) MembersFor(route string, siteID int64) []MemberRef {
	var out []MemberRef
	for _, member := range r.members {
		if member.Route != route {
			continue
		}
		if siteID > 0 && member.SiteID != siteID {
			continue
		}
		out = append(out, member)
	}
	return out
}

// normalizeModelName folds the differences that never distinguish two models:
// case, surrounding spaces, spaces inside the name, and a vendor prefix. It
// deliberately leaves '.', '-' and '_' alone: this repo treats "5-5" and "5.5"
// as different models, and a fold here would silently disable the wrong member.
func normalizeModelName(name string) string {
	trimmed := strings.ToLower(strings.TrimSpace(name))
	trimmed = strings.ReplaceAll(trimmed, " ", "")
	if index := strings.LastIndex(trimmed, "/"); index >= 0 && index+1 < len(trimmed) {
		trimmed = trimmed[index+1:]
	}
	return trimmed
}

// coreModelName also drops the catalog namespace our routes carry
// (cn:glm-5.2, global:gpt-5.5) so a site's bare upstream name can still line up.
//
// The colon is only treated as a namespace when what follows it is non-empty and
// what precedes it looks like a label rather than a URL scheme (no dot, no
// slash): "cn:glm-5.2" folds, while a hypothetical "http://…" or "1.5:2" does not.
func coreModelName(name string) string {
	folded := normalizeModelName(name)
	index := strings.LastIndex(folded, ":")
	if index < 0 || index+1 >= len(folded) {
		return folded
	}
	prefix := folded[:index]
	if prefix == "" || strings.ContainsAny(prefix, "./") {
		return folded
	}
	return folded[index+1:]
}

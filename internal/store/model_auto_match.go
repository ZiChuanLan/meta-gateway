package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/lan/meta-gateway/internal/domain"
)

// ModelChannelMatch is one enabled channel whose model list (models_csv or
// the discovery snapshot) carries a model matching a route pattern.
type ModelChannelMatch struct {
	ChannelID   int64  `json:"channel_id"`
	ChannelName string `json:"channel_name"`
	// Source says where the match was seen: "models_csv" | "discovered" (the
	// channel's own lists) | "routed" (a name this gateway already serves
	// through the channel — an alias, or the upstream name behind one).
	Source string `json:"source"`
	// Model is the name a member attached for this match must FORWARD. It is the
	// upstream's own name: the matched one for a sibling or a substring hit, and
	// the renamed real name when the match was found through an alias. The
	// console shows it so the operator can see WHY a channel qualified.
	Model string `json:"model,omitempty"`
}

// ModelMatchMode selects how a route pattern is matched against a channel's
// model list.
type ModelMatchMode string

const (
	// ModelMatchExact keeps the pattern as written: a plain name matches that
	// name, and a "*" in it matches by wildcard — the same semantics routing
	// itself uses.
	ModelMatchExact ModelMatchMode = "exact"
	// ModelMatchRelated also matches the pattern's "-…" siblings. An operator
	// routing "mimo-v2.5" normally wants the channels that serve
	// "mimo-v2.5-flash" too, and a channel listing the sibling very often does
	// not list the bare name at all.
	ModelMatchRelated ModelMatchMode = "related"
	// ModelMatchContains accepts any model whose name CONTAINS the pattern, at
	// any position: "deepseek" finds "deepseek-ai/deepseek-v4-flash",
	// "cn:deepseek-r1" and "deepseek-chat". The operator knows the family, not
	// the vendor's spelling of it — a real catalog prefixes, namespaces and
	// renames — which is why a prefix rule alone keeps missing channels the
	// operator can see listed in the console.
	ModelMatchContains ModelMatchMode = "contains"
)

// ParseModelMatchMode normalizes a console value. Anything unknown means
// exact, which is the conservative reading: a widening match rewrites the
// upstream model name, so it must never happen by accident.
func ParseModelMatchMode(raw string) ModelMatchMode {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(ModelMatchRelated):
		return ModelMatchRelated
	case string(ModelMatchContains):
		return ModelMatchContains
	default:
		return ModelMatchExact
	}
}

// expressions lists the matchers for the mode. Each widening mode adds one
// wildcard form, so a single matcher implementation serves all three.
func (m ModelMatchMode) expressions(pattern string) []string {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return nil
	}
	if strings.ContainsAny(pattern, "*?") {
		// The operator wrote the pattern; every mode uses it as written rather
		// than stacking a second wildcard on top.
		return []string{pattern}
	}
	switch m {
	case ModelMatchRelated:
		// Prefix, not "-suffix": real catalogs spell variants as
		// "mimo-v2.5-flash", "cn:glm-5.1", "mimo-v2.5turbo" — the part the
		// operator knows is the stem. Nothing here changes what the ROUTE
		// matches (the route name stays as typed), only which channels the
		// attach action considers, and the preview names the entry it found so
		// a surprising hit can be unticked before saving.
		return []string{pattern, pattern + "*"}
	case ModelMatchContains:
		return []string{pattern, "*" + pattern + "*"}
	default:
		return []string{pattern}
	}
}

// matchCandidate is one name a channel is known to serve, with the name a
// member attached for it must forward.
//
// The two differ exactly when the console found the channel under a name its
// upstream does not use: a sibling model, a substring hit, or an alias this
// gateway renamed.
type matchCandidate struct {
	Name    string
	Forward string
	Source  string
}

// pickMatch returns the candidate a channel matched, preferring the pattern
// itself (expression index 0) over a widening: a channel serving both names
// must keep forwarding the name the operator routed. Ties keep the earlier
// candidate, which is why callers append the channel's own list first.
func pickMatch(candidates []matchCandidate, expressions []string) (matchCandidate, bool) {
	var best matchCandidate
	bestExact := false
	for _, candidate := range candidates {
		for index, expression := range expressions {
			if !MatchModelPattern(expression, candidate.Name) {
				continue
			}
			exact := index == 0
			switch {
			case best.Name == "":
			case exact && !bestExact:
			case exact == bestExact && candidate.Name < best.Name:
			default:
				continue
			}
			best, bestExact = candidate, exact
			break
		}
	}
	return best, best.Name != ""
}

// forward is the name the upstream is told.
func (m matchCandidate) forward() string {
	if strings.TrimSpace(m.Forward) == "" {
		return m.Name
	}
	return m.Forward
}

// ChannelsWithModel returns every enabled channel able to serve a route
// pattern: models_csv and the discovery snapshot are unioned per channel and
// matched with the same pattern semantics routing uses (a plain name matches
// exactly, "gpt-*" matches by wildcard). Manual-sync channels are included —
// attaching one here is the operator's explicit adoption decision. A channel
// is reported once, preferring the models_csv source.
func (s *DB) ChannelsWithModel(pattern string) ([]ModelChannelMatch, error) {
	return s.ChannelsMatchingModel(pattern, ModelMatchExact)
}

// ChannelsMatchingModel returns every enabled channel able to serve a route
// pattern under one match mode, with the model name that matched. The
// channel's own models_csv, its discovery snapshot and the names this gateway
// already serves through it (routes and their aliases) are unioned per channel
// and matched with the same pattern semantics routing uses (a plain name
// matches exactly, "gpt-*" matches by wildcard). Manual-sync channels are
// included — attaching one here is the operator's explicit adoption decision.
// A channel is reported once.
//
// The routed names are what makes a RENAMED model findable: after unifying
// "deepseek-ai/deepseek-v4-flash" into "deepseek-v4-flash", the channel's own
// list still says the upstream name, so searching the name the operator now
// uses would otherwise miss the channel that is already serving it. Either name
// finds the channel; the member a match produces forwards the upstream one.
func (s *DB) ChannelsMatchingModel(pattern string, mode ModelMatchMode) ([]ModelChannelMatch, error) {
	expressions := mode.expressions(pattern)
	if len(expressions) == 0 {
		return nil, nil
	}
	channels, err := s.Channel.List()
	if err != nil {
		return nil, fmt.Errorf("channels with model channels: %w", err)
	}
	discovered, err := s.DiscoveredModel.List(nil)
	if err != nil {
		return nil, fmt.Errorf("channels with model discovery: %w", err)
	}
	routed, err := s.routedModelNames()
	if err != nil {
		return nil, err
	}

	// Only the candidates that match need to survive: the picker below ranks
	// candidates, it does not need the whole snapshot.
	discoveredByChannel := map[int64][]matchCandidate{}
	for _, model := range discovered {
		for _, expression := range expressions {
			if MatchModelPattern(expression, model.ModelName) {
				discoveredByChannel[model.ChannelID] = append(discoveredByChannel[model.ChannelID], matchCandidate{
					Name:   model.ModelName,
					Source: "discovered",
				})
				break
			}
		}
	}

	out := make([]ModelChannelMatch, 0, len(discoveredByChannel)+len(routed))
	for _, channel := range channels {
		if channel.Status != domain.StatusEnabled {
			continue
		}
		// Candidate order is preference order for equal matches (see pickMatch):
		// the channel's own list is the upstream truth, an alias is this
		// gateway's own invention.
		candidates := make([]matchCandidate, 0, 4)
		for _, name := range splitCSV(channel.ModelsCSV) {
			candidates = append(candidates, matchCandidate{Name: name, Source: "models_csv"})
		}
		candidates = append(candidates, discoveredByChannel[channel.ID]...)
		candidates = append(candidates, routed[channel.ID]...)
		if len(candidates) == 0 {
			continue
		}
		match, ok := pickMatch(candidates, expressions)
		if !ok {
			continue
		}
		out = append(out, ModelChannelMatch{
			ChannelID:   channel.ID,
			ChannelName: channel.Name,
			Source:      match.Source,
			Model:       match.forward(),
		})
	}
	return out, nil
}

// routedModelNames lists, per channel, the client-facing names it already
// serves and the upstream name it forwards for them. The forward name is the
// member's own alias rewrite, falling back to the route's (the legacy
// route-level mapping) and then to the route pattern itself.
func (s *DB) routedModelNames() (map[int64][]matchCandidate, error) {
	rows, err := s.Query(`SELECT rm.channel_id, r.model_pattern,
			COALESCE(NULLIF(CASE WHEN json_valid(rm.mapping_json) THEN json_extract(rm.mapping_json,'$.real') END,''),''),
			COALESCE(NULLIF(CASE WHEN json_valid(r.mapping_json) THEN json_extract(r.mapping_json,'$.real') END,''),'')
		FROM route_members rm JOIN routes r ON r.id = rm.route_id
		WHERE rm.enabled = 1 AND r.enabled = 1`)
	if err != nil {
		return nil, fmt.Errorf("routed model names: %w", err)
	}
	defer rows.Close()
	out := map[int64][]matchCandidate{}
	for rows.Next() {
		var channelID int64
		var pattern, memberReal, routeReal string
		if err := rows.Scan(&channelID, &pattern, &memberReal, &routeReal); err != nil {
			return nil, fmt.Errorf("routed model name scan: %w", err)
		}
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		forward := strings.TrimSpace(memberReal)
		if forward == "" {
			forward = strings.TrimSpace(routeReal)
		}
		out[channelID] = append(out[channelID], matchCandidate{
			Name:    pattern,
			Forward: forward,
			Source:  "routed",
		})
	}
	return out, rows.Err()
}

// CreateRouteWithAutoMatch creates the route and attaches a default-group
// member (enabled, auto, priority 0 / weight 100 — the same shape Reconcile
// and the channel models panel use) for every channel in matchChannelIDs that
// ChannelsWithModel reports for the pattern, in one transaction, so the route
// never becomes visible half-wired. The intersection guards against a stale
// client selection: ids of channels that are disabled or no longer serve the
// model are skipped.
//
// With ids present, a route that already carries the pattern is reused rather
// than rejected — wiring channels into the existing (often empty) route is the
// whole point of the request, and matches how unification reuses canonical
// routes. Members the route already has are not duplicated. A bare create
// (no ids) stays strict and still fails on a duplicate pattern. Returns the
// route id and how many members were attached.
func (s *DB) CreateRouteWithAutoMatch(rt *domain.Route, matchChannelIDs []int64, mode ModelMatchMode) (int64, int, error) {
	// The match is computed BEFORE the transaction opens. It reads the catalog,
	// and a read of the route tables while this transaction is writing them can
	// only wait for its own writer — the console would see it as "saving a route
	// never returns". It also keeps the write lock as short as the insert.
	var matches []ModelChannelMatch
	if len(matchChannelIDs) > 0 {
		found, err := s.ChannelsMatchingModel(rt.ModelPattern, mode)
		if err != nil {
			return 0, 0, err
		}
		matches = found
	}

	tx, err := s.Begin()
	if err != nil {
		return 0, 0, fmt.Errorf("route auto match begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	routes := &RouteStore{db: s.DB}
	routeID := int64(0)
	if len(matchChannelIDs) > 0 {
		existing, err := routes.GetByModelAnyTx(tx, rt.ModelPattern)
		if err != nil {
			return 0, 0, err
		}
		if existing != nil {
			routeID = existing.ID
			if rt.Enabled && !existing.Enabled {
				if err := routes.SetEnabledTx(tx, routeID, true); err != nil {
					return 0, 0, err
				}
			}
		}
	}
	if routeID == 0 {
		var err error
		routeID, err = routes.CreateTx(tx, rt)
		if err != nil {
			return 0, 0, err
		}
	}

	attached := 0
	if len(matchChannelIDs) > 0 {
		attached, _, err = s.attachMatchesTx(tx, routeID, matchChannelIDs, matches, domain.DefaultRouteGroup, rt.ModelPattern)
		if err != nil {
			return 0, 0, err
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("route auto match commit: %w", err)
	}
	return routeID, attached, nil
}

// AttachChannelsToRoute wires channels into one of an existing route's member
// groups ('default' when the name is empty). It is the console's "add every
// channel that serves this model" action, and it applies the same intersection
// as CreateRouteWithAutoMatch — only channels that are enabled and verifiably
// serve the pattern are attached — so a stale console selection can never
// invent a member. Channels already in the target group are left alone; a
// channel that only exists in another group is still attached, because groups
// are additive (a key bound to one group must not silently miss a channel the
// operator explicitly attached elsewhere).
//
// Returns how many members were created and how many requested channels the
// intersection rejected (disabled, unknown, or no longer serving the model).
// Channels the route already serves in the target group are neither attached
// nor counted as skipped — re-running is a plain no-op, so a double click
// cannot double-attach or look like a failure. An empty request writes nothing.
func (s *DB) AttachChannelsToRoute(routeID int64, pattern string, mode ModelMatchMode, channelIDs []int64, group string) (int, int, error) {
	if routeID <= 0 {
		return 0, 0, errors.New("route id required")
	}
	if len(channelIDs) == 0 {
		return 0, 0, nil
	}
	matches, err := s.ChannelsMatchingModel(pattern, mode)
	if err != nil {
		return 0, 0, err
	}
	tx, err := s.Begin()
	if err != nil {
		return 0, 0, fmt.Errorf("attach channels begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	attached, skipped, err := s.attachMatchesTx(tx, routeID, channelIDs, matches, group, pattern)
	if err != nil {
		return 0, 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("attach channels commit: %w", err)
	}
	return attached, skipped, nil
}

// attachMatchesTx creates members in one group for the requested channels that
// both appear in matches and are not already in that group. Route creation and
// the console's attach action share it so the two can never drift on what
// counts as a match.
func (s *DB) attachMatchesTx(tx *sql.Tx, routeID int64, matchChannelIDs []int64, matches []ModelChannelMatch, group, pattern string) (attached, skipped int, err error) {
	group = NormalizeMemberGroup(group)
	selected := make(map[int64]struct{}, len(matchChannelIDs))
	for _, id := range matchChannelIDs {
		selected[id] = struct{}{}
	}
	// Skip channels the route already serves in the target group.
	current, err := (&RouteMemberStore{db: s.DB}).ListByRouteTx(tx, routeID)
	if err != nil {
		return 0, 0, err
	}
	for _, member := range current {
		if NormalizeMemberGroup(member.GroupName) == group {
			delete(selected, member.ChannelID)
		}
	}
	requested := len(selected)
	members := &RouteMemberStore{db: s.DB}
	for _, match := range matches {
		if _, ok := selected[match.ChannelID]; !ok {
			continue
		}
		if _, err := members.CreateTx(tx, &domain.RouteMember{
			RouteID:     routeID,
			ChannelID:   match.ChannelID,
			Priority:    0,
			Weight:      100,
			Enabled:     true,
			Auto:        true,
			GroupName:   group,
			MappingJSON: forwardingMapping(pattern, match.Model),
		}); err != nil {
			return 0, 0, err
		}
		attached++
	}
	return attached, requested - attached, nil
}

// forwardingMapping returns the member's {"real": …} redirect when the match
// attached the channel under a DIFFERENT name than the route's — a sibling, a
// substring hit, or the upstream name behind an alias the gateway renamed.
// Without it the member would forward the route name ("deepseek") to an
// upstream that only ever listed the real one: an attached member that cannot
// answer.
//
// Two cases keep the route name untouched. A match on the name itself has
// nothing to redirect. A pattern with a wildcard is not a name the upstream is
// ever asked for — the client's concrete model passes through — so pinning it
// to whichever name happened to match would break every other request through
// that route.
func forwardingMapping(pattern, matched string) string {
	pattern = strings.TrimSpace(pattern)
	if matched == "" || matched == pattern || strings.ContainsAny(pattern, "*?") {
		return ""
	}
	encoded, err := json.Marshal(map[string]string{"real": matched})
	if err != nil {
		return ""
	}
	return string(encoded)
}

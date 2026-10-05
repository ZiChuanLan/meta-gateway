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
	Source      string `json:"source"` // "models_csv" | "discovered"
	// Model is the name that matched. It is what the member will forward as
	// when the match was a sibling ("mimo-v2.5-flash" for a route named
	// "mimo-v2.5"), and it is what the console shows so the operator can see
	// WHY a channel qualified.
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
)

// ParseModelMatchMode normalizes a console value. Anything unknown means
// exact, which is the conservative reading: related attachment rewrites the
// upstream model name, so it must never happen by accident.
func ParseModelMatchMode(raw string) ModelMatchMode {
	if strings.EqualFold(strings.TrimSpace(raw), string(ModelMatchRelated)) {
		return ModelMatchRelated
	}
	return ModelMatchExact
}

// expressions lists the matchers for the mode. Related adds the prefix
// wildcard, so one matcher implementation serves both.
func (m ModelMatchMode) expressions(pattern string) []string {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return nil
	}
	if m == ModelMatchRelated && !strings.ContainsAny(pattern, "*?") {
		// Prefix, not "-suffix": real catalogs spell variants as
		// "mimo-v2.5-flash", "cn:glm-5.1", "mimo-v2.5turbo" — the part the
		// operator knows is the stem. Nothing here changes what the ROUTE
		// matches (the route name stays as typed), only which channels the
		// attach action considers, and the preview names the entry it found so
		// a surprising hit can be unticked before saving.
		return []string{pattern, pattern + "*"}
	}
	return []string{pattern}
}

// pickMatch returns the model name a channel's list matched, preferring the
// pattern itself over a sibling: a channel serving both names must keep
// forwarding the name the operator routed.
func pickMatch(models []string, expressions []string) (string, bool) {
	best, bestExact := "", false
	for _, model := range models {
		for index, expression := range expressions {
			if !MatchModelPattern(expression, model) {
				continue
			}
			exact := index == 0
			switch {
			case best == "":
			case exact && !bestExact:
			case exact == bestExact && model < best:
			default:
				continue
			}
			best, bestExact = model, exact
			break
		}
	}
	return best, best != ""
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
// pattern under one match mode, with the model name that matched. models_csv
// and the discovery snapshot are unioned per channel and matched with the same
// pattern semantics routing uses (a plain name matches exactly, "gpt-*"
// matches by wildcard). Manual-sync channels are included — attaching one here
// is the operator's explicit adoption decision. A channel is reported once,
// preferring the models_csv source.
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

	// Only the matching discovered names need to survive: the picker below
	// ranks candidates, it does not need the whole snapshot.
	discoveredByChannel := map[int64][]string{}
	for _, model := range discovered {
		for _, expression := range expressions {
			if MatchModelPattern(expression, model.ModelName) {
				discoveredByChannel[model.ChannelID] = append(discoveredByChannel[model.ChannelID], model.ModelName)
				break
			}
		}
	}

	out := make([]ModelChannelMatch, 0, len(discoveredByChannel))
	for _, channel := range channels {
		if channel.Status != domain.StatusEnabled {
			continue
		}
		if model, ok := pickMatch(splitCSV(channel.ModelsCSV), expressions); ok {
			out = append(out, ModelChannelMatch{
				ChannelID:   channel.ID,
				ChannelName: channel.Name,
				Source:      "models_csv",
				Model:       model,
			})
			continue
		}
		if model, ok := pickMatch(discoveredByChannel[channel.ID], expressions); ok {
			out = append(out, ModelChannelMatch{
				ChannelID:   channel.ID,
				ChannelName: channel.Name,
				Source:      "discovered",
				Model:       model,
			})
		}
	}
	return out, nil
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
		matches, err := s.ChannelsMatchingModel(rt.ModelPattern, mode)
		if err != nil {
			return 0, 0, err
		}
		attached, _, err = s.attachMatchesTx(tx, routeID, matchChannelIDs, matches, domain.DefaultRouteGroup, rt.ModelPattern, mode)
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

	attached, skipped, err := s.attachMatchesTx(tx, routeID, channelIDs, matches, group, pattern, mode)
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
func (s *DB) attachMatchesTx(tx *sql.Tx, routeID int64, matchChannelIDs []int64, matches []ModelChannelMatch, group, pattern string, mode ModelMatchMode) (attached, skipped int, err error) {
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
			MappingJSON: forwardingMapping(pattern, mode, match.Model),
		}); err != nil {
			return 0, 0, err
		}
		attached++
	}
	return attached, requested - attached, nil
}

// forwardingMapping returns the member's {"real": …} redirect when a related
// match attached a channel under a DIFFERENT model name. Without it the member
// would forward the route name ("mimo-v2.5") to an upstream that only ever
// listed the sibling ("mimo-v2.5-flash") — an attached member that cannot
// answer. An exact match, or a wildcard pattern the operator wrote, keeps the
// previous behavior and forwards the route name unchanged.
func forwardingMapping(pattern string, mode ModelMatchMode, matched string) string {
	if mode != ModelMatchRelated || matched == "" {
		return ""
	}
	if matched == strings.TrimSpace(pattern) {
		return ""
	}
	encoded, err := json.Marshal(map[string]string{"real": matched})
	if err != nil {
		return ""
	}
	return string(encoded)
}

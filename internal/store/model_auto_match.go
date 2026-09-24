package store

import (
	"database/sql"
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
}

// ChannelsWithModel returns every enabled channel able to serve a route
// pattern: models_csv and the discovery snapshot are unioned per channel and
// matched with the same pattern semantics routing uses (a plain name matches
// exactly, "gpt-*" matches by wildcard). Manual-sync channels are included —
// attaching one here is the operator's explicit adoption decision. A channel
// is reported once, preferring the models_csv source.
func (s *DB) ChannelsWithModel(pattern string) ([]ModelChannelMatch, error) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
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

	matchedDiscovered := map[int64]struct{}{}
	for _, model := range discovered {
		if MatchModelPattern(pattern, model.ModelName) {
			matchedDiscovered[model.ChannelID] = struct{}{}
		}
	}

	out := make([]ModelChannelMatch, 0, len(matchedDiscovered))
	for _, channel := range channels {
		if channel.Status != domain.StatusEnabled {
			continue
		}
		source := ""
		for _, model := range splitCSV(channel.ModelsCSV) {
			if MatchModelPattern(pattern, model) {
				source = "models_csv"
				break
			}
		}
		if source == "" {
			if _, ok := matchedDiscovered[channel.ID]; ok {
				source = "discovered"
			}
		}
		if source == "" {
			continue
		}
		out = append(out, ModelChannelMatch{
			ChannelID:   channel.ID,
			ChannelName: channel.Name,
			Source:      source,
		})
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
func (s *DB) CreateRouteWithAutoMatch(rt *domain.Route, matchChannelIDs []int64) (int64, int, error) {
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
		matches, err := s.ChannelsWithModel(rt.ModelPattern)
		if err != nil {
			return 0, 0, err
		}
		attached, _, err = s.attachMatchesTx(tx, routeID, matchChannelIDs, matches, domain.DefaultRouteGroup)
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
func (s *DB) AttachChannelsToRoute(routeID int64, pattern string, channelIDs []int64, group string) (int, int, error) {
	if routeID <= 0 {
		return 0, 0, errors.New("route id required")
	}
	if len(channelIDs) == 0 {
		return 0, 0, nil
	}
	matches, err := s.ChannelsWithModel(pattern)
	if err != nil {
		return 0, 0, err
	}
	tx, err := s.Begin()
	if err != nil {
		return 0, 0, fmt.Errorf("attach channels begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	attached, skipped, err := s.attachMatchesTx(tx, routeID, channelIDs, matches, group)
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
func (s *DB) attachMatchesTx(tx *sql.Tx, routeID int64, matchChannelIDs []int64, matches []ModelChannelMatch, group string) (attached, skipped int, err error) {
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
			RouteID:   routeID,
			ChannelID: match.ChannelID,
			Priority:  0,
			Weight:    100,
			Enabled:   true,
			Auto:      true,
			GroupName: group,
		}); err != nil {
			return 0, 0, err
		}
		attached++
	}
	return attached, requested - attached, nil
}

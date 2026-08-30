package probe

import (
	"sort"

	"github.com/lan/meta-gateway/internal/store"
)

// CandidatePairs builds the probe targets from live route members, so "probe
// everything" means exactly the models the gateway actually serves.
//
// This lives here rather than in the HTTP handler because the scheduled run
// needs the identical selection: if the two ever drifted, a cron-driven probe
// could disable a member that the UI never showed as in scope.
//
// An empty channel or model filter means "all of them".
func CandidatePairs(db *store.DB, channelIDs []int64, models []string) ([]Pair, error) {
	overviews, err := db.RouteMember.ListRouteOverviews()
	if err != nil {
		return nil, err
	}
	wantChannels := make(map[int64]bool, len(channelIDs))
	for _, id := range channelIDs {
		wantChannels[id] = true
	}
	wantModels := make(map[string]bool, len(models))
	for _, name := range models {
		wantModels[name] = true
	}

	seen := make(map[Pair]bool)
	var pairs []Pair
	for _, overview := range overviews {
		pattern := overview.Route.ModelPattern
		if pattern == "" || !overview.Route.Enabled {
			continue
		}
		if len(wantModels) > 0 && !wantModels[pattern] {
			continue
		}
		for _, candidate := range overview.Members {
			member := candidate.Member
			// Probe-disabled members stay in scope: the probe is the only thing
			// that can re-enable them, so excluding enabled=0 pairs would turn
			// every auto-disable into a dead end. Members an operator disabled
			// by hand (auto_disabled=0) remain excluded.
			if !member.Enabled && !member.AutoDisabled {
				continue
			}
			if len(wantChannels) > 0 && !wantChannels[member.ChannelID] {
				continue
			}
			pair := Pair{ChannelID: member.ChannelID, Model: pattern}
			if seen[pair] {
				continue
			}
			seen[pair] = true
			pairs = append(pairs, pair)
		}
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].Model != pairs[j].Model {
			return pairs[i].Model < pairs[j].Model
		}
		return pairs[i].ChannelID < pairs[j].ChannelID
	})
	return pairs, nil
}

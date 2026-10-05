package siteprobe

import (
	"context"
	"sort"
)

// AutoApplyRound applies the policy for every site whose config asks for it.
//
// This is the unattended half of the feature and it is off unless an operator
// turned it on for that particular site (SourceConfig.AutoApply). Sites that
// only want the evidence are skipped, so "collect and show" stays the default
// even while other sites act on their own.
//
// Sites are grouped by their policy and evaluated once per group: the report
// rebuilds the whole route/member index, so evaluating per site would repeat
// that work for no benefit. Groups are applied in a stable order so two runs
// over the same data produce the same sequence.
func (s *Service) AutoApplyRound(ctx context.Context) ([]Action, error) {
	return s.autoApplySites(ctx, nil)
}

// AutoApplySites is the manual-collection boundary: an empty selection is a
// no-op, never an instruction to apply changes to the whole fleet.
func (s *Service) AutoApplySites(ctx context.Context, ids []int64) ([]Action, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	selected := make(map[int64]bool, len(ids))
	for _, id := range ids {
		selected[id] = true
	}
	return s.autoApplySites(ctx, selected)
}

func (s *Service) autoApplySites(ctx context.Context, selected map[int64]bool) ([]Action, error) {
	sites, err := s.EnabledSites()
	if err != nil {
		return nil, err
	}
	type group struct {
		policy Policy
		ids    []int64
	}
	groups := map[string]*group{}
	for _, site := range sites {
		if selected != nil && !selected[site.ID] {
			continue
		}
		config, ok := ParseSourceConfig(site.ProbeSourceConfig)
		if !ok || !config.AutoApply {
			continue
		}
		key, err := EncodeSourceConfig(SourceConfig{AutoApply: true, Policy: config.Policy})
		if err != nil {
			continue
		}
		entry, exists := groups[key]
		if !exists {
			entry = &group{policy: config.Policy}
			groups[key] = entry
		}
		entry.ids = append(entry.ids, site.ID)
	}
	if len(groups) == 0 {
		return nil, nil
	}

	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var applied []Action
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return applied, err
		}
		entry := groups[key]
		actions, err := s.Apply(ctx, ApplyRequest{SiteIDs: entry.ids, Policy: entry.policy})
		if err != nil {
			return applied, err
		}
		for _, action := range actions {
			if action.MembersMoved == 0 {
				// A skip is not a failure (a single-member route must never be
				// parked); it is logged so the operator can see why nothing
				// happened.
				s.logger.Info("site probe: auto-apply skipped",
					"route", action.Route, "site", action.SiteName, "reason", action.Skipped)
				continue
			}
			s.logger.Info("site probe: auto-applied",
				"kind", action.Kind, "route", action.Route, "site", action.SiteName,
				"channel", action.ChannelName, "moved", action.MembersMoved)
			applied = append(applied, action)
		}
	}
	return applied, nil
}

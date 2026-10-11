package siteprobe

import (
	"fmt"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
	"sort"
	"strings"
	"time"
)

type Observation struct {
	ID           string                    `json:"id"`
	SiteID       int64                     `json:"site_id"`
	RawModel     string                    `json:"raw_model"`
	MonitorID    string                    `json:"monitor_id,omitempty"`
	GroupName    string                    `json:"group_name,omitempty"`
	Price        store.SiteProbePrice      `json:"price"`
	ObservedAt   time.Time                 `json:"observed_at"`
	Series       *store.AvailabilitySeries `json:"series"`
	WeakEvidence bool                      `json:"weak_evidence"`
}
type Observations struct {
	Items []Observation `json:"items"`
	Sites []SiteStatus  `json:"sites"`
	Since time.Time     `json:"since"`
	Until time.Time     `json:"until"`
}

func (s *Service) Observations(hours int) (*Observations, error) {
	if hours <= 0 {
		hours = 24
	}
	until := s.now().UTC().Truncate(time.Second).Add(time.Second)
	since := until.Add(-time.Duration(hours) * time.Hour)
	result := &Observations{Items: []Observation{}, Sites: []SiteStatus{}, Since: since, Until: until}
	sites, err := s.db.Site.List()
	if err != nil {
		return nil, err
	}
	samples, err := s.db.LatestSiteObservations()
	if err != nil {
		return nil, err
	}
	for _, site := range sites {
		resolved := resolveProbeSource(site)
		status := SiteStatus{SiteID: site.ID, SiteName: site.Name, Kind: resolved.ProbeSourceKind, URL: resolved.ProbeSourceURL,
			Enabled: site.ProbeSourceEnabled, LastRunAt: site.ProbeLastRunAt, LastError: site.ProbeLastError,
			Config: site.ProbeSourceConfig, Policy: DefaultPolicy()}
		if cfg, ok := ParseSourceConfig(site.ProbeSourceConfig); ok {
			status.AutoApply = cfg.AutoApply
			status.Policy = cfg.Policy
		}
		if run, err := s.db.LatestSiteProbeRun(site.ID); err != nil {
			return nil, err
		} else if run != nil {
			status.LastStatus = run.Status
			status.MonitorCount = run.MonitorCount
		}
		result.Sites = append(result.Sites, status)
		beats, err := s.db.SiteHeartbeats(site.ID, sourceIdentity(site), since)
		if err != nil {
			return nil, err
		}
		byMonitor := map[string]*store.AvailabilitySeries{}
		for _, beat := range beats {
			series := byMonitor[beat.MonitorID]
			if series == nil {
				series = store.NewAvailabilitySeries("site", "site_model", since, until)
				byMonitor[beat.MonitorID] = series
			}
			outcome := "ignored"
			if beat.Status == 1 {
				outcome = "ok"
			} else if beat.Status == 0 {
				outcome = "failed"
			}
			series.Observe(beat.At, outcome, "", until)
		}
		for _, sample := range samples[site.ID] {
			// A changed source has no observations until its first successful collection.
			if sample.SourceKey != "" && sample.SourceKey != sourceIdentity(site) {
				continue
			}
			series := byMonitor[sample.MonitorID]
			if series == nil {
				series = store.NewAvailabilitySeries("site", "site_model", since, until)
				if sample.RatioKnown || sample.Samples > 0 {
					ratio := sample.Ratio
					series.Ratio = &ratio
					series.Samples = sample.Samples
					series.OK = sample.UpCount
					series.Failed = sample.Samples - sample.UpCount
					at := sample.ObservedAt
					series.LastAt = &at
				}
			}
			series.Stale = sample.ObservedAt.IsZero() || until.Sub(sample.ObservedAt) > StaleAfter
			result.Items = append(result.Items, Observation{ID: fmt.Sprintf("%d/%s/%s/%s", site.ID, sample.RawModel, sample.GroupName, sample.MonitorID),
				SiteID: site.ID, RawModel: sample.RawModel, MonitorID: sample.MonitorID, GroupName: sample.GroupName,
				Price: sample.Price, ObservedAt: sample.ObservedAt, Series: series, WeakEvidence: sample.WeakEvidence})
		}
	}
	return result, nil
}

type AvailabilityItem struct {
	MemberID  int64                       `json:"member_id,omitempty"`
	RouteID   int64                       `json:"route_id,omitempty"`
	ChannelID int64                       `json:"channel_id,omitempty"`
	SiteID    int64                       `json:"site_id,omitempty"`
	Model     string                      `json:"model"`
	Primary   *store.AvailabilitySeries   `json:"primary"`
	Evidence  []*store.AvailabilitySeries `json:"evidence"`
	Ambiguous bool                        `json:"ambiguous,omitempty"`
}
type AvailabilityReport struct {
	Members []AvailabilityItem `json:"members"`
	Models  []AvailabilityItem `json:"models"`
	Routes  []AvailabilityItem `json:"routes"`
	Since   time.Time          `json:"since"`
	Until   time.Time          `json:"until"`
}

func (s *Service) Availability(routeIDs []int64, channelID int64, group string, hours int) (*AvailabilityReport, error) {
	observations, err := s.Observations(hours)
	if err != nil {
		return nil, err
	}
	since, until := observations.Since, observations.Until
	traffic, channelTraffic, err := s.db.AvailabilityTraffic(since, until, routeIDs, channelID)
	if err != nil {
		return nil, err
	}
	probes, channelProbes, err := s.db.AvailabilityProbes(since, until, channelID)
	if err != nil {
		return nil, err
	}
	requests, err := s.db.AvailabilityRoutes(since, until, routeIDs, group)
	if err != nil {
		return nil, err
	}
	overviews, err := s.db.RouteMember.ListRouteOverviews()
	if err != nil {
		return nil, err
	}
	external, err := s.externalIndex()
	if err != nil {
		return nil, err
	}
	report := &AvailabilityReport{Members: []AvailabilityItem{}, Models: []AvailabilityItem{}, Routes: []AvailabilityItem{}, Since: since, Until: until}
	wanted := map[int64]bool{}
	for _, id := range routeIDs {
		wanted[id] = true
	}
	for _, overview := range overviews {
		if len(wanted) > 0 && !wanted[overview.Route.ID] {
			continue
		}
		route := AvailabilityItem{RouteID: overview.Route.ID, Model: overview.Route.ModelPattern, Evidence: []*store.AvailabilitySeries{}}
		route.Primary = requests[route.RouteID]
		if route.Primary != nil {
			route.Evidence = append(route.Evidence, route.Primary)
		}
		report.Routes = append(report.Routes, route)
		for _, candidate := range overview.Members {
			if channelID > 0 && candidate.Channel.ID != channelID {
				continue
			}
			if group != "" && candidate.Member.GroupName != group {
				continue
			}
			real := domain.ResolveUpstreamModel(overview.Route.ModelPattern, candidate.Member.MappingJSON, overview.Route.MappingJSON)
			item := AvailabilityItem{MemberID: candidate.Member.ID, RouteID: overview.Route.ID, ChannelID: candidate.Channel.ID,
				Model: real, Evidence: []*store.AvailabilitySeries{}}
			if candidate.Channel.SiteID != nil {
				item.SiteID = *candidate.Channel.SiteID
			}
			for _, series := range []*store.AvailabilitySeries{traffic[store.MemberAvailabilityKey(item.MemberID, real)], probes[store.MemberAvailabilityKey(item.MemberID, real)]} {
				if series != nil {
					item.Evidence = append(item.Evidence, series)
				}
			}
			addSiteEvidence(&item, observations, external, since, until)
			choosePrimary(&item)
			report.Members = append(report.Members, item)
		}
	}
	if channelID > 0 {
		ch, err := s.db.Channel.GetByID(channelID)
		if err != nil {
			return nil, err
		}
		if ch != nil {
			names := map[string]bool{}
			for _, name := range strings.Split(ch.ModelsCSV, ",") {
				if name = strings.TrimSpace(name); name != "" {
					names[name] = true
				}
			}
			discovered, err := s.db.DiscoveredModel.List(&channelID)
			if err != nil {
				return nil, err
			}
			for _, m := range discovered {
				names[m.ModelName] = true
			}
			for _, m := range report.Members {
				names[m.Model] = true
			}
			order := []string{}
			for name := range names {
				order = append(order, name)
			}
			sort.Strings(order)
			for _, name := range order {
				item := AvailabilityItem{ChannelID: channelID, Model: name, Evidence: []*store.AvailabilitySeries{}}
				if ch.SiteID != nil {
					item.SiteID = *ch.SiteID
				}
				key := store.TrafficKey(channelID, name)
				for _, series := range []*store.AvailabilitySeries{channelTraffic[key], channelProbes[key]} {
					if series != nil {
						item.Evidence = append(item.Evidence, series)
					}
				}
				addSiteEvidence(&item, observations, external, since, until)
				choosePrimary(&item)
				report.Models = append(report.Models, item)
			}
		}
	}
	return report, nil
}
func addSiteEvidence(item *AvailabilityItem, observations *Observations, external map[string]ExternalReading, since, until time.Time) {
	exact, folded := []Observation{}, []Observation{}
	for _, o := range observations.Items {
		if o.SiteID != item.SiteID {
			continue
		}
		if strings.TrimSpace(o.RawModel) == strings.TrimSpace(item.Model) {
			exact = append(exact, o)
		} else if coreModelName(o.RawModel) == coreModelName(item.Model) {
			folded = append(folded, o)
		}
	}
	matches := exact
	if len(matches) == 0 {
		matches = folded
	}
	if len(matches) == 1 {
		item.Evidence = append(item.Evidence, matches[0].Series)
	} else if len(matches) > 1 {
		item.Ambiguous = true
	}
	if reading := externalFor(item.SiteID, item.Model, external); reading != nil {
		series := store.NewAvailabilitySeries("watchbot", "site_model", since, until)
		ratio := reading.Ratio
		series.Ratio = &ratio
		series.Stale = true
		if at, err := time.Parse(time.RFC3339, reading.ObservedAt); err == nil {
			series.LastAt = &at
			series.Stale = until.Sub(at) > StaleAfter
		}
		item.Evidence = append(item.Evidence, series)
	}
}
func choosePrimary(item *AvailabilityItem) {
	for _, s := range item.Evidence {
		if s.Ratio != nil && !s.Stale {
			item.Primary = s
			return
		}
	}
}

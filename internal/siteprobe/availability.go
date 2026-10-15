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
	ID         string                    `json:"id"`
	SiteID     int64                     `json:"site_id"`
	RawModel   string                    `json:"raw_model"`
	MonitorID  string                    `json:"monitor_id,omitempty"`
	GroupName  string                    `json:"group_name,omitempty"`
	Price      store.SiteProbePrice      `json:"price"`
	ObservedAt time.Time                 `json:"observed_at"`
	Series     *store.AvailabilitySeries `json:"series"`
	// Evidence is every reading behind this row, in the order the console shows
	// them: our own relay traffic, our probes, the site's own word, then the
	// third-party directory — the same four the model page shows for the same
	// model. Series is the third of those alone, because the panel filters rows by
	// what the site itself claims.
	Evidence     []*store.AvailabilitySeries `json:"evidence"`
	Primary      *store.AvailabilitySeries   `json:"primary"`
	WeakEvidence bool                        `json:"weak_evidence"`
}
type Observations struct {
	Items []Observation `json:"items"`
	Sites []SiteStatus  `json:"sites"`
	Since time.Time     `json:"since"`
	Until time.Time     `json:"until"`
	// ThirdParty is the state of the monitoring directory's snapshot — when it was
	// last pulled, how many readings it held, and the failure if the last attempt
	// had one. It is absent until this process has attempted a sync: the panel
	// shows third-party readings for the site it is displaying, so "we have not
	// been able to refresh those since X" belongs next to them, and a line about
	// a snapshot nobody has tried to pull yet would be noise.
	ThirdParty *ExternalSyncState `json:"third_party,omitempty"`
}

func (s *Service) Observations(hours int) (*Observations, error) {
	if hours <= 0 {
		hours = 24
	}
	until := s.now().UTC().Truncate(time.Second).Add(time.Second)
	since := until.Add(-time.Duration(hours) * time.Hour)
	result := &Observations{Items: []Observation{}, Sites: []SiteStatus{}, Since: since, Until: until}
	if state := s.ExternalSyncState(); !state.AttemptedAt.IsZero() {
		result.ThirdParty = &state
	}
	sites, err := s.db.Site.List()
	if err != nil {
		return nil, err
	}
	samples, err := s.db.LatestSiteObservations()
	if err != nil {
		return nil, err
	}
	evidence := s.observationEvidence(sites, since, until)
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
		// The site's own collections, drawn as one cell per collection. They used to
		// be a single number with an empty timeline: a site that said 99.2% was a bar
		// with no history beside sources that have one, and "when did it turn red"
		// had no answer on the panel that exists to answer it.
		identity := sourceIdentity(site)
		for _, sample := range samples[site.ID] {
			// A changed source has no observations until its first successful collection.
			if sample.SourceKey != "" && sample.SourceKey != identity {
				continue
			}
			series := byMonitor[sample.MonitorID]
			if series == nil {
				series = store.NewAvailabilitySeries("site", "site_model", since, until)
				byMonitor[sample.MonitorID] = series
				// The totals are the newest reading's own numbers, and its face is the
				// time it was collected: adding up what a site published across
				// collections would print a total no site ever reported.
				if sample.RatioKnown || sample.Samples > 0 {
					ratio := sample.Ratio
					series.Ratio = &ratio
					series.Samples = sample.Samples
					series.OK = sample.UpCount
					series.Failed = sample.Samples - sample.UpCount
					at := sample.ObservedAt
					series.LastAt = &at
				}
				for _, point := range evidence.samples[siteMonitorKey(site.ID, identity, sample.MonitorID)] {
					ok, readings := readingCell(point.Ratio, point.OK, point.Samples)
					series.ObserveReading(point.At, ok, readings, until)
				}
			}
			series.Stale = sample.ObservedAt.IsZero() || until.Sub(sample.ObservedAt) > StaleAfter
			item := Observation{ID: fmt.Sprintf("%d/%s/%s/%s", site.ID, sample.RawModel, sample.GroupName, sample.MonitorID),
				SiteID: site.ID, RawModel: sample.RawModel, MonitorID: sample.MonitorID, GroupName: sample.GroupName,
				Price: sample.Price, ObservedAt: sample.ObservedAt, Series: series, WeakEvidence: sample.WeakEvidence}
			folded := coreModelName(sample.RawModel)
			item.Evidence = siteModelEvidence(evidence.traffic[site.ID][folded], evidence.probes[site.ID][folded], series,
				externalSeries(evidence.external, evidence.externalHistory, site.ID, sample.RawModel, since, until))
			item.Primary = primaryOf(item.Evidence)
			result.Items = append(result.Items, item)
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
	externalHistory := s.externalHistoryIndex(since)
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
			addSiteEvidence(&item, observations, external, externalHistory, since, until)
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
				addSiteEvidence(&item, observations, external, externalHistory, since, until)
				choosePrimary(&item)
				report.Models = append(report.Models, item)
			}
		}
	}
	return report, nil
}
func addSiteEvidence(item *AvailabilityItem, observations *Observations, external map[string]ExternalReading, externalHistory map[string][]store.ExternalHistoryPoint, since, until time.Time) {
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
	if series := externalSeries(external, externalHistory, item.SiteID, item.Model, since, until); series != nil {
		item.Evidence = append(item.Evidence, series)
	}
}

// externalSeries renders the directory's reading for one (site, published model),
// with every reading this gateway has pulled for that pair as its history. A pair
// the directory publishes nothing usable about has no series at all: its absence
// is not a 0%.
func externalSeries(external map[string]ExternalReading, history map[string][]store.ExternalHistoryPoint, siteID int64, rawModel string, since, until time.Time) *store.AvailabilitySeries {
	reading := externalFor(siteID, rawModel, external)
	if reading == nil {
		return nil
	}
	return thirdPartySeries(*reading, history[externalKey(siteID, rawModel)], since, until)
}

// thirdPartySeries renders one directory reading as evidence.
//
// The ratio, the counts and the last sample are the directory's own numbers for
// its own window, printed for what they are. What this gateway judges is only
// its own copy: a snapshot it has not been able to refresh for longer than a probe
// round is allowed to go stale is marked as such, because "we could not pull
// this" is a fact about us — and the only one that should wear 「已过期」.
//
// The timeline is our own log of what the directory said, one cell per pull, which
// is the one thing a snapshot of a rolling window cannot answer for itself: the
// totals above are the newest reading's, not the sum of readings that overlap.
func thirdPartySeries(reading ExternalReading, points []store.ExternalHistoryPoint, since, until time.Time) *store.AvailabilitySeries {
	series := store.NewAvailabilitySeries(SourceWatchbot, "site_model", since, until)
	for _, point := range points {
		ok, samples := readingCell(point.Ratio, point.SuccessCount, point.RequestCount)
		series.ObserveReading(point.At, ok, samples, until)
	}
	ratio := reading.Ratio
	series.Ratio = &ratio
	series.Samples = reading.RequestCount
	series.OK = reading.SuccessCount
	series.Failed = reading.FailureCount
	series.Stale = reading.SnapshotAt.IsZero() || until.Sub(reading.SnapshotAt) > StaleAfter
	if at, err := time.Parse(time.RFC3339, reading.ObservedAt); err == nil {
		series.LastAt = &at
	}
	return series
}

// availabilityBrokenBelow is the ratio this console's time cells call broken —
// the amber/red boundary its own banding uses. It is repeated here for the one
// case where a cell has to be drawn from a claim instead of from counts.
const availabilityBrokenBelow = 0.8

// readingCell turns a reading that may publish only a ratio into the counts a time
// cell can carry.
//
// A cell is drawn as ok over samples, so a source that publishes a bare
// percentage — the directory's success_rate on most of its rows, a transit
// snapshot's availability — has nothing to draw with, and those rows are the
// majority. One reading is exactly one sample: green when the number is at or
// above the band this console calls broken, red below. Inventing request counts
// so that a percentage can be drawn as a ratio would print numbers no source ever
// published.
//
// A source that publishes neither counts nor a number has no reading at all — a
// site's price table lists models it reports no health for — and gets no cell: a
// missing measurement is not a failed one.
func readingCell(ratio float64, ok, samples int) (int, int) {
	if samples > 0 {
		return ok, samples
	}
	if ratio <= 0 {
		return 0, 0
	}
	if ratio >= availabilityBrokenBelow {
		return 1, 1
	}
	return 0, 1
}

// observationEvidence is every reading the console shows next to a site's own
// word, all of it keyed by (site, model): our relay traffic, our probes, the
// site's collections, and the third-party directory's readings.
type observationEvidence struct {
	traffic         map[int64]map[string]*store.AvailabilitySeries
	probes          map[int64]map[string]*store.AvailabilitySeries
	samples         map[string][]store.SiteProbeSeriesPoint
	external        map[string]ExternalReading
	externalHistory map[string][]store.ExternalHistoryPoint
}

// siteMonitorKey identifies one monitor's timeline: the site, the source it was
// collected from — a site re-pointed at a different source must not show the
// previous source's history under the new one — and the monitor itself.
func siteMonitorKey(siteID int64, sourceKey, monitorID string) string {
	return fmt.Sprintf("%d|%s|%s", siteID, sourceKey, monitorID)
}

// observationEvidence collects the readings that are not the site's own claim.
//
// None of it may fail the panel: a source that cannot be read is logged and left
// out, and the row keeps whatever else it has — the same posture the report takes
// towards traffic and third-party readings.
func (s *Service) observationEvidence(sites []domain.Site, since, until time.Time) *observationEvidence {
	evidence := &observationEvidence{
		traffic:         map[int64]map[string]*store.AvailabilitySeries{},
		probes:          map[int64]map[string]*store.AvailabilitySeries{},
		samples:         map[string][]store.SiteProbeSeriesPoint{},
		external:        map[string]ExternalReading{},
		externalHistory: map[string][]store.ExternalHistoryPoint{},
	}
	// A row collected before the source was recorded carries no key of its own,
	// and the aggregate path treats those as belonging to whatever the site
	// collects from now. The timeline has to read them the same way.
	identity := make(map[int64]string, len(sites))
	for _, site := range sites {
		identity[site.ID] = sourceIdentity(site)
	}
	if series, err := s.db.SiteAvailabilityTraffic(since, until); err != nil {
		s.logger.Warn("site probe: traffic evidence unavailable", "error", err)
	} else {
		evidence.traffic = foldSiteSeries(series)
	}
	if series, err := s.db.SiteAvailabilityProbes(since, until); err != nil {
		s.logger.Warn("site probe: probe evidence unavailable", "error", err)
	} else {
		evidence.probes = foldSiteSeries(series)
	}
	if points, err := s.db.SiteProbeSampleHistory(since, until, store.AvailabilityBuckets); err != nil {
		s.logger.Warn("site probe: collection history unavailable", "error", err)
	} else {
		for _, point := range points {
			source := point.SourceKey
			if source == "" {
				source = identity[point.SiteID]
			}
			key := siteMonitorKey(point.SiteID, source, point.MonitorID)
			evidence.samples[key] = append(evidence.samples[key], point)
		}
	}
	if readings, err := s.externalIndex(); err != nil {
		s.logger.Warn("site probe: external readings unavailable", "error", err)
	} else {
		evidence.external = readings
	}
	evidence.externalHistory = s.externalHistoryIndex(since)
	return evidence
}

// externalHistoryIndex loads the readings this gateway has pulled, keyed the same
// way the snapshot is (see externalKey) so a row finds its own history.
func (s *Service) externalHistoryIndex(since time.Time) map[string][]store.ExternalHistoryPoint {
	points, err := s.db.ListSiteProbeExternalHistory(since)
	if err != nil {
		s.logger.Warn("site probe: external history unavailable", "error", err)
		return map[string][]store.ExternalHistoryPoint{}
	}
	out := make(map[string][]store.ExternalHistoryPoint, len(points))
	for _, point := range points {
		key := externalKey(point.SiteID, point.RawModel)
		out[key] = append(out[key], point)
	}
	return out
}

// foldSiteSeries re-keys a (site, upstream model) series map by the folded model
// name, merging what folds together.
//
// A site publishes the bare upstream name while our catalog names each alias with
// a namespace (cn:glm-5.2, global:glm-5.2), so one published model can be reached
// under several names. The console shows it as one row, and dropping either set of
// calls would understate what we sent.
func foldSiteSeries(in map[int64]map[string]*store.AvailabilitySeries) map[int64]map[string]*store.AvailabilitySeries {
	out := make(map[int64]map[string]*store.AvailabilitySeries, len(in))
	for siteID, byModel := range in {
		folded := make(map[string]*store.AvailabilitySeries, len(byModel))
		for model, series := range byModel {
			key := coreModelName(model)
			if existing, found := folded[key]; found {
				existing.Merge(series)
				continue
			}
			folded[key] = series
		}
		out[siteID] = folded
	}
	return out
}

// siteModelEvidence assembles one row's readings in the order the console shows
// them: our traffic, our probes, the site's own word, then the directory. A source
// with nothing to say is left out rather than sent as an empty series, which the
// panel would draw as a source that answered.
func siteModelEvidence(traffic, probes, own, external *store.AvailabilitySeries) []*store.AvailabilitySeries {
	out := []*store.AvailabilitySeries{}
	for _, series := range []*store.AvailabilitySeries{traffic, probes, own, external} {
		if series != nil {
			out = append(out, series)
		}
	}
	return out
}

func choosePrimary(item *AvailabilityItem) {
	item.Primary = primaryOf(item.Evidence)
}

// primaryOf is the reading a table cell quotes: the first unstale one that carries
// a ratio. A source that has nothing to say must not shadow one that has.
func primaryOf(evidence []*store.AvailabilitySeries) *store.AvailabilitySeries {
	for _, series := range evidence {
		if series != nil && series.Ratio != nil && !series.Stale {
			return series
		}
	}
	return nil
}

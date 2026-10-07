package siteprobe

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lan/meta-gateway/internal/adapters"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

// Defaults for the collection loop. The cadence matches the probe pages we read
// (Uptime Kuma's default status page refreshes every 5 minutes and keeps ~100
// beats per monitor, i.e. one every ~14 minutes), so a 15-minute round sees one
// new beat per monitor on average.
const (
	DefaultInterval = 15 * time.Minute
	DefaultJitter   = 2 * time.Minute
	// StaleAfter: readings older than this are never acted on. A site that
	// stopped being collectable must not keep a stale verdict alive.
	StaleAfter = 2 * time.Hour
	// collectTimeout bounds one site's collection.
	collectTimeout = 30 * time.Second
)

// Verdicts. "pending" is not an error: it means the evidence is not yet
// consecutive enough to act on, which is the normal state for the first rounds
// after a source is configured.
const (
	VerdictOK           = "ok"
	VerdictLow          = "low"
	VerdictPending      = "pending"
	VerdictInsufficient = "insufficient"
	VerdictStale        = "stale"
	VerdictNoData       = "no_data"
)

// Where a verdict's evidence came from.
const (
	// SourceSite: the site's own published probe data (rounds).
	SourceSite = "site"
	// SourceTraffic: our own relay logs for the same (channel, model). Used only
	// when the site publishes no availability of its own.
	SourceTraffic = "traffic"
	// SourceExternal: a third-party monitoring directory's aggregate. Last
	// resort, and always labelled: nobody in this gateway measured it.
	SourceExternal = "watchbot"
)

// TrafficWindow is how much of our own relay traffic counts as current evidence
// for availability. A day keeps a busy channel well above the sample floor while
// still forgetting an outage that ended yesterday.
const TrafficWindow = 24 * time.Hour

// Policy is the verdict rule set: how many rounds in a row, with how many
// samples each, and at which availability.
type Policy struct {
	RatioThreshold float64 `json:"ratio_threshold"`
	MinSamples     int     `json:"min_samples"`
	LowRounds      int     `json:"low_rounds"`
	HighRounds     int     `json:"high_rounds"`
}

// DefaultPolicy mirrors the design doc: 90% availability over at least 5
// samples, twice in a row, in both directions.
func DefaultPolicy() Policy {
	return Policy{RatioThreshold: 0.9, MinSamples: 5, LowRounds: 2, HighRounds: 2}
}

func (p Policy) withDefaults() Policy {
	fallback := DefaultPolicy()
	if p.RatioThreshold <= 0 || p.RatioThreshold > 1 {
		p.RatioThreshold = fallback.RatioThreshold
	}
	if p.MinSamples <= 0 {
		p.MinSamples = fallback.MinSamples
	}
	if p.MinSamples > 1000 {
		p.MinSamples = 1000
	}
	if p.LowRounds <= 0 {
		p.LowRounds = fallback.LowRounds
	}
	if p.HighRounds <= 0 {
		p.HighRounds = fallback.HighRounds
	}
	if p.LowRounds > 10 {
		p.LowRounds = 10
	}
	if p.HighRounds > 10 {
		p.HighRounds = 10
	}
	return p
}

// Round is one collection round's reading of one model on one site.
type Round struct {
	RunID        int64                 `json:"run_id"`
	Ratio        float64               `json:"ratio"`
	Samples      int                   `json:"samples"`
	UpCount      int                   `json:"up_count"`
	WeakEvidence bool                  `json:"weak_evidence"`
	AvgPingMS    *int                  `json:"avg_ping_ms,omitempty"`
	ObservedAt   time.Time             `json:"observed_at"`
	Price        *store.SiteProbePrice `json:"price,omitempty"`
}

// MemberState is what the tool shows about a member it may act on.
type MemberState struct {
	MemberID     int64  `json:"member_id"`
	ChannelID    int64  `json:"channel_id"`
	ChannelName  string `json:"channel_name"`
	GroupName    string `json:"group_name,omitempty"`
	Enabled      bool   `json:"enabled"`
	AutoDisabled bool   `json:"auto_disabled"`
	SingleMember bool   `json:"single_member,omitempty"`
	// HasPrice marks a member that already carries a price in the billing
	// layer. Adopting an observed price fills empty fields only, so this tells
	// the operator whether the button would do anything.
	HasPrice bool `json:"has_price"`
}

// ExternalReading is a third-party monitoring directory's aggregate for one
// (site, model) pair. It exists because the gateway has no way to read a
// per-model availability from the sites themselves: measured across a fleet of
// them, /api/models/status answers 401, /api/ratio_config 403, /api/uptime 404,
// and /healthz only says "I am up".
type ExternalReading struct {
	Source           string  `json:"source"`
	Ratio            float64 `json:"ratio"`
	AvgLatencyMS     int     `json:"avg_latency_ms,omitempty"`
	FirstTokenMS     int     `json:"first_token_ms,omitempty"`
	TokensPerSecond  float64 `json:"tokens_per_second,omitempty"`
	ServiceState     string  `json:"service_state,omitempty"`
	AcquisitionState string  `json:"acquisition_state,omitempty"`
	ObservedAt       string  `json:"observed_at,omitempty"`
}

// TrafficReading is our own observed outcome for one (route, site) pair. It is
// the fallback evidence for a site that publishes no availability, and it is
// labelled as such everywhere it appears: a self-reported number and a measured
// one are not the same claim.
type TrafficReading struct {
	Samples        int     `json:"samples"`
	Failures       int     `json:"failures"`
	Ratio          float64 `json:"ratio"`
	AvgFirstByteMS int     `json:"avg_first_byte_ms,omitempty"`
	WindowHours    int     `json:"window_hours"`
}

// Row is one (route × site) reading: what the site says about a model we serve.
type Row struct {
	Route      string        `json:"route"`
	Match      string        `json:"match"`
	RawModel   string        `json:"raw_model"`
	SiteID     int64         `json:"site_id"`
	SiteName   string        `json:"site_name"`
	GroupName  string        `json:"group_name,omitempty"`
	Verdict    string        `json:"verdict"`
	LowStreak  int           `json:"low_streak"`
	OKStreak   int           `json:"ok_streak"`
	Rounds     []Round       `json:"rounds"`
	Members    []MemberState `json:"members"`
	PriceOnly  bool          `json:"price_only,omitempty"`
	SourceKind string        `json:"source_kind,omitempty"`
	// ObservedPrice is the newest price the site published for this model,
	// normalized to USD by the shared normalizer. It is evidence: it never
	// reaches the billing layer unless an operator adopts it.
	ObservedPrice *adapters.PriceQuote `json:"observed_price,omitempty"`
	// Traffic is our own relay's record for this pair, present whenever any
	// request has been logged in the window — including for sites that publish
	// no probe data at all.
	Traffic *TrafficReading `json:"traffic,omitempty"`
	// AvailabilitySource names the evidence behind Verdict/LowStreak: "site"
	// (the site's own probe data) or "traffic" (our own relay logs).
	AvailabilitySource string `json:"availability_source,omitempty"`
	// External is a third-party directory's reading for the same pair, shown and
	// used only when the site and our own traffic are both silent.
	External *ExternalReading `json:"external,omitempty"`
	// CatalogPrice is the model catalog's reference price, filled only when the
	// site publishes no price for this model. It is a public list price in USD per
	// 1M tokens, not this site's price, and it is already what the billing layer
	// falls back to when a member carries none — so it is shown for completeness
	// and is never adopted per member (that write would only duplicate it).
	CatalogPrice *adapters.PriceQuote `json:"catalog_price,omitempty"`
}

// Unmatched is a monitored model that maps to no route. It is reported so the
// operator can see what the site monitors (and what it charges) that we do not
// serve; it is never acted on.
type Unmatched struct {
	SiteID    int64                 `json:"site_id"`
	SiteName  string                `json:"site_name"`
	RawModel  string                `json:"raw_model"`
	GroupName string                `json:"group_name,omitempty"`
	Ratio     float64               `json:"ratio"`
	Samples   int                   `json:"samples"`
	Price     *store.SiteProbePrice `json:"price,omitempty"`
}

// NameOnly is a model a site publishes whose name matches one of our routes,
// but that route has no member on this site.
//
// It exists because the alternative is a lie: the match index is a name index,
// so a site publishing "kimi-k3" matches whichever route has a member mapping
// that name — even when that member lives on a different site. Emitting a Row
// for it produced rows with no members, which carry a verdict nobody can act on
// and hide the model the site actually publishes (2026-10-07: 31 of 47 rows in
// production were like this, and 15 of them shared one route name, so every one
// of them read as the same model). A candidate is honest: here is a model this
// site sells that we could attach a member to.
type NameOnly struct {
	SiteID    int64                 `json:"site_id"`
	SiteName  string                `json:"site_name"`
	RawModel  string                `json:"raw_model"`
	Route     string                `json:"route"`
	Match     string                `json:"match"`
	GroupName string                `json:"group_name,omitempty"`
	Ratio     float64               `json:"ratio"`
	Samples   int                   `json:"samples"`
	Price     *store.SiteProbePrice `json:"price,omitempty"`
}

// SiteStatus is the collection state of one site, for the tool's site list.
type SiteStatus struct {
	SiteID       int64      `json:"site_id"`
	SiteName     string     `json:"site_name"`
	Kind         string     `json:"probe_source_kind,omitempty"`
	URL          string     `json:"probe_source_url,omitempty"`
	Enabled      bool       `json:"probe_source_enabled"`
	LastRunAt    *time.Time `json:"probe_last_run_at,omitempty"`
	LastError    string     `json:"probe_last_error,omitempty"`
	LastStatus   string     `json:"last_run_status,omitempty"`
	MonitorCount int        `json:"monitor_count"`
	// Config is the stored settings blob, echoed so the dialog can show what
	// this site is configured to do; AutoApply is the parsed flag the operator
	// actually reads.
	Config    string `json:"probe_source_config,omitempty"`
	AutoApply bool   `json:"auto_apply"`
	Policy    Policy `json:"policy"`
}

// Report is what the model-page tool renders.
type Report struct {
	Policy      Policy       `json:"policy"`
	Rows        []Row        `json:"rows"`
	Unmatched   []Unmatched  `json:"unmatched"`
	NameOnly    []NameOnly   `json:"name_only"`
	Sites       []SiteStatus `json:"sites"`
	GeneratedAt time.Time    `json:"generated_at"`
}

// Service collects site probe data and turns it into routing decisions.
type Service struct {
	db     *store.DB
	client *http.Client
	logger *slog.Logger
	now    func() time.Time

	// externalMu guards the last third-party snapshot sync, which is rate-limited
	// independently of the site cadence.
	externalMu       sync.Mutex
	externalSyncedAt time.Time
}

// NewService builds the collector. The HTTP client is separate from the relay's
// outbound client on purpose: probe pages are plain public GETs and must not be
// able to affect upstream request policy.
//
// proxyHook is how the collector picks up the console-configured global proxy.
// It must NOT fall back to the environment: the container inherits the host's
// HTTP_PROXY verbatim, and a host value like 127.0.0.1:7897 points at the
// container itself, not at the host — every collection round would then die on
// `proxyconnect tcp: connection refused` (the exact bug this hook exists for;
// the same reasoning already forced the OAuth client off the environment).
// Passing nil keeps direct connections.
func NewService(db *store.DB, logger *slog.Logger, proxyHook func(*http.Request) (*url.URL, error)) *Service {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	transport := &http.Transport{
		Proxy: func(req *http.Request) (*url.URL, error) {
			if proxyHook == nil {
				return nil, nil
			}
			return proxyHook(req)
		},
	}
	return &Service{
		db:     db,
		client: &http.Client{Timeout: collectTimeout, Transport: transport},
		logger: logger,
		now:    func() time.Time { return time.Now().UTC() },
	}
}

// HTTPClient exposes the collector's client (timeout + proxy hook) for the
// admin endpoints that fetch one-off previews (detect, catalog). They must see
// the same network view as the scheduled rounds, or the preview would succeed
// and the collection fail.
func (s *Service) HTTPClient() *http.Client { return s.client }

// CollectSite runs one round against a single site and stores its samples. A
// failure is recorded on the run and on the site row, and stores no samples:
// with no samples there is no verdict, so a broken source can never disable a
// healthy member.
func (s *Service) CollectSite(ctx context.Context, site domain.Site) (*store.SiteProbeRun, error) {
	kind := strings.TrimSpace(site.ProbeSourceKind)
	if kind == "" || strings.TrimSpace(site.ProbeSourceURL) == "" {
		return nil, fmt.Errorf("site %d has no probe source", site.ID)
	}
	started := s.now()
	runID, err := s.db.CreateSiteProbeRun(site.ID, kind, started)
	if err != nil {
		return nil, err
	}

	var samples []store.SiteProbeSample
	switch kind {
	case SourceUptimeKuma:
		source, fetchErr := FetchKuma(ctx, s.client, site.ProbeSourceURL, kumaDefaultWindow)
		if fetchErr != nil {
			return s.failRun(site, runID, started, fetchErr)
		}
		samples = source.Samples(site.ID, runID, started)
	case SourceNewAPI:
		snapshot, fetchErr := FetchPublicPrices(ctx, s.client, site.ProbeSourceURL)
		if fetchErr != nil {
			return s.failRun(site, runID, started, fetchErr)
		}
		samples = priceSamples(site.ID, runID, started, snapshot)
	case SourceSub2APITransit:
		_, snapshotURL, fetchErr := ResolveTransitDiscovery(ctx, s.client, site.ProbeSourceURL)
		if fetchErr != nil {
			return s.failRun(site, runID, started, fetchErr)
		}
		transit, _, fetchErr := FetchTransit(ctx, s.client, snapshotURL)
		if fetchErr != nil {
			return s.failRun(site, runID, started, fetchErr)
		}
		samples = TransitSamples(transit, site.ID, runID, started)
	default:
		return s.failRun(site, runID, started, fmt.Errorf("unsupported probe source kind %q", kind))
	}

	if err := s.db.InsertSiteProbeSamples(samples); err != nil {
		return s.failRun(site, runID, started, err)
	}
	if err := s.db.FinishSiteProbeRun(runID, store.SiteProbeRunOK, len(samples), ""); err != nil {
		return nil, err
	}
	if err := s.db.MarkSiteProbeOutcome(site.ID, started, ""); err != nil {
		return nil, err
	}
	return &store.SiteProbeRun{
		ID: runID, SiteID: site.ID, SourceKind: kind, StartedAt: started,
		Status: store.SiteProbeRunOK, MonitorCount: len(samples),
	}, nil
}

func (s *Service) failRun(site domain.Site, runID int64, started time.Time, cause error) (*store.SiteProbeRun, error) {
	message := cause.Error()
	if err := s.db.FinishSiteProbeRun(runID, store.SiteProbeRunFailed, 0, message); err != nil {
		s.logger.Warn("site probe: finish failed run", "site", site.ID, "error", err)
	}
	if err := s.db.MarkSiteProbeOutcome(site.ID, started, message); err != nil {
		s.logger.Warn("site probe: mark outcome", "site", site.ID, "error", err)
	}
	s.logger.Info("site probe: collection failed", "site", site.ID, "name", site.Name, "error", message)
	return &store.SiteProbeRun{
		ID: runID, SiteID: site.ID, SourceKind: site.ProbeSourceKind, StartedAt: started,
		Status: store.SiteProbeRunFailed, Error: message,
	}, nil
}

// CollectEnabledSites collects every site whose probe source is enabled. Sites
// are collected sequentially: these are small public GETs and a burst of them
// from one address is exactly what a site operator would block.
func (s *Service) CollectEnabledSites(ctx context.Context) (ok int, failed int, err error) {
	sites, err := s.EnabledSites()
	if err != nil {
		return 0, 0, err
	}
	for _, site := range sites {
		if ctx.Err() != nil {
			return ok, failed, ctx.Err()
		}
		run, err := s.CollectSite(ctx, site)
		if err != nil {
			failed++
			s.logger.Warn("site probe: collect", "site", site.ID, "error", err)
			continue
		}
		if run != nil && run.Status == store.SiteProbeRunFailed {
			failed++
			continue
		}
		ok++
	}
	return ok, failed, nil
}

// EnabledSites lists sites that have a usable probe source: either an explicit
// kind+URL, or auto mode with a platform the collector can derive a source for
// (new-api -> its pricing table, sub2api -> its public-transit discovery). Sites
// whose auto derivation resolves to nothing are skipped silently — there is no
// known place to look, and an error per round would just be noise.
func (s *Service) EnabledSites() ([]domain.Site, error) {
	sites, err := s.db.Site.List()
	if err != nil {
		return nil, err
	}
	out := make([]domain.Site, 0, len(sites))
	for _, site := range sites {
		if !site.ProbeSourceEnabled {
			continue
		}
		site = resolveProbeSource(site)
		if strings.TrimSpace(site.ProbeSourceKind) == "" || strings.TrimSpace(site.ProbeSourceURL) == "" {
			continue
		}
		out = append(out, site)
	}
	return out, nil
}

// resolveProbeSource fills kind/url on an auto site. A custom source always
// wins: an operator who typed a URL is telling us the platform default is wrong
// for this site (a Kuma page on another host, a pricing page on a subdomain).
func resolveProbeSource(site domain.Site) domain.Site {
	if strings.TrimSpace(site.ProbeSourceURL) != "" && strings.TrimSpace(site.ProbeSourceKind) != "" {
		return site
	}
	if !site.ProbeAuto {
		return site
	}
	if kind, url, ok := AutoSource(site.Platform, site.BaseURL); ok {
		site.ProbeSourceKind = kind
		site.ProbeSourceURL = url
	}
	return site
}

// Report builds the tool's view: every (route × site) reading we have, plus the
// monitored models that match no route and each site's collection state.
func (s *Service) Report(policy Policy) (*Report, error) {
	policy = policy.withDefaults()
	now := s.now()
	resolver, err := BuildResolver(s.db)
	if err != nil {
		return nil, err
	}
	sites, err := s.db.Site.List()
	if err != nil {
		return nil, err
	}
	siteNames := make(map[int64]string, len(sites))
	for _, site := range sites {
		siteNames[site.ID] = site.Name
	}
	samplesBySite, err := s.db.ListRecentSamplesBySite(max(policy.LowRounds, policy.HighRounds, 3))
	if err != nil {
		return nil, err
	}
	// Our own traffic is the availability signal for the sites that publish
	// none (which is nearly all of them: a New-API price table carries prices,
	// not health). A read failure here must not hide the whole report, so it is
	// logged and the rows simply carry no traffic reading.
	trafficWindow := TrafficWindow
	if trafficWindow <= 0 {
		trafficWindow = 24 * time.Hour
	}
	traffic, err := s.db.TrafficAvailability(now.Add(-trafficWindow))
	if err != nil {
		s.logger.Warn("site probe: traffic availability unavailable", "error", err)
		traffic = map[string]store.TrafficStat{}
	}
	// Third-party readings, likewise, must not be able to break the report.
	external, err := s.externalIndex()
	if err != nil {
		s.logger.Warn("site probe: external readings unavailable", "error", err)
		external = map[string]ExternalReading{}
	}
	// The catalog's own list prices, used only where the site publishes none.
	catalog, err := s.CatalogPrices()
	if err != nil {
		s.logger.Warn("site probe: catalog prices unavailable", "error", err)
		catalog = map[string]adapters.PriceQuote{}
	}

	// Every array starts non-nil so the JSON is `[]` rather than `null`: the
	// console renders these directly, and a null array is exactly the kind of
	// thing that survives every test (they all have data) and crashes the
	// first real empty view.
	report := &Report{
		Policy: policy, GeneratedAt: now,
		Rows: []Row{}, Unmatched: []Unmatched{}, NameOnly: []NameOnly{}, Sites: []SiteStatus{},
	}
	seenUnmatched := make(map[string]bool)
	seenNameOnly := make(map[string]bool)
	for _, site := range sites {
		status := SiteStatus{
			SiteID: site.ID, SiteName: site.Name, Kind: site.ProbeSourceKind,
			URL: site.ProbeSourceURL, Enabled: site.ProbeSourceEnabled,
			LastRunAt: site.ProbeLastRunAt, LastError: site.ProbeLastError,
			Config: site.ProbeSourceConfig, Policy: policy,
		}
		if config, ok := ParseSourceConfig(site.ProbeSourceConfig); ok && config.AutoApply {
			status.AutoApply = true
			status.Policy = config.Policy
		}
		if run, runErr := s.db.LatestSiteProbeRun(site.ID); runErr == nil && run != nil {
			status.LastStatus = run.Status
			status.MonitorCount = run.MonitorCount
		}
		report.Sites = append(report.Sites, status)

		rows, candidates := s.rowsForSite(site, samplesBySite[site.ID], resolver, traffic, external, catalog, policy, now)
		for _, candidate := range candidates {
			key := fmt.Sprintf("%d/%s/%s", site.ID, candidate.RawModel, candidate.Route)
			if seenNameOnly[key] {
				continue
			}
			seenNameOnly[key] = true
			report.NameOnly = append(report.NameOnly, candidate)
		}
		for _, row := range rows {
			if row.Route == "" {
				key := fmt.Sprintf("%d/%s", site.ID, row.RawModel)
				if !seenUnmatched[key] {
					seenUnmatched[key] = true
					reading := Round{}
					if len(row.Rounds) > 0 {
						reading = row.Rounds[0]
					}
					report.Unmatched = append(report.Unmatched, Unmatched{
						SiteID: site.ID, SiteName: site.Name, RawModel: row.RawModel,
						GroupName: row.GroupName, Ratio: reading.Ratio, Samples: reading.Samples,
						Price: reading.Price,
					})
				}
				continue
			}
			report.Rows = append(report.Rows, row)
		}
	}
	sort.Slice(report.Rows, func(i, j int) bool {
		if report.Rows[i].Route != report.Rows[j].Route {
			return report.Rows[i].Route < report.Rows[j].Route
		}
		return report.Rows[i].SiteName < report.Rows[j].SiteName
	})
	sort.Slice(report.Unmatched, func(i, j int) bool {
		if report.Unmatched[i].SiteName != report.Unmatched[j].SiteName {
			return report.Unmatched[i].SiteName < report.Unmatched[j].SiteName
		}
		return report.Unmatched[i].RawModel < report.Unmatched[j].RawModel
	})
	sort.Slice(report.NameOnly, func(i, j int) bool {
		if report.NameOnly[i].SiteName != report.NameOnly[j].SiteName {
			return report.NameOnly[i].SiteName < report.NameOnly[j].SiteName
		}
		if report.NameOnly[i].RawModel != report.NameOnly[j].RawModel {
			return report.NameOnly[i].RawModel < report.NameOnly[j].RawModel
		}
		return report.NameOnly[i].Route < report.NameOnly[j].Route
	})
	return report, nil
}

// rowsForSite folds one site's samples into per-model rows (newest round first),
// plus the candidates it publishes for routes that have no member here.
//
// The split is the point: a row is something an operator can act on (it has
// members on this site), a candidate is a name that happens to collide with one
// of our routes. Mixing them produced rows whose members list was empty — a
// verdict nothing could act on, titled with the route name, so every such row
// looked like the same model.
func (s *Service) rowsForSite(site domain.Site, samples []store.SiteProbeSample, resolver *Resolver, traffic map[string]store.TrafficStat, external map[string]ExternalReading, catalog map[string]adapters.PriceQuote, policy Policy, now time.Time) ([]Row, []NameOnly) {
	type bucket struct {
		rawModel  string
		groupName string
		rounds    []Round
	}
	order := []string{}
	byModel := map[string]*bucket{}
	for _, sample := range samples {
		key := sample.RawModel
		entry, ok := byModel[key]
		if !ok {
			entry = &bucket{rawModel: sample.RawModel, groupName: sample.GroupName}
			byModel[key] = entry
			order = append(order, key)
		}
		price := sample.Price
		entry.rounds = append(entry.rounds, Round{
			RunID: sample.RunID, Ratio: sample.Ratio, Samples: sample.Samples, UpCount: sample.UpCount,
			WeakEvidence: sample.WeakEvidence, AvgPingMS: sample.AvgPingMS, ObservedAt: sample.ObservedAt,
			Price: &price,
		})
	}
	rows := make([]Row, 0, len(order))
	candidates := make([]NameOnly, 0, len(order))
	for _, key := range order {
		entry := byModel[key]
		// A site lists the bare upstream name while our catalog names each alias
		// with a namespace (cn:glm-5.2, global:glm-5.2). One published model can
		// therefore belong to several routes, and each is a real route with its
		// own members: emit a row per match instead of picking an arbitrary one.
		matches := resolver.MatchAll(entry.rawModel)
		if len(matches) == 0 {
			matches = []ModelMatch{{}}
		}
		observed := observablePrice(entry.rawModel, entry.rounds)
		for _, match := range matches {
			members := resolver.MembersFor(match.Route, site.ID)
			// A match by name with no member on this site is not something this
			// site serves for us: report the published model as a candidate and
			// leave the routing table alone. (The second pass below covers the
			// other direction — routes we do serve here with nothing published.)
			if match.Route != "" && len(members) == 0 {
				reading := Round{}
				if len(entry.rounds) > 0 {
					reading = entry.rounds[0]
				}
				candidates = append(candidates, NameOnly{
					SiteID: site.ID, SiteName: site.Name, RawModel: entry.rawModel,
					Route: match.Route, Match: match.Kind, GroupName: entry.groupName,
					Ratio: reading.Ratio, Samples: reading.Samples, Price: reading.Price,
				})
				continue
			}
			row := Row{
				Route: match.Route, Match: match.Kind, RawModel: entry.rawModel,
				SiteID: site.ID, SiteName: site.Name, GroupName: entry.groupName,
				Rounds: entry.rounds, Members: []MemberState{}, SourceKind: site.ProbeSourceKind,
				ObservedPrice: observed,
			}
			row.PriceOnly = readingsArePriceOnly(entry.rounds)
			for _, member := range members {
				row.Members = append(row.Members, MemberState{
					MemberID: member.MemberID, ChannelID: member.ChannelID, ChannelName: member.ChannelName,
					GroupName: member.GroupName, Enabled: member.Enabled, AutoDisabled: member.AutoDisabled,
					SingleMember: member.SingleMember, HasPrice: member.HasPrice,
				})
			}
			// Traffic is keyed by channel and requested model; a row can own
			// several members of this site on this route, so their totals are
			// summed rather than averaged. It is resolved BEFORE the verdict:
			// it is the fallback evidence for a site that publishes none.
			row.Traffic = trafficFor(row.Members, match.Route, traffic, trafficWindowHours())
			row.External = externalFor(site.ID, entry.rawModel, external)
			// The site's own price wins; the catalog only fills a hole.
			if row.ObservedPrice == nil {
				row.CatalogPrice = catalogPriceFor(match.Route, catalog)
			}
			row.Verdict, row.LowStreak, row.OKStreak, row.AvailabilitySource = evaluate(policy, entry.rounds, row.Traffic, row.External, now)
			rows = append(rows, row)
		}
	}
	// Second pass: every route this site serves gets a row, even when the site
	// published nothing about it. Without this the price chain could not reach its
	// last step — "the site has no price for a model we route through it" would
	// simply be invisible — and the model would look unpriced instead of priced by
	// the catalog.
	covered := make(map[string]bool, len(rows))
	for _, row := range rows {
		covered[row.Route] = true
	}
	for _, route := range resolver.RoutesFor(site.ID) {
		if covered[route] {
			continue
		}
		row := Row{
			Route: route, Match: MatchMemberReal, SiteID: site.ID, SiteName: site.Name,
			Rounds: []Round{}, Members: []MemberState{}, SourceKind: site.ProbeSourceKind,
			CatalogPrice: catalogPriceFor(route, catalog),
		}
		for _, member := range resolver.MembersFor(route, site.ID) {
			row.Members = append(row.Members, MemberState{
				MemberID: member.MemberID, ChannelID: member.ChannelID, ChannelName: member.ChannelName,
				GroupName: member.GroupName, Enabled: member.Enabled, AutoDisabled: member.AutoDisabled,
				SingleMember: member.SingleMember, HasPrice: member.HasPrice,
			})
		}
		row.Traffic = trafficFor(row.Members, route, traffic, trafficWindowHours())
		row.External = externalFor(site.ID, route, external)
		row.PriceOnly = row.CatalogPrice != nil
		row.Verdict, row.LowStreak, row.OKStreak, row.AvailabilitySource = evaluate(policy, nil, row.Traffic, row.External, now)
		rows = append(rows, row)
	}
	return rows, candidates
}

// observablePrice normalizes the newest published price into a quote, or nil
// when the site published nothing usable (no price at all, or an expression we
// refuse to evaluate — the latter is still surfaced through the Unparsed flag so
// the raw text can be shown instead of a guessed number).
func observablePrice(rawModel string, rounds []Round) *adapters.PriceQuote {
	for _, round := range rounds {
		if round.Price == nil || round.Price.Mode == "" {
			continue
		}
		return &adapters.PriceQuote{
			Model:               rawModel,
			Mode:                round.Price.Mode,
			Currency:            round.Price.Currency,
			CurrencySymbol:      round.Price.CurrencySymbol,
			InputPerMillion:     round.Price.InputPerMillion,
			OutputPerMillion:    round.Price.OutputPerMillion,
			CacheReadPerMillion: round.Price.CacheReadPerMillion,
			PerRequest:          round.Price.PerRequest,
			GroupRatio:          round.Price.GroupRatio,
			Raw:                 round.Price.Raw,
			Unparsed:            round.Price.Unparsed,
		}
	}
	return nil
}

// evaluate applies the policy to one model's rounds (newest first).
//
// Consecutive is the whole point: one bad round is noise, and a model that a
// site's own probe has written off for two rounds in a row is what we act on.
// Rounds without enough samples break a streak instead of counting as either up
// or down.
// evaluate applies the policy to one model's evidence.
//
// Two kinds of evidence, in this order:
//
//  1. the site's own probe data, which is a series of rounds — consecutive is
//     the whole point there: one bad round is noise, so a streak of them is
//     what we act on.
//  2. our own relay traffic for the same (channel, model), which is already a
//     window of real requests rather than a point sample. Its streak is
//     therefore satisfied by the window itself: requiring two consecutive
//     "rounds" of an aggregate would mean never acting on it at all.
//
// Rounds without enough samples break a streak instead of counting as either up
// or down.
func evaluate(policy Policy, rounds []Round, traffic *TrafficReading, external *ExternalReading, now time.Time) (verdict string, lowStreak, okStreak int, source string) {
	if len(rounds) > 0 {
		if !rounds[0].ObservedAt.IsZero() && now.Sub(rounds[0].ObservedAt) > StaleAfter {
			return VerdictStale, 0, 0, SourceSite
		}
		for _, round := range rounds {
			if round.Samples < policy.MinSamples {
				break
			}
			if round.Ratio >= policy.RatioThreshold {
				if lowStreak > 0 {
					break
				}
				okStreak++
				continue
			}
			if okStreak > 0 {
				break
			}
			lowStreak++
		}
		switch {
		case lowStreak >= policy.LowRounds:
			return VerdictLow, lowStreak, okStreak, SourceSite
		case okStreak >= policy.HighRounds:
			return VerdictOK, lowStreak, okStreak, SourceSite
		case rounds[0].Samples >= policy.MinSamples:
			return VerdictPending, lowStreak, okStreak, SourceSite
		}
	}
	if traffic != nil && traffic.Samples >= policy.MinSamples {
		if traffic.Ratio < policy.RatioThreshold {
			return VerdictLow, 0, 0, SourceTraffic
		}
		return VerdictOK, 0, 0, SourceTraffic
	}
	if external != nil {
		// The directory's number is already an aggregate over its own window,
		// so the sample floor does not apply — but it is third-party evidence
		// and is only consulted when nothing first-hand exists.
		if external.Ratio < policy.RatioThreshold {
			return VerdictLow, 0, 0, SourceExternal
		}
		return VerdictOK, 0, 0, SourceExternal
	}
	if len(rounds) == 0 {
		return VerdictNoData, 0, 0, ""
	}
	return VerdictInsufficient, 0, 0, SourceSite
}

// trafficFor sums our own traffic over the members a row owns.
func trafficFor(members []MemberState, route string, traffic map[string]store.TrafficStat, windowHours int) *TrafficReading {
	if len(members) == 0 || len(traffic) == 0 {
		return nil
	}
	reading := TrafficReading{WindowHours: windowHours}
	for _, member := range members {
		stat, found := traffic[store.TrafficKey(member.ChannelID, route)]
		if !found {
			continue
		}
		reading.Samples += stat.Samples
		reading.Failures += stat.Failures
		if stat.AvgFirstByteMS > 0 {
			reading.AvgFirstByteMS = stat.AvgFirstByteMS
		}
	}
	if reading.Samples == 0 {
		return nil
	}
	reading.Ratio = float64(reading.Samples-reading.Failures) / float64(reading.Samples)
	return &reading
}

// externalIndex loads the third-party snapshot keyed by (site, model core name).
//
// Only readings the directory itself considers fresh are indexed. Measured: its
// snapshot carries pairs it could not collect, with serviceState "no_samples" and
// acquisitionState "stale" and an observedAt days old — and a ratio of 0. Indexing
// those would park a channel because a third party stopped looking at it, which is
// the opposite of the truth. Anything not fresh is simply absent here, so the row
// falls back to "no samples" and nothing acts on it.
func (s *Service) externalIndex() (map[string]ExternalReading, error) {
	rows, err := s.db.ListSiteProbeExternal()
	if err != nil {
		return nil, err
	}
	out := make(map[string]ExternalReading, len(rows))
	for _, row := range rows {
		if !externalReadingIsUsable(row) {
			continue
		}
		out[externalKey(row.SiteID, row.RawModel)] = ExternalReading{
			Source: row.Source, Ratio: row.Ratio, AvgLatencyMS: row.AvgLatencyMS,
			FirstTokenMS: row.FirstTokenMS, TokensPerSecond: row.TokensPerSecond,
			ServiceState: row.ServiceState, AcquisitionState: row.AcquisitionState,
			ObservedAt: row.ObservedAt,
		}
	}
	return out, nil
}

// externalReadingIsUsable is the freshness gate for third-party evidence.
func externalReadingIsUsable(row store.SiteProbeExternal) bool {
	switch strings.ToLower(strings.TrimSpace(row.AcquisitionState)) {
	case "fresh":
	default:
		// stale | collection_failed | login_expired | challenge_pending |
		// challenge_failed | unknown: the directory is not currently seeing this
		// pair, which says nothing about the site.
		return false
	}
	switch strings.ToLower(strings.TrimSpace(row.ServiceState)) {
	case "healthy", "degraded", "failed":
	default:
		// no_samples | empty: no measurement to report.
		return false
	}
	return true
}

// externalFor looks a reading up by site and published model name. Several
// directory rows can describe the same model in different groups; the best of
// them is used, because a model that is healthy in any group is reachable.
func externalFor(siteID int64, rawModel string, external map[string]ExternalReading) *ExternalReading {
	if len(external) == 0 {
		return nil
	}
	key := externalKey(siteID, rawModel)
	reading, found := external[key]
	if !found {
		return nil
	}
	return &reading
}

func externalKey(siteID int64, rawModel string) string {
	return fmt.Sprintf("%d|%s", siteID, coreModelName(rawModel))
}

func trafficWindowHours() int {
	window := TrafficWindow
	if window <= 0 {
		window = 24 * time.Hour
	}
	return int(window.Hours())
}

// readingsArePriceOnly reports a row that carries prices but no availability —
// a New-API price source with no probe page. Such rows are informational.
func readingsArePriceOnly(rounds []Round) bool {
	if len(rounds) == 0 {
		return false
	}
	for _, round := range rounds {
		if round.Samples > 0 {
			return false
		}
	}
	return true
}

// Action is one routing change the policy asked for.
type Action struct {
	Route        string `json:"route"`
	SiteID       int64  `json:"site_id"`
	SiteName     string `json:"site_name"`
	ChannelID    int64  `json:"channel_id"`
	ChannelName  string `json:"channel_name"`
	Kind         string `json:"kind"` // disable | recover
	Reason       string `json:"reason"`
	MembersMoved int    `json:"members_moved"`
	Skipped      string `json:"skipped,omitempty"`
}

// ApplyRequest is one evaluation run. DryRun computes the identical action list
// without touching a single member, which is what the tool shows first.
type ApplyRequest struct {
	Routes  []string `json:"routes,omitempty"`
	Policy  Policy   `json:"policy"`
	DryRun  bool     `json:"dry_run"`
	SiteIDs []int64  `json:"site_ids,omitempty"`
}

// Apply turns the report's verdicts into member state changes.
//
// It only ever calls the two existing store actions (AutoDisableByPair /
// AutoRecoverByPair), which carry the guarantees the rest of the gateway relies
// on: a member pinned as a route's single member is never disabled (a
// preventive system must not be able to make a model unreachable), and a member
// an operator disabled by hand is never recovered.
func (s *Service) Apply(ctx context.Context, req ApplyRequest) ([]Action, error) {
	policy := req.Policy.withDefaults()
	report, err := s.Report(policy)
	if err != nil {
		return nil, err
	}
	wanted := make(map[string]bool, len(req.Routes))
	for _, route := range req.Routes {
		wanted[strings.TrimSpace(route)] = true
	}
	sites := make(map[int64]bool, len(req.SiteIDs))
	for _, id := range req.SiteIDs {
		sites[id] = true
	}

	actions := []Action{}
	for _, row := range report.Rows {
		if ctx.Err() != nil {
			return actions, ctx.Err()
		}
		if len(wanted) > 0 && !wanted[row.Route] {
			continue
		}
		if len(sites) > 0 && !sites[row.SiteID] {
			continue
		}
		kind := ""
		switch row.Verdict {
		case VerdictLow:
			kind = "disable"
		case VerdictOK:
			kind = "recover"
		default:
			continue
		}
		newest := row.Rounds[0]
		for _, member := range row.Members {
			if kind == "disable" && !member.Enabled {
				continue
			}
			if kind == "recover" && !member.AutoDisabled {
				continue
			}
			reason := describeAction(kind, member, row, newest, policy)
			action := Action{
				Route: row.Route, SiteID: row.SiteID, SiteName: row.SiteName,
				ChannelID: member.ChannelID, ChannelName: member.ChannelName, Kind: kind, Reason: reason,
			}
			if member.SingleMember && kind == "disable" {
				action.Skipped = "single_member"
				actions = append(actions, action)
				continue
			}
			if req.DryRun {
				actions = append(actions, action)
				continue
			}
			if kind == "disable" {
				moved, err := s.db.RouteMember.AutoDisableByPair(member.ChannelID, row.Route, reason)
				if err != nil {
					return actions, err
				}
				action.MembersMoved = moved
				if moved == 0 {
					action.Skipped = "not_disableable"
				}
			} else {
				moved, err := s.db.RouteMember.AutoRecoverByPair(member.ChannelID, row.Route)
				if err != nil {
					return actions, err
				}
				action.MembersMoved = moved
				if moved == 0 {
					action.Skipped = "nothing_to_recover"
				}
			}
			actions = append(actions, action)
		}
	}
	return actions, nil
}

// describeAction writes the reason that lands in route_members.last_error, which
// is what an operator sees later in the routing view.
func describeAction(kind string, member MemberState, row Row, newest Round, policy Policy) string {
	// The reason is what an operator reads later in the routing view, so it has
	// to say WHICH evidence moved the member: a site's own claim and our own
	// relay's record are different statements about the same channel.
	var readings string
	switch {
	case row.AvailabilitySource == SourceTraffic && row.Traffic != nil:
		readings = fmt.Sprintf("our traffic %.0f%% (%d/%d in %dh)",
			row.Traffic.Ratio*100, row.Traffic.Samples-row.Traffic.Failures, row.Traffic.Samples, row.Traffic.WindowHours)
	case row.AvailabilitySource == SourceExternal && row.External != nil:
		readings = fmt.Sprintf("third-party %.0f%% (%s)", row.External.Ratio*100, row.External.Source)
	default:
		readings = fmt.Sprintf("%.0f%% (%d/%d samples)", newest.Ratio*100, newest.UpCount, newest.Samples)
	}
	if kind == "disable" {
		reason := "site probe: "
		switch row.AvailabilitySource {
		case SourceTraffic:
			reason += fmt.Sprintf("%s below %.0f%% on %s", readings, policy.RatioThreshold*100, row.SiteName)
		case SourceExternal:
			reason += fmt.Sprintf("%s below %.0f%% on %s [third-party, not our measurement]",
				readings, policy.RatioThreshold*100, row.SiteName)
		default:
			reason += fmt.Sprintf("%d rounds below %.0f%% — %s on %s",
				policy.LowRounds, policy.RatioThreshold*100, readings, row.SiteName)
		}
		if newest.WeakEvidence && row.AvailabilitySource == SourceSite {
			reason += " [endpoint-only monitor]"
		}
		if member.GroupName != "" {
			reason += fmt.Sprintf(" [group %s]", member.GroupName)
		}
		return reason
	}
	return fmt.Sprintf("site probe: recovered, %s on %s", readings, row.SiteName)
}

// CatalogPrices converts the model catalog's per-1k prices into the per-1M USD
// shape probe rows use, keyed by the catalog's own model name.
//
// It exists for one case: a site publishes prices for the models it is proud of
// and nothing for the rest, and an operator looking at "no price" wants to know
// what that model costs in general. The catalog's numbers are already the
// gateway's billing fallback (billing reads them when a member has none), so this
// surfaces what would be charged rather than adding a price source.
func (s *Service) CatalogPrices() (map[string]adapters.PriceQuote, error) {
	models, err := s.db.ModelMetadata.List()
	if err != nil {
		return nil, err
	}
	out := make(map[string]adapters.PriceQuote, len(models))
	for _, model := range models {
		if model.PricePromptPer1k <= 0 && model.PriceCompletionPer1k <= 0 &&
			model.PriceCachePer1k <= 0 && model.PricePerRequest <= 0 {
			continue
		}
		quote := adapters.PriceQuote{
			Model: model.ModelName, Mode: "token", Currency: "USD", CurrencySymbol: "$",
			// Stored per 1k, shown per 1M like every other published price.
			InputPerMillion:     model.PricePromptPer1k * 1000,
			OutputPerMillion:    model.PriceCompletionPer1k * 1000,
			CacheReadPerMillion: model.PriceCachePer1k * 1000,
		}
		if quote.InputPerMillion == 0 && model.PricePerRequest > 0 {
			quote.Mode = "fixed"
			quote.PerRequest = model.PricePerRequest
		}
		out[model.ModelName] = quote
	}
	return out, nil
}

// catalogPriceFor finds the reference price for a route. The route pattern is the
// catalog's model name (both are the gateway's own naming), so a wildcard route
// has no single price and gets none.
func catalogPriceFor(route string, catalog map[string]adapters.PriceQuote) *adapters.PriceQuote {
	if len(catalog) == 0 || route == "" || strings.ContainsAny(route, "*?") {
		return nil
	}
	quote, found := catalog[route]
	if !found {
		return nil
	}
	return &quote
}

// priceSamples converts a public price snapshot into storable samples. They
// carry no availability (Samples stays 0), so they can never trigger a disable.
func priceSamples(siteID, runID int64, observedAt time.Time, snapshot *PriceSnapshot) []store.SiteProbeSample {
	models := make([]string, 0, len(snapshot.Quotes))
	for model := range snapshot.Quotes {
		models = append(models, model)
	}
	sort.Strings(models)
	out := make([]store.SiteProbeSample, 0, len(models))
	for _, model := range models {
		quote := snapshot.Quotes[model]
		out = append(out, store.SiteProbeSample{
			RunID: runID, SiteID: siteID, MonitorName: model, MonitorType: MonitorTypePrice,
			RawModel: model, ObservedAt: observedAt,
			Price: store.SiteProbePrice{
				Mode: quote.Mode, Currency: quote.Currency, CurrencySymbol: quote.CurrencySymbol,
				InputPerMillion: quote.InputPerMillion, OutputPerMillion: quote.OutputPerMillion,
				CacheReadPerMillion: quote.CacheReadPerMillion, PerRequest: quote.PerRequest,
				GroupRatio: quote.GroupRatio, Raw: quote.Raw, Unparsed: quote.Unparsed,
			},
		})
	}
	return out
}

// QuoteToMemberPrices converts a normalized quote into the per-1k/per-call units
// the billing columns use. Returning ok=false keeps a quote that cannot be
// converted out of the billing layer: an unparsed expression, a missing price,
// or — the case that matters here — a price in a currency other than USD. The
// billing columns are dollars per 1k tokens, and writing a ¥ amount into them
// would silently under- or over-charge every request, so a non-USD quote is
// simply not adoptable.
func QuoteToMemberPrices(quote adapters.PriceQuote) (prompt, completion, cache, perRequest float64, ok bool) {
	if quote.Unparsed {
		return 0, 0, 0, 0, false
	}
	if currency := strings.ToUpper(strings.TrimSpace(quote.Currency)); currency != "" && currency != "USD" {
		return 0, 0, 0, 0, false
	}
	switch quote.Mode {
	case "fixed":
		if quote.PerRequest <= 0 {
			return 0, 0, 0, 0, false
		}
		return 0, 0, 0, quote.PerRequest, true
	default:
		if quote.InputPerMillion <= 0 {
			return 0, 0, 0, 0, false
		}
		// Stored prices are per 1k tokens; published ones are per 1M.
		return quote.InputPerMillion / 1000, quote.OutputPerMillion / 1000,
			quote.CacheReadPerMillion / 1000, 0, true
	}
}

// AdoptPricesResult reports what adopting an observed price changed.
type AdoptPricesResult struct {
	MemberID    int64    `json:"member_id"`
	ChannelName string   `json:"channel_name"`
	Adopted     []string `json:"adopted,omitempty"`
	// Skipped explains why nothing was written (no price, unparsed expression,
	// or the member already carries every field the quote could fill).
	Skipped string `json:"skipped,omitempty"`
}

// AdoptPrices writes a site's published price into the members the operator
// picked.
//
// The billing layers are "most specific wins": a value on the member silently
// outranks the model catalog and the observed price alike. That is exactly why
// this is an operator action and never an automatic one, and why the store
// helper only fills fields that are still empty — a price someone typed must not
// be replaced by whatever a status page published.
func (s *Service) AdoptPrices(memberIDs []int64, quote *adapters.PriceQuote) ([]AdoptPricesResult, error) {
	if len(memberIDs) == 0 {
		return nil, nil
	}
	if quote == nil {
		return nil, fmt.Errorf("no observed price for this model")
	}
	prompt, completion, cache, perRequest, ok := QuoteToMemberPrices(*quote)
	if !ok {
		return nil, fmt.Errorf("the published price cannot be converted: %s", quote.Raw)
	}
	results := make([]AdoptPricesResult, 0, len(memberIDs))
	for _, memberID := range memberIDs {
		result := AdoptPricesResult{MemberID: memberID}
		adopted, err := s.db.RouteMember.AdoptObservedPrices(memberID, prompt, completion, cache, perRequest)
		if err != nil {
			return results, err
		}
		result.Adopted = adopted
		if len(adopted) == 0 {
			result.Skipped = "already_priced"
		}
		results = append(results, result)
	}
	return results, nil
}

package httpapi

import (
	"context"
	"errors"
	"log"
	"net/http"
	neturl "net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/siteprobe"
	"github.com/lan/meta-gateway/internal/store"
)

// SiteProbeHandler exposes the external site probe source: the public probe
// data a site publishes itself, which lets the gateway judge availability
// without spending upstream tokens.
type SiteProbeHandler struct {
	db        *store.DB
	service   *siteprobe.Service
	scheduler *siteprobe.Scheduler
}

func NewSiteProbeHandler(db *store.DB, service *siteprobe.Service, scheduler *siteprobe.Scheduler) *SiteProbeHandler {
	return &SiteProbeHandler{db: db, service: service, scheduler: scheduler}
}

func (h *SiteProbeHandler) Register(r chi.Router) {
	r.Get("/site-probe/report", h.report)
	r.Post("/site-probe/catalog/import", h.catalogImport)
	r.Post("/site-probe/catalog/prune", h.catalogPrune)
	r.Post("/site-probe/collect", h.collect)
	r.Post("/site-probe/apply", h.apply)
	r.Post("/site-probe/adopt-price", h.adoptPrice)
	r.Post("/site-probe/detect", h.detect)
	r.Put("/site-probe/source", h.saveSource)
	r.Delete("/site-probe/source/{siteId}", h.clearSource)
}

// report returns the collected readings joined with our routes. The policy
// knobs travel as query parameters so the dialog can preview a different
// threshold without saving anything.
func (h *SiteProbeHandler) report(w http.ResponseWriter, r *http.Request) {
	policy := siteprobe.DefaultPolicy()
	if raw := strings.TrimSpace(r.URL.Query().Get("ratio_threshold")); raw != "" {
		if value, err := strconv.ParseFloat(raw, 64); err == nil {
			policy.RatioThreshold = value
		}
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("min_samples")); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil {
			policy.MinSamples = value
		}
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("low_rounds")); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil {
			policy.LowRounds = value
		}
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("high_rounds")); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil {
			policy.HighRounds = value
		}
	}
	report, err := h.service.Report(policy)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

type siteProbeCollectRequest struct {
	SiteIDs []int64 `json:"site_ids,omitempty"`
}

// collect runs a round now. With no site ids it collects every enabled site,
// which is exactly what the background loop does.
func (h *SiteProbeHandler) collect(w http.ResponseWriter, r *http.Request) {
	var request siteProbeCollectRequest
	if err := decodeJSON(w, r, &request, 0, false); err != nil {
		return
	}
	// A round is a handful of public GETs per site, and one slow site can hold a
	// connection for the client timeout: the budget has to cover the whole fleet,
	// or the round is cut off mid-way and the later steps (the third-party
	// snapshot, the auto-apply pass) never run. It matches the loop's own budget.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Minute)
	defer cancel()

	runs := []*store.SiteProbeRun{}
	failed := 0
	if len(request.SiteIDs) == 0 {
		result, err := h.scheduler.CollectNow(ctx)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, result)
		return
	}
	for _, id := range request.SiteIDs {
		site, err := h.db.Site.GetByID(id)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		if site == nil {
			writeError(w, http.StatusNotFound, "site not found")
			return
		}
		run, err := h.service.CollectSite(ctx, *site)
		if err != nil {
			// "This site has no readable probe source" is a fact about the site, not
			// a database failure: routing it through writeStoreError answered 500
			// "database operation failed", which sent the operator looking for a
			// broken database instead of an empty source.
			writeError(w, http.StatusConflict, "site probe: "+err.Error())
			return
		}
		if run != nil {
			runs = append(runs, run)
			if run.Status == store.SiteProbeRunFailed {
				failed++
			}
		}
	}
	// A round is also the moment the opted-in sites act, so a manual collect
	// does not leave their verdicts waiting for the scheduler.
	actions, err := h.service.AutoApplySites(ctx, request.SiteIDs)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs, "failed": failed, "actions": actions})
}

// adoptPrice writes a site's published price into the billing layer for the
// members the operator picked. The quote is NOT taken from the request: it comes
// from the latest collected sample for the row that member belongs to, so a
// client cannot write an invented number through this endpoint.
func (h *SiteProbeHandler) adoptPrice(w http.ResponseWriter, r *http.Request) {
	var request struct {
		MemberIDs []int64 `json:"member_ids"`
	}
	if err := decodeJSON(w, r, &request, 0, false); err != nil {
		return
	}
	if len(request.MemberIDs) == 0 {
		writeError(w, http.StatusBadRequest, "missing member_ids")
		return
	}
	report, err := h.service.Report(siteprobe.DefaultPolicy())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	// One row per member: members of the same (site, model) share a quote, so
	// they are adopted together and reported per member.
	rowByMember := make(map[int64]*siteprobe.Row, len(request.MemberIDs))
	for index := range report.Rows {
		row := &report.Rows[index]
		if row.ObservedPrice == nil {
			continue
		}
		for _, member := range row.Members {
			rowByMember[member.MemberID] = row
		}
	}
	groupedByRow := make(map[*siteprobe.Row][]int64, len(request.MemberIDs))
	noPrice := make([]int64, 0)
	for _, memberID := range request.MemberIDs {
		if row, ok := rowByMember[memberID]; ok {
			groupedByRow[row] = append(groupedByRow[row], memberID)
		} else {
			noPrice = append(noPrice, memberID)
		}
	}
	results := make([]siteprobe.AdoptPricesResult, 0, len(request.MemberIDs))
	for _, memberID := range noPrice {
		results = append(results, siteprobe.AdoptPricesResult{MemberID: memberID, Skipped: "no_price"})
	}
	for row, members := range groupedByRow {
		adopted, err := h.service.AdoptPrices(members, row.ObservedPrice)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		results = append(results, adopted...)
	}
	sort.Slice(results, func(i, j int) bool { return results[i].MemberID < results[j].MemberID })
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

// apply evaluates the policy over the collected data. dry_run returns exactly
// what would change without touching a member, which is what the dialog shows
// before the operator confirms.
func (h *SiteProbeHandler) apply(w http.ResponseWriter, r *http.Request) {
	var request siteprobe.ApplyRequest
	if err := decodeJSON(w, r, &request, 0, false); err != nil {
		return
	}
	actions, err := h.service.Apply(r.Context(), request)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"actions": actions, "dry_run": request.DryRun})
}

type siteProbeDetectRequest struct {
	URL string `json:"url"`
}

type siteProbeDetection struct {
	Kind string `json:"kind"`
	URL  string `json:"url"`
	Base string `json:"base,omitempty"`
	Slug string `json:"slug,omitempty"`
	// Title is the status page's own title, shown so the operator can confirm
	// they pasted the page they meant.
	Title string `json:"title,omitempty"`
	// Groups and Monitors preview what the source would contribute.
	Groups        []siteprobe.KumaGroup   `json:"groups,omitempty"`
	Monitors      []siteprobe.KumaMonitor `json:"monitors,omitempty"`
	PriceCount    int                     `json:"price_count,omitempty"`
	PriceUnparsed int                     `json:"price_unparsed,omitempty"`
	// Sub2API transit: which snapshot shape the site serves (v1 own probes, v2
	// real-traffic aggregates) and what it currently reports.
	TransitMode     string                  `json:"transit_mode,omitempty"`
	TransitHomepage string                  `json:"transit_homepage,omitempty"`
	TransitReadings []siteprobe.KumaMonitor `json:"transit_readings,omitempty"`
}

// detect identifies which public source a URL points at and previews what it
// offers, so the operator can see the data before saving any configuration.
func (h *SiteProbeHandler) detect(w http.ResponseWriter, r *http.Request) {
	var request siteProbeDetectRequest
	if err := decodeJSON(w, r, &request, 0, false); err != nil {
		return
	}
	url := strings.TrimSpace(request.URL)
	if url == "" {
		writeError(w, http.StatusBadRequest, "missing url")
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 30*time.Second)
	defer cancel()
	// The service's client carries the configured-proxy hook; an ad-hoc client
	// would inherit the environment's proxy and die the same death the
	// collector just recovered from.
	client := h.service.HTTPClient()

	// Uptime Kuma first: its URL shape is distinctive, and a status page that
	// answers must not be misread as a price table.
	if base, slug, err := siteprobe.ResolveKumaSource(url); err == nil {
		if source, fetchErr := siteprobe.FetchKuma(ctx, client, url, 0); fetchErr == nil {
			writeJSON(w, http.StatusOK, siteProbeDetection{
				Kind: siteprobe.SourceUptimeKuma, URL: url, Base: base, Slug: slug,
				Title: source.Title, Groups: source.Groups, Monitors: source.Monitors,
			})
			return
		}
	}
	// Sub2API public transit next: the discovery file lives at the host root,
	// which is where the protocol defines it, whatever page was pasted.
	if discovery, snapshotURL, err := siteprobe.ResolveTransitDiscovery(ctx, client, url); err == nil {
		if transit, mode, fetchErr := siteprobe.FetchTransit(ctx, client, snapshotURL); fetchErr == nil {
			availability := siteprobe.TransitAvailability(transit)
			readings := make([]siteprobe.KumaMonitor, 0, len(availability))
			for _, reading := range availability {
				readings = append(readings, reading)
			}
			sort.Slice(readings, func(i, j int) bool { return readings[i].Name < readings[j].Name })
			writeJSON(w, http.StatusOK, siteProbeDetection{
				Kind: siteprobe.SourceSub2APITransit, URL: url, Base: snapshotURL,
				Title: discovery.SchemaVersion, TransitMode: mode,
				TransitHomepage: discovery.HomepageURL, TransitReadings: readings,
			})
			return
		}
	}
	snapshot, err := siteprobe.FetchPublicPrices(ctx, client, url)
	if err != nil {
		writeError(w, http.StatusBadGateway, "not a recognised public probe source: "+err.Error())
		return
	}
	base, _ := siteprobe.ResolveNewAPIBase(url)
	writeJSON(w, http.StatusOK, siteProbeDetection{
		Kind: siteprobe.SourceNewAPI, URL: url, Base: base,
		PriceCount: len(snapshot.Quotes), PriceUnparsed: snapshot.Unparsed,
	})
}

// fetchCatalog reads the public monitoring directory named by the request. The
// URL is required: an import that silently fell back to a different directory
// than the one the operator's button names would be a nasty surprise.
func (h *SiteProbeHandler) fetchCatalog(w http.ResponseWriter, r *http.Request, bodyURL string) ([]siteprobe.CatalogEntry, bool) {
	url := strings.TrimSpace(bodyURL)
	if url == "" {
		url = strings.TrimSpace(r.URL.Query().Get("url"))
	}
	if url == "" {
		writeError(w, http.StatusBadRequest, "missing catalog url")
		return nil, false
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 60*time.Second)
	defer cancel()
	entries, err := siteprobe.FetchCatalog(ctx, h.service.HTTPClient(), url)
	if err != nil {
		writeError(w, http.StatusBadGateway, "catalog unreadable: "+err.Error())
		return nil, false
	}
	return entries, true
}

type siteProbeCatalogImportRequest struct {
	URL string `json:"url,omitempty"`
	// Names limits the import to specific entries; empty means every entry.
	Names []string `json:"names,omitempty"`
	// CollectNow immediately reads every imported site once (default true), so
	// the operator sees real readings after the single click instead of having
	// to press anything else.
	CollectNow *bool `json:"collect_now,omitempty"`
	// CreateMissing also creates sites for catalog entries we route nothing
	// through. Off by default: probe data for a site with no channel is not
	// useful, and importing the whole directory would fill the site list with
	// entries nobody asked for.
	CreateMissing bool `json:"create_missing,omitempty"`
}

// catalogImport attaches probe sources to the sites we already have.
//
// The directory is a source of *addresses*, not of site records: matching is
// what makes the import useful, and creating a site for every directory entry
// would leave the console full of sites nobody routes through (there is no site
// management screen, so they could not even be removed by hand).
func (h *SiteProbeHandler) catalogImport(w http.ResponseWriter, r *http.Request) {
	var request siteProbeCatalogImportRequest
	if err := decodeJSON(w, r, &request, 0, false); err != nil {
		return
	}
	entries, ok := h.fetchCatalog(w, r, request.URL)
	if !ok {
		return
	}
	// The single-click contract: import the entries and read them right away.
	// The only reason to skip collection is a directory so large the HTTP
	// request would time out; the scheduled round picks those up anyway.
	collectNow := request.CollectNow == nil || *request.CollectNow
	sites, err := h.db.ListProbeSites()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	index := newSiteMatcher(sites)
	wanted := make(map[string]bool, len(request.Names))
	for _, name := range request.Names {
		wanted[strings.TrimSpace(name)] = true
	}

	matched, skipped, unmatched, created := 0, 0, 0, 0
	touched := make([]int64, 0, len(entries))
	for _, entry := range entries {
		if len(wanted) > 0 && !wanted[entry.Name] {
			continue
		}
		site, found := index.match(entry.URL)
		if !found {
			if !request.CreateMissing {
				unmatched++
				continue
			}
			siteID, err := h.db.Site.Create(&domain.Site{
				Name: entry.Name, BaseURL: strings.TrimRight(entry.URL, "/"),
				Platform: platformForEntry(entry), Status: domain.StatusEnabled,
			})
			if err != nil {
				writeStoreError(w, err)
				return
			}
			createdSite, err := h.db.Site.GetByID(siteID)
			if err != nil || createdSite == nil {
				writeStoreError(w, err)
				return
			}
			site = createdSite
			created++
		} else if hasCustomProbeSource(*site) {
			// A source the operator typed is never overwritten by a directory.
			skipped++
			continue
		}
		if err := h.applyCatalogEntry(*site, entry); err != nil {
			writeStoreError(w, err)
			return
		}
		matched++
		touched = append(touched, site.ID)
	}

	collected, failed := 0, 0
	if collectNow {
		collected, failed = h.collectSites(r, touched)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"matched": matched, "skipped": skipped, "unmatched": unmatched,
		"created": created, "collected": collected, "failed": failed,
	})
}

// applyCatalogEntry writes one imported source onto an existing site. A
// status-page entry carries a usable URL, so it becomes an explicit source; a
// pricing-shaped entry relies on the platform derivation, and falls back to an
// explicit new-api source when the site's platform is unknown — otherwise the
// import would report a match that could never collect anything.
func (h *SiteProbeHandler) applyCatalogEntry(site domain.Site, entry siteprobe.CatalogEntry) error {
	if entry.Kind == siteprobe.SourceUptimeKuma {
		return h.db.UpdateSiteProbeSourceWithAuto(site.ID, entry.Kind, entry.URL, "{}", true, false)
	}
	if _, _, ok := siteprobe.AutoSource(site.Platform, site.BaseURL); ok {
		return h.db.UpdateSiteProbeSourceWithAuto(site.ID, "", "", "{}", true, true)
	}
	return h.db.UpdateSiteProbeSourceWithAuto(site.ID, siteprobe.SourceNewAPI, site.BaseURL, "{}", true, false)
}

// collectSites reads the sites an import just touched, and only those: a global
// round would also hammer every other configured site for a request the
// operator made about the directory.
//
// Every failure is logged with its reason. The counts alone ("0 collected, 12
// failed") are what the operator sees, and without a line here the reason for a
// whole round failing exists nowhere: the collector only logs the failures it
// reaches itself, so a failure BEFORE it (a site row that cannot be read, a run
// row that cannot be written) would be counted and then vanish.
func (h *SiteProbeHandler) collectSites(r *http.Request, siteIDs []int64) (int, int) {
	if len(siteIDs) == 0 {
		return 0, 0
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Minute)
	defer cancel()
	collected, failed := 0, 0
	for _, siteID := range siteIDs {
		if ctx.Err() != nil {
			log.Printf("site probe: import round stopped early: %v", ctx.Err())
			break
		}
		site, err := h.db.Site.GetByID(siteID)
		if err != nil || site == nil {
			failed++
			log.Printf("site probe: import round cannot read site %d: site=%v err=%v", siteID, site, err)
			continue
		}
		run, err := h.service.CollectSite(ctx, *site)
		if err != nil {
			failed++
			log.Printf("site probe: import round failed for site %d (%s): %v", siteID, site.Name, err)
			continue
		}
		if run != nil && run.Status == store.SiteProbeRunFailed {
			failed++
			continue
		}
		collected++
	}
	return collected, failed
}

// catalogPrune removes the sites nothing routes through — the debris an import
// that created missing sites would leave behind. Only sites with no channels AND
// no credentials are touched, and the guard is re-checked inside the delete.
func (h *SiteProbeHandler) catalogPrune(w http.ResponseWriter, r *http.Request) {
	sites, err := h.db.ListUnusedSites()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	removed := 0
	for _, site := range sites {
		ok, err := h.db.DeleteUnusedSite(site.ID)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		if ok {
			removed++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"removed": removed})
}

// siteMatcher ties a directory entry's host to a local site. Status pages often
// live on a subdomain of the site itself (stat.example.com for example.com), so
// a suffix match at a label boundary counts; the most specific match wins.
// Matching on the registrable domain needs no public-suffix list this way.
type siteMatcher struct {
	sites []domain.Site
}

func newSiteMatcher(sites []domain.Site) siteMatcher {
	return siteMatcher{sites: sites}
}

func (m siteMatcher) match(rawURL string) (*domain.Site, bool) {
	host := hostOf(rawURL)
	if host == "" {
		return nil, false
	}
	bestScore, bestHostLen, bestIndex := 0, 0, -1
	for index, site := range m.sites {
		siteHost := hostOf(site.BaseURL)
		if siteHost == "" {
			continue
		}
		score := hostMatchScore(siteHost, host)
		if score == 0 {
			continue
		}
		// The longest matching host wins, so api.example.com beats example.com.
		if score > bestScore || (score == bestScore && len(siteHost) > bestHostLen) {
			bestScore, bestHostLen, bestIndex = score, len(siteHost), index
		}
	}
	if bestIndex < 0 {
		return nil, false
	}
	return &m.sites[bestIndex], true
}

// hostMatchScore: 2 = same host, 1 = one host is a subdomain of the other.
func hostMatchScore(a, b string) int {
	if a == b {
		return 2
	}
	if strings.HasSuffix(a, "."+b) || strings.HasSuffix(b, "."+a) {
		return 1
	}
	return 0
}

func hostOf(rawURL string) string {
	parsed, err := neturl.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed.Hostname())
}

// hasCustomProbeSource reports a source the operator owns: a hand-typed URL, or
// auto mode turned off with a kind set. A directory must not overwrite either.
func hasCustomProbeSource(site domain.Site) bool {
	return strings.TrimSpace(site.ProbeSourceURL) != "" ||
		(!site.ProbeAuto && strings.TrimSpace(site.ProbeSourceKind) != "")
}

// platformForEntry guesses the platform column for a new site from its URL
// shape. It is a default, not a verdict: the channel-add flow's detector stays
// the authority, and the platform only matters here for auto-source derivation.
func platformForEntry(entry siteprobe.CatalogEntry) string {
	if entry.Kind == siteprobe.SourceUptimeKuma {
		return "new-api"
	}
	return "new-api"
}

type siteProbeSourceRequest struct {
	SiteID  int64  `json:"site_id"`
	Kind    string `json:"kind"`
	URL     string `json:"url"`
	Enabled bool   `json:"enabled"`
	Config  string `json:"config"`
	// Auto marks a source derived from the site's platform instead of a
	// hand-typed URL. Incompatible with URL: saving a URL always clears it.
	Auto bool `json:"auto"`
}

// saveSource writes the probe-source columns of one site. It is a dedicated
// endpoint (rather than the site form) so a site edit elsewhere can never blank
// the probe configuration.
func (h *SiteProbeHandler) saveSource(w http.ResponseWriter, r *http.Request) {
	var request siteProbeSourceRequest
	if err := decodeJSON(w, r, &request, 0, false); err != nil {
		return
	}
	if request.SiteID <= 0 {
		writeError(w, http.StatusBadRequest, "missing site_id")
		return
	}
	kind, url := strings.TrimSpace(request.Kind), strings.TrimSpace(request.URL)
	if url != "" {
		// A hand-typed URL is an explicit source: auto mode is the absence of
		// one. Letting both stick would make "which source ran" ambiguous.
		request.Auto = false
	}
	if err := validateProbeSource(kind, url); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	site, err := h.db.Site.GetByID(request.SiteID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if site == nil {
		writeError(w, http.StatusNotFound, "site not found")
		return
	}
	config := strings.TrimSpace(request.Config)
	if config == "" {
		config = "{}"
	}
	// The config blob is confined to a schema the reader understands: an
	// unknown key here would otherwise sit in the database looking meaningful
	// while the collector ignores it.
	if err := siteprobe.ValidateSourceConfig(config); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.db.UpdateSiteProbeSourceWithAuto(request.SiteID, kind, url, config, request.Enabled && (kind != "" || request.Auto), request.Auto); err != nil {
		writeStoreError(w, err)
		return
	}
	// UpdateSiteProbeSource drops the site cache; read the row back so the
	// caller sees exactly what was stored.
	updated, err := h.db.Site.GetByID(request.SiteID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *SiteProbeHandler) clearSource(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "siteId")
	if !ok {
		return
	}
	if err := h.db.UpdateSiteProbeSource(id, "", "", "{}", false); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"site_id": id, "cleared": true})
}

// validateProbeSource rejects a configuration that cannot work before it is
// stored: a kind with no URL, an unknown kind, or a URL that is not shaped like
// the source it claims to be.
func validateProbeSource(kind, url string) error {
	switch kind {
	case "":
		if url != "" {
			return errors.New("pick a source type for that url")
		}
		return nil
	case siteprobe.SourceUptimeKuma:
		if url == "" {
			return errors.New("missing probe source url")
		}
		if _, _, err := siteprobe.ResolveKumaSource(url); err != nil {
			return err
		}
		return nil
	case siteprobe.SourceNewAPI:
		if url == "" {
			return errors.New("missing probe source url")
		}
		if _, err := siteprobe.ResolveNewAPIBase(url); err != nil {
			return err
		}
		return nil
	case siteprobe.SourceSub2APITransit:
		if url == "" {
			return errors.New("missing probe source url")
		}
		// The snapshot is discovered at request time, so only the URL shape can
		// be checked here; a host without the discovery file fails the round
		// (and the detect preview tells the operator before they save).
		parsed, parseErr := neturl.Parse(strings.TrimSpace(url))
		if parseErr != nil || parsed.Host == "" {
			return errors.New("invalid probe source url")
		}
		return nil
	default:
		return errors.New("unsupported probe source kind")
	}
}

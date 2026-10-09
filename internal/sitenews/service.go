// Package sitenews reads what an upstream site publishes on its own notice
// board and keeps it for the console.
//
// It is the operator-facing counterpart of internal/siteprobe: both ask a site
// for a public page and neither spends an upstream token or a login. What a
// site says about itself — a channel going down, a group being renamed, an
// account being killed — is the one piece of context the gateway cannot infer
// from traffic, and it arrives before the failure does.
package sitenews

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/siteprobe"
	"github.com/lan/meta-gateway/internal/store"
)

const (
	// DefaultInterval is the shipped cadence. A notice board changes when a site
	// has something to say — hours apart at most — so this is fast enough to
	// count as live, and the console's refresh button covers "now".
	DefaultInterval = 5 * time.Minute
	// fetchTimeout bounds one site's /api/status. Announcement boards ride along
	// the same endpoint as the site's other public config, which is a small
	// document; anything slower is a site in trouble, not a slow answer.
	fetchTimeout = 12 * time.Second
	// roundTimeout bounds a whole refresh so a dead fleet cannot pin the manual
	// button (or a scheduled round) open indefinitely.
	roundTimeout = 2 * time.Minute
	// concurrency is how many sites are read at once. Small on purpose: the
	// fleet is a handful of volunteer-run sites, and a refresh is not urgent.
	concurrency = 6
	// maxPerSite caps what one site can contribute in a single fetch, so a board
	// that returns thousands of rows cannot turn into thousands of inserts.
	maxPerSite = 120
	// maxErrors caps the error list a refresh reports; the count stays complete.
	maxErrors = 8
)

// ErrBusy is returned when a refresh is already running.
var ErrBusy = errors.New("site news: a refresh is already running")

// Service reads the boards and owns the stored feed.
type Service struct {
	db     *store.DB
	client *http.Client
	logger *slog.Logger
	now    func() time.Time

	// refreshing serialises rounds: two concurrent refreshes would read the same
	// boards twice and race on the same rows for no benefit.
	refreshing sync.Mutex
}

// NewService builds the reader. The HTTP client is separate from the relay's
// outbound client on purpose (probe pages must not affect request policy), and
// proxyHook is how it picks up the console-configured global proxy — it must
// NOT fall back to the environment, for the reason spelled out in
// siteprobe.NewService: the container inherits the host's HTTP_PROXY, which
// points at the container itself and fails every fetch.
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
		client: &http.Client{Timeout: fetchTimeout, Transport: transport},
		logger: logger,
		now:    func() time.Time { return time.Now().UTC() },
	}
}

// SiteCoverage says what the feed covers, so the console can tell "no site has
// spoken" apart from "no site can be read".
type SiteCoverage struct {
	// Readable is how many configured sites publish a board this gateway knows
	// how to read (New-API family sites and their forks).
	Readable int `json:"readable"`
	// Reported is how many of them have said something, ever.
	Reported int `json:"reported"`
}

// Feed is what the console renders.
type Feed struct {
	Items       []store.SiteAnnouncement `json:"items"`
	Sites       SiteCoverage             `json:"sites"`
	LastFetched string                   `json:"last_fetched,omitempty"`
}

// RefreshResult is what one round did, for the manual button and the log.
type RefreshResult struct {
	Sites   int      `json:"sites"`
	Fetched int      `json:"fetched"`
	Failed  int      `json:"failed"`
	Added   int      `json:"added"`
	Errors  []string `json:"errors,omitempty"`
	At      string   `json:"at"`
}

// Feed returns the stored announcements, newest published first.
func (s *Service) Feed(limit int) (Feed, error) {
	if limit <= 0 || limit > 200 {
		limit = 200
	}
	items, err := s.db.SiteAnnouncements(limit)
	if err != nil {
		return Feed{}, err
	}
	reported, last, err := s.db.SiteNewsStatus()
	if err != nil {
		return Feed{}, err
	}
	readable, err := s.readableSites()
	if err != nil {
		return Feed{}, err
	}
	return Feed{
		Items:       items,
		Sites:       SiteCoverage{Readable: len(readable), Reported: reported},
		LastFetched: last,
	}, nil
}

// readableSites lists the configured sites whose notice board this gateway can
// read. The platform decides, exactly as it does for the probe sources: an
// operator should not have to type /api/status anywhere.
func (s *Service) readableSites() ([]domain.Site, error) {
	sites, err := s.db.Site.List()
	if err != nil {
		return nil, err
	}
	out := make([]domain.Site, 0, len(sites))
	for _, site := range sites {
		if _, _, ok := boardURL(site); ok {
			out = append(out, site)
		}
	}
	return out, nil
}

// boardURL resolves the site's notice board, or reports that it has none.
//
// New-API (and the forks the platform list names) answer GET /api/status with
// `data.announcements`. Anything else is left alone rather than guessed at: a
// wrong URL is a fetch that fails every round forever.
func boardURL(site domain.Site) (string, string, bool) {
	kind, base, ok := siteprobe.AutoSource(site.Platform, site.BaseURL)
	if !ok || kind != siteprobe.SourceNewAPI {
		return "", "", false
	}
	resolved, err := siteprobe.ResolveNewAPIBase(base)
	if err != nil {
		return "", "", false
	}
	return resolved + "/api/status", resolved, true
}

// statusPayload is the slice of New-API's /api/status this package reads. The
// document also carries the site's nav modules, quota units and checkout links;
// none of that is stored here.
type statusPayload struct {
	Success *bool  `json:"success"`
	Message string `json:"message"`
	Data    struct {
		Announcements []struct {
			ID   json.RawMessage `json:"id"`
			Text string          `json:"content"`
			// Extra is a link a site may attach to an announcement.
			Extra string `json:"extra"`
			// PublishDate is RFC3339 with milliseconds on the sites measured, but
			// forks differ, so parsing is lenient and an unreadable date simply
			// keeps whatever was stored before.
			PublishDate string `json:"publishDate"`
			Type        string `json:"type"`
		} `json:"announcements"`
	} `json:"data"`
}

// Refresh reads every readable site's board and stores what it finds.
//
// One site failing never fails the round: the feed is background information,
// and a volunteer site being down is exactly the situation in which the
// operator needs the other sites' notices to keep working.
func (s *Service) Refresh(ctx context.Context) (RefreshResult, error) {
	if !s.refreshing.TryLock() {
		return RefreshResult{}, ErrBusy
	}
	defer s.refreshing.Unlock()

	started := s.now()
	result := RefreshResult{At: started.Format(time.RFC3339)}
	sites, err := s.readableSites()
	if err != nil {
		return result, err
	}
	result.Sites = len(sites)
	if len(sites) == 0 {
		return result, nil
	}

	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	ctx, cancel := context.WithTimeout(ctx, roundTimeout)
	defer cancel()

	type outcome struct {
		items []store.SiteAnnouncement
		err   error
	}
	outcomes := make([]outcome, len(sites))
	sem := make(chan struct{}, concurrency)
	var wait sync.WaitGroup
	for i, site := range sites {
		wait.Add(1)
		go func(index int, site domain.Site) {
			defer wait.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			items, err := s.fetchSite(ctx, site)
			outcomes[index] = outcome{items: items, err: err}
		}(i, site)
	}
	wait.Wait()

	for i, site := range sites {
		got := outcomes[i]
		if got.err != nil {
			result.Failed++
			if len(result.Errors) < maxErrors {
				result.Errors = append(result.Errors, fmt.Sprintf("%s: %s", site.Name, got.err))
			}
			continue
		}
		result.Fetched++
		added, err := s.db.UpsertSiteAnnouncements(site.ID, got.items, started)
		if err != nil {
			result.Failed++
			if len(result.Errors) < maxErrors {
				result.Errors = append(result.Errors, fmt.Sprintf("%s: %s", site.Name, err))
			}
			continue
		}
		result.Added += added
	}
	return result, nil
}

// fetchSite reads one site's board.
func (s *Service) fetchSite(ctx context.Context, site domain.Site) ([]store.SiteAnnouncement, error) {
	endpoint, _, ok := boardURL(site)
	if !ok {
		return nil, fmt.Errorf("no readable notice board")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	response, err := s.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upstream status %d", response.StatusCode)
	}
	var payload statusPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("unreadable status document: %w", err)
	}
	if payload.Success != nil && !*payload.Success {
		message := strings.TrimSpace(payload.Message)
		if message == "" {
			message = "site reported failure"
		}
		return nil, errors.New(message)
	}
	found := payload.Data.Announcements
	if len(found) == 0 {
		return nil, nil
	}
	items := make([]store.SiteAnnouncement, 0, len(found))
	for _, raw := range found {
		if len(items) >= maxPerSite {
			break
		}
		items = append(items, store.SiteAnnouncement{
			SiteID:      site.ID,
			SiteName:    site.Name,
			UpstreamID:  announcementID(raw.ID),
			Content:     raw.Text,
			Extra:       raw.Extra,
			Kind:        raw.Type,
			PublishedAt: parsePublished(raw.PublishDate),
		})
	}
	return items, nil
}

// announcementID normalizes the site's id, which arrives as a number on the
// sites measured and could arrive as a string elsewhere.
func announcementID(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	if len(trimmed) >= 2 && (trimmed[0] == '"' && trimmed[len(trimmed)-1] == '"') {
		var text string
		if err := json.Unmarshal(raw, &text); err == nil {
			return strings.TrimSpace(text)
		}
		return ""
	}
	return trimmed
}

// publishedLayouts are the shapes seen in the wild: New-API writes RFC3339 with
// milliseconds, older forks write a database timestamp.
var publishedLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05",
}

// parsePublished normalizes a publish date to UTC RFC3339Nano. An unreadable
// value returns "" so the stored row keeps the date it already had — guessing
// "now" would shove an old announcement to the top of the feed on every fetch.
func parsePublished(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	for _, layout := range publishedLayouts {
		if parsed, err := time.Parse(layout, trimmed); err == nil {
			return parsed.UTC().Format(time.RFC3339Nano)
		}
	}
	return ""
}

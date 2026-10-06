package siteprobe

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

// SourceWatchbot is the third-party monitoring directory. Its readings are
// aggregates it measured itself, so they are labelled as third-party everywhere
// they are shown and only used when nothing first-hand exists.
const SourceWatchbot = "watchbot"

// externalSyncInterval bounds how often the whole directory snapshot is fetched.
// The payload carries every site it monitors (megabytes), so it must not follow
// the per-site collection cadence: a 60-second cadence would pull the same
// snapshot 1440 times a day for data that changes on the directory's own
// schedule.
const externalSyncInterval = 15 * time.Minute

// externalCatalog is the subset of a monitoring directory's snapshot the gateway
// reads. It carries health only: prices come from the sites themselves, which is
// first-hand and per-group, while the directory's price is a copy of theirs.
type externalCatalog struct {
	Rows []struct {
		SiteName         string  `json:"siteName"`
		SiteURL          string  `json:"siteUrl"`
		RawModelName     string  `json:"rawModelName"`
		GroupName        string  `json:"groupName"`
		ServiceState     string  `json:"serviceState"`
		AcquisitionState string  `json:"acquisitionState"`
		ObservedAt       string  `json:"observedAt"`
		SuccessRatio     float64 `json:"successRatio"`
		// Latency fields are floats in the payload even though they read as
		// milliseconds: measured, averageLatencyMs arrived as 25324.967391304348.
		// Decoding them as int made the whole snapshot fail to parse, so they are
		// decoded as what they are and rounded on the way into the table.
		AverageLatencyMs float64 `json:"averageLatencyMs"`
		FirstTokenMs     float64 `json:"firstTokenMs"`
		TokensPerSecond  float64 `json:"tokensPerSecond"`
	} `json:"rows"`
}

// FetchExternalCatalog reads the directory snapshot and maps every reading onto a
// site we actually route through, by host.
//
// Readings for sites we do not route through are dropped at this point: there is
// nothing they could inform, and storing them would turn the gateway into a
// partial copy of a directory it does not own.
func FetchExternalCatalog(ctx context.Context, client *http.Client, catalogURL string, sites []domain.Site) ([]store.SiteProbeExternal, error) {
	trimmed := strings.TrimSpace(catalogURL)
	if trimmed == "" {
		return nil, nil
	}
	body, err := getBody(ctx, client, trimmed)
	if err != nil {
		return nil, err
	}
	var catalog externalCatalog
	if err := json.Unmarshal(body, &catalog); err != nil {
		return nil, fmt.Errorf("external catalog: %w", err)
	}
	matcher := newHostMatcher(sites)
	out := make([]store.SiteProbeExternal, 0, len(catalog.Rows))
	for _, row := range catalog.Rows {
		site, found := matcher.match(row.SiteURL)
		if !found {
			continue
		}
		model := strings.TrimSpace(row.RawModelName)
		if model == "" {
			continue
		}
		out = append(out, store.SiteProbeExternal{
			Source: SourceWatchbot, SiteID: site.ID, SiteName: site.Name,
			SiteHost: hostNameOf(row.SiteURL), RawModel: model, GroupName: strings.TrimSpace(row.GroupName),
			Ratio: row.SuccessRatio, AvgLatencyMS: int(math.Round(row.AverageLatencyMs)),
			FirstTokenMS: int(math.Round(row.FirstTokenMs)), TokensPerSecond: row.TokensPerSecond,
			ServiceState: row.ServiceState, AcquisitionState: row.AcquisitionState,
			ObservedAt: row.ObservedAt,
		})
	}
	return out, nil
}

// hostMatcher ties a directory entry's host to a local site. A directory often
// reports a status-page or pricing subdomain (stat.example.com) for a site whose
// base URL is the bare domain, so a suffix match at a label boundary counts and
// the most specific host wins.
type hostMatcher struct {
	sites []domain.Site
}

func newHostMatcher(sites []domain.Site) hostMatcher {
	return hostMatcher{sites: sites}
}

func (m hostMatcher) match(rawURL string) (domain.Site, bool) {
	host := hostNameOf(rawURL)
	if host == "" {
		return domain.Site{}, false
	}
	bestIndex, bestScore, bestLen := -1, 0, 0
	for index, site := range m.sites {
		siteHost := hostNameOf(site.BaseURL)
		if siteHost == "" {
			continue
		}
		score := hostMatchScore(siteHost, host)
		if score == 0 {
			continue
		}
		if score > bestScore || (score == bestScore && len(siteHost) > bestLen) {
			bestIndex, bestScore, bestLen = index, score, len(siteHost)
		}
	}
	if bestIndex < 0 {
		return domain.Site{}, false
	}
	return m.sites[bestIndex], true
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

func hostNameOf(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed.Hostname())
}

// SyncExternal fetches the directory snapshot and replaces the stored one. A
// failure keeps the previous snapshot: a directory that is temporarily
// unreachable must not erase the readings we already have.
func (s *Service) SyncExternal(ctx context.Context, catalogURL string) (int, error) {
	sites, err := s.db.Site.List()
	if err != nil {
		return 0, err
	}
	if strings.TrimSpace(catalogURL) == "" {
		catalogURL = DefaultCatalogURL
	}
	readings, err := FetchExternalCatalog(ctx, s.client, catalogURL, sites)
	if err != nil {
		return 0, err
	}
	if err := s.db.ReplaceSiteProbeExternal(readings); err != nil {
		return 0, err
	}
	return len(readings), nil
}

// SyncExternalIfDue runs the snapshot sync at most once per interval. The
// scheduler calls it every round; the interval is what keeps a fast site cadence
// from re-downloading a multi-megabyte directory.
func (s *Service) SyncExternalIfDue(ctx context.Context, catalogURL string, now time.Time) (int, bool, error) {
	s.externalMu.Lock()
	if !s.externalSyncedAt.IsZero() && now.Sub(s.externalSyncedAt) < externalSyncInterval {
		s.externalMu.Unlock()
		return 0, false, nil
	}
	// Stamp before the fetch so two concurrent rounds cannot both pull it.
	s.externalSyncedAt = now
	s.externalMu.Unlock()

	count, err := s.SyncExternal(ctx, catalogURL)
	if err != nil {
		// Leave the timestamp: retry after the interval, without an empty lock.
		return 0, true, err
	}
	return count, true, nil
}

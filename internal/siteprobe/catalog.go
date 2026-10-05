package siteprobe

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// CatalogEntry is one site from a public monitoring directory, normalized to
// what the collector needs: a name, a URL to read, and the kind of source that
// URL is. Importing a catalog copies *addresses*, not data — the readings still
// come from each site itself, so the directory being down changes nothing.
type CatalogEntry struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Kind string `json:"kind"`
	// Source is where the entry was seen (a human-readable directory name).
	Source string `json:"source,omitempty"`
	// Models is how many (model × group) readings the directory had for this
	// site — a rough size signal for the preview.
	Models int `json:"models,omitempty"`
}

// DefaultCatalogURL is the directory's public dashboard. It is a bootstrap
// default, not a contract: every catalog request takes an explicit URL, the
// address importer never runs on a schedule, and the third-party health sync
// stores whatever the URL happens to serve.
const DefaultCatalogURL = "https://watchbot.cfd/api/v1/public/dashboard"

type catalogDashboard struct {
	Rows []struct {
		SiteID     int    `json:"siteId"`
		SiteName   string `json:"siteName"`
		SiteURL    string `json:"siteUrl"`
		Provider   string `json:"provider"`
		RuleName   string `json:"ruleName"`
		TotalCount int    `json:"totalCount"`
	} `json:"rows"`
}

// FetchCatalog reads a public monitoring dashboard and folds its rows into one
// entry per site: the directory stores one row per (site × model × group), and
// the same site appears under several models.
//
// The kind is derived from the URL shape, not from a directory-side label:
// a dedicated status host (a different domain than the site itself, /status/ in
// the path) is an Uptime Kuma page; anything else whose host matches the site's
// own is a pricing/model page on the site root, which the auto source already
// knows how to read — those entries become kind "" so the platform derivation
// decides.
func FetchCatalog(ctx context.Context, client *http.Client, catalogURL string) ([]CatalogEntry, error) {
	body, err := getBody(ctx, client, catalogURL)
	if err != nil {
		return nil, err
	}
	var dashboard catalogDashboard
	if err := json.Unmarshal(body, &dashboard); err != nil {
		return nil, fmt.Errorf("catalog returned an unexpected payload: %w", err)
	}
	type entry struct {
		name   string
		url    string
		models int
	}
	bySite := make(map[int]entry, len(dashboard.Rows))
	for _, row := range dashboard.Rows {
		siteURL := strings.TrimSpace(row.SiteURL)
		if siteURL == "" || row.SiteID == 0 {
			continue
		}
		parsed, err := url.Parse(siteURL)
		if err != nil || parsed.Host == "" {
			continue
		}
		current, seen := bySite[row.SiteID]
		if !seen {
			name := strings.TrimSpace(row.SiteName)
			if name == "" {
				name = parsed.Host
			}
			current = entry{name: name, url: siteURL}
		}
		current.models += row.TotalCount
		bySite[row.SiteID] = current
	}
	out := make([]CatalogEntry, 0, len(bySite))
	for _, item := range bySite {
		parsed, err := url.Parse(item.url)
		if err != nil {
			continue
		}
		kind := ""
		// A status page on a separate host (stat.example.com vs the site root)
		// or under /status/ is an Uptime Kuma deployment; the pricing shape is
		// covered by the platform-derived auto source.
		if strings.HasPrefix(parsed.Path, "/status") || looksLikeStatusHost(parsed.Host) {
			kind = SourceUptimeKuma
		}
		out = append(out, CatalogEntry{
			Name: item.name, URL: item.url, Kind: kind, Models: item.models,
			Source: catalogHost(catalogURL),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// looksLikeStatusHost recognizes the dedicated monitoring hosts directories
// point at (stat., status., uptime. prefixes), which never serve the relay API
// itself.
func looksLikeStatusHost(host string) bool {
	prefixes := []string{"stat.", "status.", "uptime.", "monitor."}
	lower := strings.ToLower(host)
	for _, prefix := range prefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

func catalogHost(catalogURL string) string {
	parsed, err := url.Parse(catalogURL)
	if err != nil {
		return ""
	}
	return parsed.Host
}

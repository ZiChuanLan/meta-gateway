// Package siteprobe reads a site's OWN public probe data instead of spending
// upstream tokens on real completions.
//
// Two public sources are supported:
//
//   - "uptime_kuma": the site's public Uptime Kuma status page. Its
//     /api/status-page/<slug> lists the groups and monitors, and
//     /api/status-page/heartbeat/<slug> carries the raw heartbeats, from which
//     the availability of every monitored model is computed per round.
//   - "newapi": the site's public New-API price table (/api/pricing), which
//     prices every model it sells with its own group multipliers.
//
// Everything here is a plain GET against a public endpoint: no credentials, no
// browser challenge solving, no tokens. A failed round stores no samples, so a
// site that changes its page or blocks us simply produces no verdict — never a
// wrong one.
//
// Results land in site_probe_runs / site_probe_samples. model_health and
// probe_results are deliberately untouched: those tables mean "a real
// completion was sent", which is exactly what this package avoids.
package siteprobe

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lan/meta-gateway/internal/store"
)

// SourceKind* are the supported probe-source kinds.
const (
	SourceUptimeKuma = "uptime_kuma"
	SourceNewAPI     = "newapi"
	// SourceSub2APITransit reads a Sub2API site's public transit snapshot (the
	// /.well-known/ai-transit.json discovery protocol). Availability there is
	// the site's own probe (v1) or its real-traffic aggregates (v2).
	SourceSub2APITransit = "sub2api_transit"
)

// kumaDefaultWindow is how much heartbeat history a round aggregates. Uptime
// Kuma keeps ~100 beats per monitor (about 14 minutes apart by default), so a
// day is the natural window and matches what status pages themselves show.
const kumaDefaultWindow = 24 * time.Hour

// monitorType* classify where a reading came from. "price" marks a model the
// site publishes a price for but has no availability reading for.
const (
	MonitorTypeKeyword = "keyword"
	MonitorTypeHTTP    = "http"
	MonitorTypeActive  = "active"  // Sub2API V1: the site probed its own channels
	MonitorTypePassive = "passive" // Sub2API V2: the site aggregated real traffic
	MonitorTypePrice   = "price"
)

// monitor status codes as Uptime Kuma defines them.
const (
	kumaStatusDown        = 0
	kumaStatusUp          = 1
	kumaStatusPending     = 2
	kumaStatusMaintenance = 3
)

// ResolveKumaSource splits a public status-page URL into the API base and the
// page slug. Uptime Kuma serves the page at /status/<slug> (its default),
// /status-page/<slug> on some deployments, or /<slug> when the path is
// customized.
func ResolveKumaSource(rawURL string) (base string, slug string, err error) {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return "", "", fmt.Errorf("probe source url is empty")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" {
		return "", "", fmt.Errorf("invalid probe source url")
	}
	base = parsed.Scheme + "://" + parsed.Host
	segments := make([]string, 0, 3)
	for _, segment := range strings.Split(strings.Trim(parsed.Path, "/"), "/") {
		if segment != "" {
			segments = append(segments, segment)
		}
	}
	if len(segments) == 0 {
		return "", "", fmt.Errorf("probe source url has no status page path")
	}
	// Drop a known status-page prefix, then the last segment is the slug.
	if len(segments) > 1 {
		switch strings.ToLower(segments[0]) {
		case "status", "status-page":
			segments = segments[1:]
		}
	}
	slug = segments[len(segments)-1]
	if slug == "" {
		return "", "", fmt.Errorf("probe source url has no slug")
	}
	return base, slug, nil
}

type kumaStatusPage struct {
	Config struct {
		Slug  string `json:"slug"`
		Title string `json:"title"`
	} `json:"config"`
	PublicGroupList []struct {
		ID          int    `json:"id"`
		Name        string `json:"name"`
		Weight      int    `json:"weight"`
		MonitorList []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"monitorList"`
	} `json:"publicGroupList"`
}

// kumaBeat is one heartbeat entry from the public API. Ping is a pointer
// because Uptime Kuma stores null on every down beat (measured), and a zero
// would read as a real 0 ms response.
type kumaBeat struct {
	Status int    `json:"status"`
	Time   string `json:"time"`
	Msg    string `json:"msg"`
	Ping   *int   `json:"ping"`
}

type kumaHeartbeat struct {
	HeartbeatList map[string][]kumaBeat `json:"heartbeatList"`
}

// KumaSource is one resolved status page: its groups, monitors and the raw
// heartbeats behind them.
type KumaSource struct {
	Title    string
	Groups   []KumaGroup
	Monitors []KumaMonitor
}

// KumaGroup is a public group on the status page (site -> our model group).
type KumaGroup struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// KumaMonitor is one monitored model, with the availability computed from its
// heartbeats inside the aggregation window.
type KumaMonitor struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Type         string    `json:"type"`
	GroupName    string    `json:"group_name"`
	Samples      int       `json:"samples"`
	UpCount      int       `json:"up_count"`
	Ratio        float64   `json:"ratio"`
	AvgPingMS    *int      `json:"avg_ping_ms,omitempty"`
	WeakEvidence bool      `json:"weak_evidence"`
	LastBeatAt   time.Time `json:"last_beat_at"`
}

// Ratio is the availability over the aggregation window; a monitor with no
// countable heartbeat (pending/maintenance only) reports 0 samples and must be
// treated as "no data" rather than as down.
func (m KumaMonitor) RatioString() string {
	return fmt.Sprintf("%d/%d", m.UpCount, m.Samples)
}

// FetchKuma reads a public status page and computes availability per monitor.
func FetchKuma(ctx context.Context, client *http.Client, statusURL string, window time.Duration) (*KumaSource, error) {
	base, slug, err := ResolveKumaSource(statusURL)
	if err != nil {
		return nil, err
	}
	if window <= 0 {
		window = kumaDefaultWindow
	}
	var page kumaStatusPage
	if err := getJSON(ctx, client, fmt.Sprintf("%s/api/status-page/%s", base, url.PathEscape(slug)), &page); err != nil {
		return nil, err
	}
	var heartbeats kumaHeartbeat
	if err := getJSON(ctx, client, fmt.Sprintf("%s/api/status-page/heartbeat/%s", base, url.PathEscape(slug)), &heartbeats); err != nil {
		return nil, err
	}

	source := &KumaSource{Title: page.Config.Title}
	// A monitor can be listed under several groups; the first one wins, which
	// matches how the status page itself orders them.
	groupOf := make(map[string]string)
	for _, group := range page.PublicGroupList {
		source.Groups = append(source.Groups, KumaGroup{ID: group.ID, Name: group.Name})
		for _, monitor := range group.MonitorList {
			id := fmt.Sprintf("%d", monitor.ID)
			if _, seen := groupOf[id]; !seen {
				groupOf[id] = group.Name
			}
		}
	}
	cutoff := time.Now().Add(-window)
	seen := make(map[string]bool)
	for _, group := range page.PublicGroupList {
		for _, monitor := range group.MonitorList {
			id := fmt.Sprintf("%d", monitor.ID)
			if seen[id] {
				continue
			}
			seen[id] = true
			computed := computeMonitor(id, monitor.Name, monitor.Type, groupOf[id], heartbeats.HeartbeatList[id], cutoff)
			source.Monitors = append(source.Monitors, computed)
		}
	}
	return source, nil
}

// computeMonitor turns one monitor's heartbeats into an availability reading.
//
// Only up and down beats count: a maintenance window or a pending check must
// not be able to drag a healthy model's availability down, and a monitor that
// never reported anything is "no data", not "0%".
func computeMonitor(id, name, monitorType, groupName string, beats []kumaBeat, cutoff time.Time) KumaMonitor {
	monitor := KumaMonitor{
		ID:           id,
		Name:         strings.TrimSpace(name),
		Type:         monitorType,
		GroupName:    groupName,
		WeakEvidence: strings.EqualFold(monitorType, MonitorTypeHTTP),
	}
	pingSum, pingCount := 0, 0
	for _, beat := range beats {
		at := parseKumaTime(beat.Time)
		if !at.IsZero() {
			if at.After(monitor.LastBeatAt) {
				monitor.LastBeatAt = at
			}
			if at.Before(cutoff) {
				continue
			}
		}
		switch beat.Status {
		case kumaStatusUp:
			monitor.Samples++
			monitor.UpCount++
			if beat.Ping != nil {
				pingSum += *beat.Ping
				pingCount++
			}
		case kumaStatusDown:
			monitor.Samples++
		default:
			// pending / maintenance: excluded from the denominator
		}
	}
	if monitor.Samples > 0 {
		monitor.Ratio = float64(monitor.UpCount) / float64(monitor.Samples)
	}
	if pingCount > 0 {
		avg := pingSum / pingCount
		monitor.AvgPingMS = &avg
	}
	return monitor
}

// parseKumaTime parses Uptime Kuma's timestamp ("2006-01-02 15:04:05.000"),
// which carries no zone and is UTC.
func parseKumaTime(value string) time.Time {
	for _, layout := range []string{"2006-01-02 15:04:05.000", "2006-01-02 15:04:05", time.RFC3339} {
		if parsed, err := time.Parse(layout, strings.TrimSpace(value)); err == nil {
			return parsed.UTC()
		}
	}
	return time.Time{}
}

// Samples converts a Kuma source into storable samples for one run.
func (s *KumaSource) Samples(siteID, runID int64, observedAt time.Time) []store.SiteProbeSample {
	out := make([]store.SiteProbeSample, 0, len(s.Monitors))
	for _, monitor := range s.Monitors {
		if monitor.Name == "" {
			continue
		}
		out = append(out, store.SiteProbeSample{
			RunID:        runID,
			SiteID:       siteID,
			MonitorID:    monitor.ID,
			MonitorName:  monitor.Name,
			MonitorType:  monitor.Type,
			GroupName:    monitor.GroupName,
			RawModel:     monitor.Name,
			ObservedAt:   observedAt,
			Samples:      monitor.Samples,
			UpCount:      monitor.UpCount,
			Ratio:        monitor.Ratio,
			AvgPingMS:    monitor.AvgPingMS,
			WeakEvidence: monitor.WeakEvidence,
		})
	}
	return out
}

// getJSON performs a bounded public GET and decodes JSON into out.
func getJSON(ctx context.Context, client *http.Client, endpoint string, out any) error {
	body, err := getBody(ctx, client, endpoint)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("probe source %s returned an unexpected payload: %w", endpoint, err)
	}
	return nil
}

// getBody performs a bounded public GET and returns the raw body.
func getBody(ctx context.Context, client *http.Client, endpoint string) ([]byte, error) {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "meta-gateway-site-probe/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	const maxProbeBytes = 8 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxProbeBytes))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("probe source %s answered %d", endpoint, resp.StatusCode)
	}
	return body, nil
}

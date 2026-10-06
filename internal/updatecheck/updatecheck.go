// Package updatecheck compares the running build against the latest GitHub
// release so the console can surface "a newer version is available". All
// outbound calls are gated by the admin toggle surfaced through the enabled
// predicate; with the toggle off the service never touches the network.
package updatecheck

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/lan/meta-gateway/internal/buildinfo"
)

const (
	// Repo is the GitHub slug queried for the latest release.
	Repo = "ZiChuanLan/meta-gateway"
	// defaultTimeout bounds a single GitHub API call.
	defaultTimeout = 5 * time.Second
	// DefaultInterval is both the background cadence and the cache TTL.
	DefaultInterval = time.Hour
)

// Status is the cached outcome of the most recent comparison.
type Status struct {
	Current   string `json:"current_version"`
	Channel   string `json:"channel"`
	Latest    string `json:"latest_version"`
	HasUpdate bool   `json:"has_update"`
	URL       string `json:"release_url"`
	// Notes is the release body as published on GitHub (markdown). The
	// console's update dialog shows it so an operator can decide what an update
	// changes BEFORE clicking apply, instead of trusting a version number.
	Notes     string    `json:"notes,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
	// Err carries the last refresh failure; the prior comparison is kept.
	Err string `json:"error,omitempty"`
}

type releaseResponse struct {
	TagName    string `json:"tag_name"`
	HTMLURL    string `json:"html_url"`
	Body       string `json:"body"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
}

// Service caches the latest release comparison and refreshes it on a
// schedule. The zero value is not usable; use New.
type Service struct {
	baseURL  string
	client   *http.Client
	interval time.Duration
	enabled  func() bool
	status   atomic.Pointer[Status]
	channel  func() string
}

// New builds a service. enabled is consulted before every network call.
func New(enabled func() bool) *Service {
	return &Service{
		baseURL:  "https://api.github.com",
		client:   &http.Client{Timeout: defaultTimeout},
		interval: DefaultInterval,
		enabled:  enabled,
	}
}

// SetChannelSource is configured before the service starts.
func (s *Service) SetChannelSource(source func() string) { s.channel = source }
func (s *Service) Channel() string {
	if s.channel != nil && s.channel() == "beta" {
		return "beta"
	}
	return "stable"
}

// Interval exposes the background cadence (also the freshness bound used by
// RefreshIfStale).
func (s *Service) Interval() time.Duration { return s.interval }

// Status returns the cached comparison without touching the network.
func (s *Service) Status() Status {
	if cached := s.status.Load(); cached != nil && cached.Channel == s.Channel() {
		return *cached
	}
	return Status{Current: buildinfo.Version, Channel: s.Channel()}
}

// Refresh queries GitHub now and caches the result.
func (s *Service) Refresh(ctx context.Context) Status {
	next := s.fetch(ctx)
	s.status.Store(&next)
	return next
}

// RefreshIfStale reuses the cached result while it is fresh enough and
// triggers a synchronous refresh otherwise (e.g. on first admin visit).
func (s *Service) RefreshIfStale(ctx context.Context, maxAge time.Duration) Status {
	if cached := s.status.Load(); cached != nil && cached.Channel == s.Channel() && cached.Latest != "" && time.Since(cached.CheckedAt) < maxAge {
		return *cached
	}
	return s.Refresh(ctx)
}

// Run drives the periodic refresh until ctx is cancelled. Ticks skip the
// network entirely while the admin toggle is off.
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if s.enabled != nil && !s.enabled() {
				continue
			}
			s.Refresh(ctx)
		}
	}
}

func (s *Service) fetch(ctx context.Context) Status {
	channel := s.Channel()
	fallback := s.Status()
	fallback.Current = buildinfo.Version
	fallback.Channel = channel
	fallback.Err = ""
	if s.enabled != nil && !s.enabled() {
		return fallback
	}
	endpoint := "/releases/latest"
	if channel == "beta" {
		endpoint = "/releases?per_page=100"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		s.baseURL+"/repos/"+Repo+endpoint, nil)
	if err != nil {
		return failed(fallback, err.Error())
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := s.client.Do(req)
	if err != nil {
		return failed(fallback, err.Error())
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return failed(fallback, fmt.Sprintf("github api status %d", resp.StatusCode))
	}
	var release releaseResponse
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 4<<20))
	if channel == "beta" {
		var releases []releaseResponse
		if err := decoder.Decode(&releases); err != nil {
			return failed(fallback, err.Error())
		}
		for _, candidate := range releases {
			if candidate.Draft || !IsReleaseTag(candidate.TagName) {
				continue
			}
			if candidate.Prerelease && !strings.Contains(candidate.TagName, "-beta.") {
				continue
			}
			if release.TagName == "" || IsNewer(candidate.TagName, release.TagName) {
				release = candidate
			}
		}
	} else if err := decoder.Decode(&release); err != nil {
		return failed(fallback, err.Error())
	}
	if release.Draft || (channel == "stable" && release.Prerelease) {
		return failed(fallback, "release not eligible")
	}
	tag := strings.TrimSpace(release.TagName)
	if tag == "" {
		return failed(fallback, "github api returned no tag")
	}
	return Status{
		Current:   buildinfo.Version,
		Channel:   channel,
		Latest:    tag,
		HasUpdate: IsNewer(tag, buildinfo.Version),
		URL:       release.HTMLURL,
		Notes:     release.Body,
		CheckedAt: time.Now().UTC(),
	}
}

func failed(base Status, message string) Status {
	base.Err = message
	base.CheckedAt = time.Now().UTC()
	return base
}

// IsNewer reports whether tag denotes a release newer than current. Both are
// dotted numeric versions with an optional "v" prefix; unparseable input
// (custom tags, dev builds) never counts as newer.
var releaseTagPattern = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(?:-beta\.[0-9]+)?$`)

func IsReleaseTag(tag string) bool { return releaseTagPattern.MatchString(tag) }

// Compare numeric core first, then SemVer prerelease identifiers. A final
// release follows every prerelease of the same core, beta.10 follows beta.2.
func IsNewer(tag, current string) bool {
	a, ap, ok := parseComparable(tag)
	if !ok {
		return false
	}
	b, bp, ok := parseComparable(current)
	if !ok {
		return false
	}
	for i := 0; i < len(a) || i < len(b); i++ {
		x, y := 0, 0
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			return x > y
		}
	}
	if ap == bp {
		return false
	}
	if ap == "" {
		return true
	}
	if bp == "" {
		return false
	}
	aa, bb := strings.Split(ap, "."), strings.Split(bp, ".")
	for i := 0; i < len(aa) && i < len(bb); i++ {
		if aa[i] == bb[i] {
			continue
		}
		x, xe := strconv.Atoi(aa[i])
		y, ye := strconv.Atoi(bb[i])
		if xe == nil && ye == nil {
			return x > y
		}
		if xe == nil {
			return false
		}
		if ye == nil {
			return true
		}
		return aa[i] > bb[i]
	}
	return len(aa) > len(bb)
}
func parseComparable(raw string) ([]int, string, bool) {
	raw = strings.TrimPrefix(strings.TrimSpace(raw), "v")
	raw = strings.SplitN(raw, "+", 2)[0]
	parts := strings.SplitN(raw, "-", 2)
	core := strings.Split(parts[0], ".")
	if len(core) < 2 || len(core) > 3 {
		return nil, "", false
	}
	nums := make([]int, len(core))
	for i, v := range core {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || v == "" {
			return nil, "", false
		}
		nums[i] = n
	}
	pre := ""
	if len(parts) == 2 {
		pre = parts[1]
		if pre == "" {
			return nil, "", false
		}
		for _, v := range strings.Split(pre, ".") {
			if v == "" {
				return nil, "", false
			}
			for _, r := range v {
				if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '-') {
					return nil, "", false
				}
			}
		}
	}
	return nums, pre, true
}

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
	Current   string    `json:"current_version"`
	Latest    string    `json:"latest_version"`
	HasUpdate bool      `json:"has_update"`
	URL       string    `json:"release_url"`
	CheckedAt time.Time `json:"checked_at"`
	// Err carries the last refresh failure; the prior comparison is kept.
	Err string `json:"error,omitempty"`
}

type releaseResponse struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
}

// Service caches the latest release comparison and refreshes it on a
// schedule. The zero value is not usable; use New.
type Service struct {
	baseURL  string
	client   *http.Client
	interval time.Duration
	enabled  func() bool
	status   atomic.Pointer[Status]
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

// Interval exposes the background cadence (also the freshness bound used by
// RefreshIfStale).
func (s *Service) Interval() time.Duration { return s.interval }

// Status returns the cached comparison without touching the network.
func (s *Service) Status() Status {
	if cached := s.status.Load(); cached != nil {
		return *cached
	}
	return Status{Current: buildinfo.Version}
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
	if cached := s.status.Load(); cached != nil && cached.Latest != "" && time.Since(cached.CheckedAt) < maxAge {
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
	fallback := Status{Current: buildinfo.Version}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		s.baseURL+"/repos/"+Repo+"/releases/latest", nil)
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
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&release); err != nil {
		return failed(fallback, err.Error())
	}
	tag := strings.TrimSpace(release.TagName)
	if tag == "" {
		return failed(fallback, "github api returned no tag")
	}
	return Status{
		Current:   buildinfo.Version,
		Latest:    tag,
		HasUpdate: IsNewer(tag, buildinfo.Version),
		URL:       release.HTMLURL,
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
func IsNewer(tag, current string) bool {
	tagParts, ok := parseVersion(tag)
	if !ok {
		return false
	}
	curParts, ok := parseVersion(current)
	if !ok {
		return false
	}
	for i := 0; i < len(tagParts) || i < len(curParts); i++ {
		var t, c int
		if i < len(tagParts) {
			t = tagParts[i]
		}
		if i < len(curParts) {
			c = curParts[i]
		}
		if t != c {
			return t > c
		}
	}
	return false
}

func parseVersion(raw string) ([]int, bool) {
	raw = strings.TrimPrefix(strings.TrimSpace(raw), "v")
	if raw == "" {
		return nil, false
	}
	parts := strings.Split(raw, ".")
	nums := make([]int, 0, len(parts))
	for _, part := range parts {
		if n, err := strconv.Atoi(part); err == nil {
			nums = append(nums, n)
			continue
		}
		// Tolerate a "1.2.3-rc1" style suffix on the last segment.
		if idx := strings.IndexByte(part, '-'); idx > 0 {
			if n, err := strconv.Atoi(part[:idx]); err == nil {
				nums = append(nums, n)
				return nums, true
			}
		}
		return nil, false
	}
	return nums, true
}

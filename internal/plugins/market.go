// Plugin market: remote registry.json sources that list installable sidecar
// plugins. Modeled after CLIProxyAPI's pluginstore protocol — a registry is a
// JSON document with a schema_version and a list of plugins; meta-gateway
// fetches, validates, caches and lists them. Installing a market entry is a
// plain sidecar registration (its "url" field plus optional manual manifest
// fields), so no artifact download is involved.
package plugins

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// DefaultMarketURL is the built-in official market registry. Point it at your
// own registry by setting PLUGIN_MARKET_URLS (comma-separated, appended after
// this one). See docs/PLUGINS.md for the registry.json schema.
const DefaultMarketURL = "https://raw.githubusercontent.com/lan/meta-gateway-plugins/main/registry.json"

const (
	marketSchemaVersion = 1
	marketMaxBytes      = 1 << 20 // 1 MiB
	marketCacheTTL      = 15 * time.Minute
)

var marketPluginIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// MarketSource identifies one registry URL.
type MarketSource struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

// MarketPlugin is one installable sidecar plugin listed in a registry.
type MarketPlugin struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Author      string   `json:"author,omitempty"`
	Version     string   `json:"version,omitempty"`
	Logo        string   `json:"logo,omitempty"`
	Homepage    string   `json:"homepage,omitempty"`
	License     string   `json:"license,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	// URL is the sidecar service address used for registration.
	URL string `json:"url"`
	// Manual manifest fields, for sidecar services without /plugin.json.
	PagePath    string `json:"page_path,omitempty"`
	HealthPath  string `json:"health_path,omitempty"`
	APIPrefix   string `json:"api_prefix,omitempty"`
	ChannelPath string `json:"channel_path,omitempty"`
}

// MarketEntry pairs a market plugin with the source it came from.
type MarketEntry struct {
	MarketPlugin
	Source MarketSource `json:"source"`
}

type marketCacheEntry struct {
	plugins []MarketEntry
	fetched time.Time
}

// market manages remote registries: fetch, validate, cache, merge.
type market struct {
	mu      sync.Mutex
	client  *http.Client
	sources []MarketSource
	cache   map[string]marketCacheEntry // by source URL
}

func newMarket(client *http.Client, extraURLs []string) *market {
	m := &market{
		client:  client,
		cache:   map[string]marketCacheEntry{},
		sources: []MarketSource{marketSourceOf(DefaultMarketURL)},
	}
	for _, raw := range extraURLs {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if src, err := parseMarketSource(raw); err == nil {
			m.sources = append(m.sources, src)
		}
	}
	return m
}

func marketSourceOf(registryURL string) MarketSource {
	return MarketSource{
		ID:   "source-" + shortHash(registryURL),
		Name: marketSourceName(registryURL),
		URL:  registryURL,
	}
}

func parseMarketSource(registryURL string) (MarketSource, error) {
	parsed, err := url.Parse(strings.TrimSpace(registryURL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return MarketSource{}, fmt.Errorf("invalid market url %q", registryURL)
	}
	if hasSensitiveQuery(parsed) {
		return MarketSource{}, fmt.Errorf("market url %q contains sensitive query parameter", registryURL)
	}
	return marketSourceOf(strings.TrimSpace(registryURL)), nil
}

func marketSourceName(registryURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(registryURL))
	if err != nil || parsed.Host == "" {
		return strings.TrimSpace(registryURL)
	}
	return parsed.Host
}

// Sources returns the configured market sources (deduplicated by URL).
func (m *market) Sources() []MarketSource {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]MarketSource, 0, len(m.sources))
	seen := map[string]bool{}
	for _, s := range m.sources {
		if seen[s.URL] {
			continue
		}
		seen[s.URL] = true
		out = append(out, s)
	}
	return out
}

// List returns all market plugins from all sources, deduplicated by plugin ID
// (first source wins), refreshing stale caches as needed.
func (m *market) List(ctx context.Context) ([]MarketEntry, error) {
	m.mu.Lock()
	sources := append([]MarketSource(nil), m.sources...)
	m.mu.Unlock()

	var out []MarketEntry
	seen := map[string]bool{}
	for _, src := range sources {
		entries, err := m.fetch(ctx, src)
		if err != nil {
			// One bad source must not kill the whole market; keep stale
			// cache if present, otherwise skip.
			continue
		}
		for _, e := range entries {
			if seen[e.ID] {
				continue
			}
			seen[e.ID] = true
			out = append(out, e)
		}
	}
	return out, nil
}

func (m *market) fetch(ctx context.Context, src MarketSource) ([]MarketEntry, error) {
	m.mu.Lock()
	if cached, ok := m.cache[src.URL]; ok && time.Since(cached.fetched) < marketCacheTTL {
		m.mu.Unlock()
		return cached.plugins, nil
	}
	m.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("market fetch %s: %w", src.URL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("market fetch %s: status %d", src.URL, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, marketMaxBytes))
	if err != nil {
		return nil, fmt.Errorf("market read %s: %w", src.URL, err)
	}
	var registry struct {
		SchemaVersion int            `json:"schema_version"`
		Plugins       []MarketPlugin `json:"plugins"`
	}
	if err := json.Unmarshal(body, &registry); err != nil {
		return nil, fmt.Errorf("market parse %s: %w", src.URL, err)
	}
	if registry.SchemaVersion != marketSchemaVersion {
		return nil, fmt.Errorf("market %s: unsupported schema_version %d", src.URL, registry.SchemaVersion)
	}
	entries := make([]MarketEntry, 0, len(registry.Plugins))
	for i := range registry.Plugins {
		p := &registry.Plugins[i]
		if err := validateMarketPlugin(p); err != nil {
			return nil, fmt.Errorf("market %s: plugins[%d] %s: %w", src.URL, i, p.ID, err)
		}
		entries = append(entries, MarketEntry{MarketPlugin: *p, Source: src})
	}
	m.mu.Lock()
	m.cache[src.URL] = marketCacheEntry{plugins: entries, fetched: time.Now()}
	m.mu.Unlock()
	return entries, nil
}

func validateMarketPlugin(p *MarketPlugin) error {
	p.ID = strings.TrimSpace(p.ID)
	p.Name = strings.TrimSpace(p.Name)
	p.URL = strings.TrimSpace(p.URL)
	p.Version = strings.TrimSpace(p.Version)
	if !marketPluginIDPattern.MatchString(p.ID) {
		return fmt.Errorf("invalid id %q", p.ID)
	}
	if p.Name == "" {
		return fmt.Errorf("missing name")
	}
	parsed, err := url.Parse(p.URL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return fmt.Errorf("invalid url %q", p.URL)
	}
	if hasSensitiveQuery(parsed) {
		return fmt.Errorf("url contains sensitive query parameter")
	}
	if p.Logo != "" {
		logoURL, err := url.Parse(strings.TrimSpace(p.Logo))
		if err != nil || (logoURL.Scheme != "http" && logoURL.Scheme != "https") || logoURL.Host == "" {
			return fmt.Errorf("invalid logo url")
		}
	}
	if p.Homepage != "" {
		homeURL, err := url.Parse(strings.TrimSpace(p.Homepage))
		if err != nil || (homeURL.Scheme != "http" && homeURL.Scheme != "https") || homeURL.Host == "" {
			return fmt.Errorf("invalid homepage url")
		}
	}
	return nil
}

// MarketInstall describes what a market entry maps to for registration.
func (e *MarketEntry) InstallSpec() *SidecarManifest {
	return &SidecarManifest{
		ID:          e.ID,
		Version:     firstNonEmpty(e.Version, "1.0.0"),
		Name:        e.Name,
		Description: e.Description,
		PagePath:    e.PagePath,
		HealthPath:  e.HealthPath,
		APIPrefix:   e.APIPrefix,
		ChannelPath: e.ChannelPath,
	}
}

// shortHash returns a stable short hex digest of s (used for source ids).
func shortHash(s string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(s)))
	return hex.EncodeToString(sum[:])[:12]
}

// hasSensitiveQuery reports whether the URL query contains credential-like
// parameters (tokens, keys, secrets) that should never be in a registry URL.
func hasSensitiveQuery(parsed *url.URL) bool {
	if parsed == nil || parsed.RawQuery == "" {
		return false
	}
	for key := range parsed.Query() {
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "token", "access_token", "access_key", "secret", "secret_key", "api_key", "key":
			return true
		}
	}
	return false
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

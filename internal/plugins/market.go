// Plugin market: remote registry.json sources that list installable sidecar
// plugins. Registry v1 keeps the original "already running HTTP service"
// entries; registry v2 additionally supports downloadable sidecar packages
// with platform-specific artifacts and SHA-256 verification.
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
	marketSchemaVersion = 2
	marketMaxBytes      = 1 << 20 // 1 MiB
	marketCacheTTL      = 15 * time.Minute
)

var marketPluginIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

const (
	marketInstallSidecar       = "sidecar"
	marketInstallDirect        = "direct"
	marketInstallGitHubRelease = "github-release"
)

// MarketSource identifies one registry URL.
type MarketSource struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

// MarketArtifact is one downloadable package for a target platform. The
// package is a zip archive containing plugin.json and the sidecar executable.
type MarketArtifact struct {
	GOOS   string `json:"goos,omitempty"`
	GOARCH string `json:"goarch,omitempty"`
	URL    string `json:"url,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
	Size   int64  `json:"size,omitempty"`
}

// MarketInstallPlan mirrors the useful subset of CLIProxyAPI's registry
// format. direct uses Artifacts; github-release resolves a GitHub release and
// uses the conventional id_version_goos_goarch.zip asset plus checksums.txt.
type MarketInstallPlan struct {
	Type            string           `json:"type,omitempty"`
	Artifacts       []MarketArtifact `json:"artifacts,omitempty"`
	ArtifactPattern string           `json:"artifact_pattern,omitempty"`
	ChecksumAsset   string           `json:"checksum_asset,omitempty"`
}

// MarketVersion pins a historical package version for direct registries.
type MarketVersion struct {
	Version string            `json:"version"`
	Install MarketInstallPlan `json:"install,omitempty"`
}

// MarketPlugin is one installable plugin listed in a registry. A sidecar entry
// points at an already-running service with URL. A packaged entry omits URL
// and supplies Install plus either Repository (github-release) or direct
// platform artifacts.
type MarketPlugin struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Author      string          `json:"author,omitempty"`
	Version     string          `json:"version,omitempty"`
	Versions    []MarketVersion `json:"versions,omitempty"`
	Repository  string          `json:"repository,omitempty"`
	Logo        string          `json:"logo,omitempty"`
	Homepage    string          `json:"homepage,omitempty"`
	License     string          `json:"license,omitempty"`
	Tags        []string        `json:"tags,omitempty"`
	// URL is the sidecar service address used for manual registration.
	URL string `json:"url,omitempty"`
	// Install describes a downloadable sidecar package.
	Install MarketInstallPlan `json:"install,omitempty"`
	// ConfigFields is retained for legacy registry v1 sidecar entries without
	// a live /plugin.json manifest.
	ConfigFields []ConfigField `json:"config_fields,omitempty"`
	// AuthRequired is metadata only. Download credentials are configured on the
	// gateway host, never embedded in registry URLs.
	AuthRequired bool `json:"auth_required,omitempty"`
	// Manual manifest fields for a sidecar service without /plugin.json.
	PagePath    string `json:"page_path,omitempty"`
	HealthPath  string `json:"health_path,omitempty"`
	APIPrefix   string `json:"api_prefix,omitempty"`
	ChannelPath string `json:"channel_path,omitempty"`
}

// InstallType returns the registry installation mode. Legacy entries with a
// URL and no install block are sidecars; packaged entries with a repository
// default to GitHub releases for CLIProxyAPI-style registries.
func (p MarketPlugin) InstallType() string {
	installType := strings.ToLower(strings.TrimSpace(p.Install.Type))
	if installType != "" {
		return installType
	}
	if strings.TrimSpace(p.URL) != "" {
		return marketInstallSidecar
	}
	return marketInstallGitHubRelease
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
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
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
	entries, err := m.listAll(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]MarketEntry, 0, len(entries))
	seen := map[string]bool{}
	for _, entry := range entries {
		if seen[entry.ID] {
			continue
		}
		seen[entry.ID] = true
		out = append(out, entry)
	}
	return out, nil
}

// listAll keeps duplicate IDs from different registries so an explicit source
// can be selected for installation. The public market listing still uses List
// and preserves the first-source-wins behavior of registry v1.
func (m *market) listAll(ctx context.Context) ([]MarketEntry, error) {
	m.mu.Lock()
	sources := append([]MarketSource(nil), m.sources...)
	m.mu.Unlock()

	var out []MarketEntry
	for _, src := range sources {
		entries, err := m.fetch(ctx, src)
		if err != nil {
			// One bad source must not kill the whole market; keep stale
			// cache if present, otherwise skip.
			continue
		}
		out = append(out, entries...)
	}
	return out, nil
}

func (m *market) fetch(ctx context.Context, src MarketSource) ([]MarketEntry, error) {
	m.mu.Lock()
	cached, hasCached := m.cache[src.URL]
	age := time.Since(cached.fetched)
	if hasCached && age >= 0 && age < marketCacheTTL {
		m.mu.Unlock()
		return cloneMarketEntries(cached.plugins), nil
	}
	stale := cloneMarketEntries(cached.plugins)
	m.mu.Unlock()
	fallback := func(err error) ([]MarketEntry, error) {
		if hasCached {
			return stale, nil
		}
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.URL, nil)
	if err != nil {
		return fallback(err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := m.client.Do(req)
	if err != nil {
		return fallback(fmt.Errorf("market fetch %s: %w", src.URL, err))
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fallback(fmt.Errorf("market fetch %s: status %d", src.URL, resp.StatusCode))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, marketMaxBytes+1))
	if err != nil {
		return fallback(fmt.Errorf("market read %s: %w", src.URL, err))
	}
	if len(body) > marketMaxBytes {
		return fallback(fmt.Errorf("market read %s: response too large", src.URL))
	}
	var registry struct {
		SchemaVersion int            `json:"schema_version"`
		Plugins       []MarketPlugin `json:"plugins"`
	}
	if err := json.Unmarshal(body, &registry); err != nil {
		return fallback(fmt.Errorf("market parse %s: %w", src.URL, err))
	}
	if registry.SchemaVersion != 1 && registry.SchemaVersion != marketSchemaVersion {
		return fallback(fmt.Errorf("market %s: unsupported schema_version %d", src.URL, registry.SchemaVersion))
	}
	entries := make([]MarketEntry, 0, len(registry.Plugins))
	for i := range registry.Plugins {
		p := &registry.Plugins[i]
		if err := validateMarketPlugin(p); err != nil {
			return fallback(fmt.Errorf("market %s: plugins[%d] %s: %w", src.URL, i, p.ID, err))
		}
		entries = append(entries, MarketEntry{MarketPlugin: *p, Source: src})
	}
	m.mu.Lock()
	m.cache[src.URL] = marketCacheEntry{plugins: cloneMarketEntries(entries), fetched: time.Now()}
	m.mu.Unlock()
	return entries, nil
}

func cloneMarketEntries(entries []MarketEntry) []MarketEntry {
	if entries == nil {
		return nil
	}
	cloned := make([]MarketEntry, len(entries))
	copy(cloned, entries)
	for i := range cloned {
		cloned[i].Tags = append([]string(nil), entries[i].Tags...)
		cloned[i].Install.Artifacts = append([]MarketArtifact(nil), entries[i].Install.Artifacts...)
		cloned[i].Versions = append([]MarketVersion(nil), entries[i].Versions...)
		for j := range cloned[i].Versions {
			cloned[i].Versions[j].Install.Artifacts = append([]MarketArtifact(nil), entries[i].Versions[j].Install.Artifacts...)
		}
	}
	return cloned
}

func validateMarketPlugin(p *MarketPlugin) error {
	p.ID = strings.TrimSpace(p.ID)
	p.Name = strings.TrimSpace(p.Name)
	p.URL = strings.TrimSpace(p.URL)
	p.Version = normalizeMarketVersion(p.Version)
	p.Repository = strings.TrimSpace(p.Repository)
	if !marketPluginIDPattern.MatchString(p.ID) {
		return fmt.Errorf("invalid id %q", p.ID)
	}
	if p.Name == "" {
		return fmt.Errorf("missing name")
	}
	installType := p.InstallType()
	switch installType {
	case marketInstallSidecar:
		if err := validateMarketURL(p.URL, "url"); err != nil {
			return err
		}
	case marketInstallDirect:
		if p.Version == "" || !validMarketVersion(p.Version) {
			return fmt.Errorf("invalid version %q", p.Version)
		}
		if err := validateMarketInstallPlan(p.Install, true); err != nil {
			return err
		}
	case marketInstallGitHubRelease:
		if err := validateGitHubRepository(p.Repository); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported install type %q", installType)
	}
	if p.Logo != "" {
		if err := validateMarketURL(p.Logo, "logo"); err != nil {
			return err
		}
	}
	if p.Homepage != "" {
		if err := validateMarketURL(p.Homepage, "homepage"); err != nil {
			return err
		}
	}
	seenVersions := map[string]struct{}{}
	for i := range p.Versions {
		version := &p.Versions[i]
		version.Version = normalizeMarketVersion(version.Version)
		if !validMarketVersion(version.Version) {
			return fmt.Errorf("versions[%d]: invalid version %q", i, version.Version)
		}
		if _, ok := seenVersions[version.Version]; ok {
			return fmt.Errorf("versions[%d]: duplicate version %q", i, version.Version)
		}
		seenVersions[version.Version] = struct{}{}
		if installType == marketInstallDirect {
			if err := validateMarketInstallPlan(version.Install, true); err != nil {
				return fmt.Errorf("versions[%d]: %w", i, err)
			}
		}
	}
	return nil
}

func validateMarketInstallPlan(plan MarketInstallPlan, requireArtifacts bool) error {
	plan.Type = strings.ToLower(strings.TrimSpace(plan.Type))
	if plan.Type != marketInstallDirect {
		return fmt.Errorf("unsupported install type %q", plan.Type)
	}
	if requireArtifacts && len(plan.Artifacts) == 0 {
		return fmt.Errorf("direct install requires at least one artifact")
	}
	seen := map[string]struct{}{}
	for i, artifact := range plan.Artifacts {
		artifact.GOOS = normalizeMarketGOOS(artifact.GOOS)
		artifact.GOARCH = normalizeMarketGOARCH(artifact.GOARCH)
		if artifact.GOOS == "" || artifact.GOARCH == "" {
			return fmt.Errorf("artifacts[%d]: goos and goarch are required", i)
		}
		key := artifact.GOOS + "/" + artifact.GOARCH
		if _, ok := seen[key]; ok {
			return fmt.Errorf("artifacts[%d]: duplicate platform %s", i, key)
		}
		seen[key] = struct{}{}
		if err := validateMarketURL(artifact.URL, fmt.Sprintf("artifacts[%d] url", i)); err != nil {
			return err
		}
		artifact.SHA256 = strings.ToLower(strings.TrimSpace(artifact.SHA256))
		if len(artifact.SHA256) != sha256.Size*2 {
			return fmt.Errorf("artifacts[%d]: invalid sha256", i)
		}
		if _, err := hex.DecodeString(artifact.SHA256); err != nil {
			return fmt.Errorf("artifacts[%d]: invalid sha256: %w", i, err)
		}
		if artifact.Size < 0 {
			return fmt.Errorf("artifacts[%d]: invalid size", i)
		}
	}
	return nil
}

func validateGitHubRepository(repository string) error {
	parsed, err := url.Parse(strings.TrimSpace(repository))
	if err != nil || parsed.Scheme != "https" || parsed.Host != "github.com" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("repository must be https://github.com/{owner}/{repo}")
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.HasSuffix(parts[1], ".git") {
		return fmt.Errorf("repository must be https://github.com/{owner}/{repo}")
	}
	return nil
}

func validateMarketURL(raw, label string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return fmt.Errorf("invalid %s url", label)
	}
	if hasSensitiveQuery(parsed) {
		return fmt.Errorf("%s url contains sensitive query parameter", label)
	}
	return nil
}

func validMarketVersion(version string) bool {
	if version == "" || strings.HasPrefix(version, "-") {
		return false
	}
	for _, r := range version {
		if (r >= '0' && r <= '9') || r == '.' || r == '-' || r == '+' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
			continue
		}
		return false
	}
	return true
}

func normalizeMarketVersion(version string) string {
	version = strings.TrimSpace(version)
	if len(version) > 1 && (version[0] == 'v' || version[0] == 'V') {
		return version[1:]
	}
	return version
}

func normalizeMarketGOOS(goos string) string {
	switch strings.ToLower(strings.TrimSpace(goos)) {
	case "mac", "macos", "osx":
		return "darwin"
	default:
		return strings.ToLower(strings.TrimSpace(goos))
	}
}

func normalizeMarketGOARCH(goarch string) string {
	switch strings.ToLower(strings.TrimSpace(goarch)) {
	case "x64", "x86_64":
		return "amd64"
	case "aarch64":
		return "arm64"
	default:
		return strings.ToLower(strings.TrimSpace(goarch))
	}
}

// InstallSpec describes what a legacy sidecar market entry maps to for
// registration. Packaged entries are installed by Service.InstallMarket.
func (e *MarketEntry) InstallSpec() *SidecarManifest {
	return &SidecarManifest{
		ID:           e.ID,
		Version:      firstNonEmpty(e.Version, "1.0.0"),
		Name:         e.Name,
		Description:  e.Description,
		PagePath:     e.PagePath,
		HealthPath:   e.HealthPath,
		APIPrefix:    e.APIPrefix,
		ChannelPath:  e.ChannelPath,
		ConfigFields: e.ConfigFields,
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

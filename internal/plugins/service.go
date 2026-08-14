package plugins

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lan/meta-gateway/internal/store"
)

const (
	StatusInstalled = "installed"
	StatusAvailable = "available"
)

var (
	ErrNotFound      = errors.New("plugin_not_found")
	ErrNotInstalled  = errors.New("plugin_not_installed")
	ErrAlreadyExists = errors.New("plugin_already_installed")
	ErrInvalidID     = errors.New("plugin_invalid_id")
	ErrCoreImmutable = errors.New("plugin_core_immutable")
)

// Module kinds: core is always-on platform capability; addon is optional store-managed.
const (
	KindCore  = "core"
	KindAddon = "addon"
)

// Official catalog entries embedded for v1.
// Only optional add-ons appear here. Core platform features (audit, backups,
// connections, relay, …) are always on and are not store-gated.
var officialCatalog = []CatalogEntry{
	{
		ID:           "exchange",
		Name:         "Exchange",
		Version:      "1.0.0",
		Description:  "Import and export channel assets, plus optional WebDAV backup pull.",
		Kind:         KindAddon,
		Unlocks:      []string{"settings.exchange", "settings.webdav", "admin.exchange", "admin.webdav"},
		Capabilities: []string{"admin_page"},
		Source:       "official",
		Checksum:     "embedded:exchange:1.0.0",
	},
	{
		ID:           "checkin",
		Name:         "Check-in",
		Version:      "1.0.0",
		Description:  "Credential check-in runs, logs, and scheduled jobs for supported platforms.",
		Kind:         KindAddon,
		Unlocks:      []string{"settings.checkins", "connections.checkin", "admin.checkin"},
		Capabilities: []string{"admin_page", "job"},
		Source:       "official",
		Checksum:     "embedded:checkin:1.0.0",
	},
}

// CoreFeatureCards describes always-on platform capabilities shown in the store
// for orientation only (not installable / not disableable).
var CoreFeatureCards = []CatalogEntry{
	{
		ID:          "core-relay",
		Name:        "Relay & routing",
		Version:     "built-in",
		Description: "OpenAI-compatible /v1 relay, multi-channel routing, retries, tokens, and proxy logs.",
		Kind:        KindCore,
		Unlocks:     []string{"nav.connections", "nav.models", "nav.keys", "nav.logs", "v1.relay"},
		Source:      "core",
	},
	{
		ID:          "core-ops",
		Name:        "Audit & backups",
		Version:     "built-in",
		Description: "Admin audit events and online SQLite backups under Settings. Always available.",
		Kind:        KindCore,
		Unlocks:     []string{"settings.audit", "settings.backups", "admin.audit", "admin.backups"},
		Source:      "core",
	},
	{
		ID:          "core-runtime",
		Name:        "Runtime & discovery",
		Version:     "built-in",
		Description: "Runtime parameters and channel model discovery. Always available.",
		Kind:        KindCore,
		Unlocks:     []string{"settings.runtime", "settings.discovery"},
		Source:      "core",
	},
}

// CatalogEntry is one store-listed module. Embedded official entries, remote
// catalog entries, and registered sidecar plugins all surface through this.
type CatalogEntry struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	Description  string   `json:"description,omitempty"`
	Kind         string   `json:"kind,omitempty"` // core | addon
	Unlocks      []string `json:"unlocks,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
	Source       string   `json:"source,omitempty"`
	Checksum     string   `json:"checksum,omitempty"`
	// Sidecar is set for third-party plugins: an external HTTP service that
	// meta-gateway embeds (iframe) and reverse-proxies. Nil for built-ins.
	Sidecar *SidecarSpec `json:"sidecar,omitempty"`
}

// SidecarSpec describes a third-party sidecar plugin service.
type SidecarSpec struct {
	// URL is the plugin service base URL (http/https), e.g. http://127.0.0.1:9100.
	URL string `json:"url"`
	// PagePath is the plugin's embeddable page path (default "/").
	PagePath string `json:"page_path,omitempty"`
	// HealthPath is the health-check path (default "/healthz").
	HealthPath string `json:"health_path,omitempty"`
	// APIPrefix is an optional root-level URL prefix (e.g. "/v0/management")
	// that is reverse-proxied to this plugin. Plugins whose frontend calls a
	// fixed absolute API path (CLIProxyAPI's CPAMC calls /v0/management/*)
	// declare it here so requests land without manual address configuration.
	APIPrefix string `json:"api_prefix,omitempty"`
	// ChannelPath is an optional OpenAI-compatible API path prefix (e.g.
	// "/v1") the plugin exposes as an upstream. When set, the store offers
	// "create channel" — the channel's base_url is {URL}{ChannelPath} and the
	// plugin participates in routing/cooldown/logs like any other upstream.
	ChannelPath string `json:"channel_path,omitempty"`
	// APIKey is the shared secret meta-gateway sends as X-Plugin-Key on every
	// proxied request; the plugin validates it. Empty disables the header.
	APIKey string `json:"api_key,omitempty"`
}

// ModuleStatus is the admin-facing combined view of catalog + install state.
type ModuleStatus struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	Description  string   `json:"description,omitempty"`
	Kind         string   `json:"kind"`
	Unlocks      []string `json:"unlocks,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
	Source       string   `json:"source,omitempty"`
	Installed    bool     `json:"installed"`
	Enabled      bool     `json:"enabled"`
	// CanToggle is false for core cards and orphans that cannot be activated.
	CanToggle bool `json:"can_toggle"`
	// OpenPath is a frontend route hint when the add-on is enabled.
	OpenPath string `json:"open_path,omitempty"`
}

type Manifest struct {
	ID           string            `json:"id"`
	Version      string            `json:"version"`
	Name         string            `json:"name"`
	Description  string            `json:"description,omitempty"`
	Capabilities []string          `json:"capabilities"`
	Admin        map[string]string `json:"admin,omitempty"`
	Permissions  []string          `json:"permissions,omitempty"`
	// Sidecar carries the embedded sidecar spec for third-party plugins.
	Sidecar *SidecarSpec `json:"sidecar,omitempty"`
}

type Service struct {
	dir   string
	store *store.PluginStore

	mu      sync.RWMutex
	enabled map[string]bool
	// onChange listeners fire after enablement map reloads (Enable/Disable/Uninstall/Activate).
	onChange []func(id string, enabled bool)
	// catalogURL optionally loads additional official entries at Catalog() time.
	catalogURL string
	httpClient *http.Client
	// sidecarClient is the dedicated client for plugin manifest fetches,
	// health checks and proxying. Sidecar plugins are locally/privately
	// hosted services the admin explicitly registers (trust model: same as
	// the CPA add-on), so this client intentionally bypasses the outbound
	// SSRF policy that guards relay traffic.
	sidecarClient *http.Client
	// market fetches, validates and caches remote plugin registries.
	market *market
	// remoteCatalog caches the last successful remote fetch for install lookups.
	remoteCatalog []CatalogEntry
}

func NewService(dir string, pluginStore *store.PluginStore) (*Service, error) {
	return NewServiceWithOptions(dir, pluginStore, "", nil)
}

// NewServiceWithOptions allows an optional remote catalog URL (merged with embedded entries).
func NewServiceWithOptions(dir string, pluginStore *store.PluginStore, catalogURL string, client *http.Client) (*Service, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("plugins: mkdir: %w", err)
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	s := &Service{
		dir:           dir,
		store:         pluginStore,
		enabled:       make(map[string]bool),
		catalogURL:    strings.TrimSpace(catalogURL),
		httpClient:    client,
		sidecarClient: &http.Client{Timeout: 10 * time.Second},
	}
	if err := s.reloadEnabled(); err != nil {
		return nil, err
	}
	s.market = newMarket(s.sidecarClient, nil)
	return s, nil
}

// SetMarketURLs appends extra registry URLs to the plugin market (called
// with the PLUGIN_MARKET_URLS env value at startup).
func (s *Service) SetMarketURLs(extra []string) {
	s.market = newMarket(s.sidecarClient, extra)
}

// MarketSources lists the configured registry sources.
func (s *Service) MarketSources() []MarketSource {
	return s.market.Sources()
}

// MarketPlugins lists all installable plugins from all market sources,
// deduplicated by ID (first source wins). A failed source is skipped so one
// bad registry does not empty the market.
func (s *Service) MarketPlugins(ctx context.Context) []MarketEntry {
	entries, err := s.market.List(ctx)
	if err != nil {
		return nil
	}
	return entries
}

// InstallMarket registers a market entry as a sidecar plugin: fetches its
// manifest (or uses the entry's manual fields), health-checks the service,
// then installs + enables — same path as a manual store registration.
func (s *Service) InstallMarket(ctx context.Context, id string) (*store.PluginRecord, error) {
	entries, err := s.market.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("plugin_market_unavailable")
	}
	for _, e := range entries {
		if e.ID == id {
			return s.RegisterSidecar(e.URL, "", e.InstallSpec())
		}
	}
	return nil, ErrNotFound
}

// SetOnChange appends a listener for enablement changes (e.g. check-in scheduler).
// Multiple listeners are supported; nil fn is ignored.
func (s *Service) SetOnChange(fn func(id string, enabled bool)) {
	if fn == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onChange = append(s.onChange, fn)
}

// EnsureOfficialModulesInstalled activates every official add-on that is missing.
// Core platform features are not catalog modules and are never gated here.
// Safe to call on every boot; already-installed add-ons are left as-is so
// operators can still disable individual extensions.
func (s *Service) EnsureOfficialModulesInstalled() error {
	installed, err := s.store.List()
	if err != nil {
		return err
	}
	have := make(map[string]struct{}, len(installed))
	for _, rec := range installed {
		have[rec.ID] = struct{}{}
		// Legacy: operations was briefly a store-gated module; core audit/backups
		// are always on now. Drop the leftover row so the store stays clean.
		if rec.ID == "operations" {
			if err := s.Uninstall("operations"); err != nil && err != ErrNotInstalled {
				return fmt.Errorf("plugins: retire legacy operations: %w", err)
			}
			continue
		}
		// Legacy: the CPA surface moved to the sidecar plugin (cpa-console);
		// the built-in cliproxyapi add-on is retired. Drop the leftover row.
		if rec.ID == "cliproxyapi" {
			if err := s.Uninstall("cliproxyapi"); err != nil && err != ErrNotInstalled {
				return fmt.Errorf("plugins: retire legacy cliproxyapi: %w", err)
			}
			continue
		}
	}
	// Re-list after possible legacy cleanup.
	installed, err = s.store.List()
	if err != nil {
		return err
	}
	have = make(map[string]struct{}, len(installed))
	for _, rec := range installed {
		have[rec.ID] = struct{}{}
	}
	for _, entry := range officialCatalog {
		if _, ok := have[entry.ID]; ok {
			continue
		}
		if _, err := s.Activate(entry.ID); err != nil {
			return fmt.Errorf("plugins: bootstrap %s: %w", entry.ID, err)
		}
	}
	return nil
}

func (s *Service) Catalog() []CatalogEntry {
	out := make([]CatalogEntry, len(officialCatalog))
	copy(out, officialCatalog)
	if s.catalogURL == "" {
		return s.mergeInstalledSidecars(out)
	}
	remote, err := s.fetchRemoteCatalog()
	if err != nil || len(remote) == 0 {
		return s.mergeInstalledSidecars(out)
	}
	s.mu.Lock()
	s.remoteCatalog = append([]CatalogEntry(nil), remote...)
	s.mu.Unlock()
	seen := make(map[string]struct{}, len(out))
	for _, entry := range out {
		seen[entry.ID] = struct{}{}
	}
	for _, entry := range remote {
		if entry.ID == "" {
			continue
		}
		if _, exists := seen[entry.ID]; exists {
			continue
		}
		if validatePluginID(entry.ID) != nil {
			continue
		}
		if entry.Source == "" {
			entry.Source = "remote"
		}
		if entry.Kind == "" {
			entry.Kind = KindAddon
		}
		// Remote entries cannot claim core (core is host-defined only).
		if entry.Kind == KindCore {
			entry.Kind = KindAddon
		}
		out = append(out, entry)
		seen[entry.ID] = struct{}{}
	}
	return s.mergeInstalledSidecars(out)
}

// mergeInstalledSidecars appends persisted sidecar plugins (installed via
// RegisterSidecar) that are not already in the catalog, so the store lists
// them after a restart even though the remote catalog is gone.
func (s *Service) mergeInstalledSidecars(out []CatalogEntry) []CatalogEntry {
	installed, err := s.store.List()
	if err != nil {
		return out
	}
	seen := make(map[string]struct{}, len(out))
	for _, entry := range out {
		seen[entry.ID] = struct{}{}
	}
	for _, rec := range installed {
		if rec.Source != "sidecar" || rec.MetaJSON == "" {
			continue
		}
		if _, exists := seen[rec.ID]; exists {
			continue
		}
		var manifest Manifest
		if err := json.Unmarshal([]byte(rec.MetaJSON), &manifest); err != nil || manifest.Sidecar == nil || manifest.Sidecar.URL == "" {
			continue
		}
		out = append(out, CatalogEntry{
			ID:           manifest.ID,
			Name:         manifest.Name,
			Version:      manifest.Version,
			Description:  manifest.Description,
			Kind:         KindAddon,
			Capabilities: manifest.Capabilities,
			Source:       "sidecar",
			Checksum:     rec.Checksum,
			Sidecar:      manifest.Sidecar,
		})
		seen[rec.ID] = struct{}{}
	}
	return out
}

// Status returns catalog add-ons (with install state) plus core orientation cards.
func (s *Service) Status() ([]ModuleStatus, error) {
	installed, err := s.ListInstalled()
	if err != nil {
		return nil, err
	}
	byID := make(map[string]store.PluginRecord, len(installed))
	for _, rec := range installed {
		byID[rec.ID] = rec
	}
	out := make([]ModuleStatus, 0, len(CoreFeatureCards)+len(s.Catalog())+2)
	for _, core := range CoreFeatureCards {
		out = append(out, ModuleStatus{
			ID:          core.ID,
			Name:        core.Name,
			Version:     core.Version,
			Description: core.Description,
			Kind:        KindCore,
			Unlocks:     append([]string{}, core.Unlocks...),
			Source:      core.Source,
			Installed:   true,
			Enabled:     true,
			CanToggle:   false,
		})
	}
	for _, entry := range s.Catalog() {
		kind := entry.Kind
		if kind == "" {
			kind = KindAddon
		}
		rec, ok := byID[entry.ID]
		st := ModuleStatus{
			ID:           entry.ID,
			Name:         entry.Name,
			Version:      entry.Version,
			Description:  entry.Description,
			Kind:         kind,
			Unlocks:      append([]string{}, entry.Unlocks...),
			Capabilities: append([]string{}, entry.Capabilities...),
			Source:       entry.Source,
			Installed:    ok && rec.Status == StatusInstalled,
			Enabled:      ok && rec.Enabled,
			CanToggle:    kind == KindAddon,
			OpenPath:     openPathFor(entry.ID, entry.Sidecar != nil),
		}
		out = append(out, st)
		delete(byID, entry.ID)
	}
	// Orphans: installed but not in catalog.
	for _, rec := range byID {
		out = append(out, ModuleStatus{
			ID:        rec.ID,
			Name:      rec.ID,
			Version:   rec.Version,
			Kind:      KindAddon,
			Source:    rec.Source,
			Installed: true,
			Enabled:   rec.Enabled,
			CanToggle: false,
		})
	}
	return out, nil
}

func openPathFor(id string, sidecar bool) string {
	if sidecar {
		return "/plugins/" + id
	}
	switch id {
	case "exchange":
		return "/settings?tab=exchange"
	case "checkin":
		return "/checkins"
	default:
		return ""
	}
}

// fetchRemoteCatalog downloads additional catalog entries from catalogURL.
// The payload is either {"plugins":[...]} or a bare array of CatalogEntry.
func (s *Service) fetchRemoteCatalog() ([]CatalogEntry, error) {
	resp, err := s.httpClient.Get(s.catalogURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("plugins: catalog status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var payload struct {
		Plugins []CatalogEntry `json:"plugins"`
	}
	if err := json.Unmarshal(body, &payload); err == nil && len(payload.Plugins) > 0 {
		return payload.Plugins, nil
	}
	var list []CatalogEntry
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, err
	}
	return list, nil
}

func (s *Service) ListInstalled() ([]store.PluginRecord, error) {
	return s.store.List()
}

func (s *Service) IsEnabled(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.enabled[id]
}

func (s *Service) EnabledSnapshot() map[string]bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]bool, len(s.enabled))
	for k, v := range s.enabled {
		out[k] = v
	}
	return out
}

// RegisterSidecar fetches a plugin manifest from an external sidecar service,
// validates it, health-checks the service, and installs + enables the plugin.
// The manifest is persisted (MetaJSON) so the plugin survives restarts.
//
// When the service has no /plugin.json (e.g. CLIProxyAPI's built-in CPAMC
// page), the caller can provide id/name/pagePath explicitly — the health
// check still runs, so a dead service is never registered.
func (s *Service) RegisterSidecar(baseURL, apiKey string, manual *SidecarManifest) (*store.PluginRecord, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return nil, fmt.Errorf("plugin_url_required")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, fmt.Errorf("plugin_url_invalid")
	}
	manifest, err := s.fetchSidecarManifest(baseURL)
	if err != nil {
		// No manifest: fall back to caller-supplied identity when provided.
		if manual != nil && validatePluginID(manual.ID) == nil && strings.TrimSpace(manual.Name) != "" {
			manifest = manual
		} else {
			return nil, err
		}
	}
	if err := validatePluginID(manifest.ID); err != nil {
		return nil, fmt.Errorf("plugin_manifest_invalid_id")
	}
	if strings.TrimSpace(manifest.Version) == "" {
		manifest.Version = "1.0.0"
	}
	if strings.TrimSpace(manifest.Name) == "" {
		return nil, fmt.Errorf("plugin_manifest_missing_name")
	}
	spec := &SidecarSpec{
		URL:         baseURL,
		PagePath:    strings.TrimPrefix(strings.TrimSpace(manifest.SidecarPagePath()), "/"),
		HealthPath:  strings.TrimPrefix(strings.TrimSpace(manifest.SidecarHealthPath()), "/"),
		APIPrefix:   normalizeAPIPrefix(manifest.APIPrefix),
		ChannelPath: normalizeChannelPath(manifest.ChannelPath),
		APIKey:      strings.TrimSpace(apiKey),
	}
	if spec.PagePath == "" {
		spec.PagePath = "/"
	}
	if spec.HealthPath == "" {
		spec.HealthPath = "healthz"
	}
	// API prefix routes must not shadow gateway surfaces.
	if spec.APIPrefix != "" {
		if err := validateAPIPrefix(spec.APIPrefix); err != nil {
			return nil, err
		}
	} // Health check: the sidecar must answer before we consider it installed.
	if err := s.healthCheck(spec); err != nil {
		return nil, fmt.Errorf("plugin_health_check_failed: %w", err)
	}
	entry := CatalogEntry{
		ID:           manifest.ID,
		Name:         manifest.Name,
		Version:      manifest.Version,
		Description:  manifest.Description,
		Kind:         KindAddon,
		Capabilities: manifest.Capabilities,
		Source:       "sidecar",
		Sidecar:      spec,
	}
	s.mu.Lock()
	found := false
	for i := range s.remoteCatalog {
		if s.remoteCatalog[i].ID == entry.ID {
			s.remoteCatalog[i] = entry
			found = true
			break
		}
	}
	if !found {
		s.remoteCatalog = append(s.remoteCatalog, entry)
	}
	s.mu.Unlock()
	// Force the WAL into the main file so a container restart right after
	// registration cannot lose the persisted record.
	_ = s.store.Checkpoint()
	return s.Activate(entry.ID)
}

// UpdateSidecar changes a sidecar plugin's connection spec (URL, API key,
// page/health paths) and re-runs the health check against the new settings.
// The record stays installed/enabled; the persisted manifest is refreshed so
// the change survives restarts.
func (s *Service) UpdateSidecar(id, name string, spec *SidecarSpec) (*store.PluginRecord, error) {
	if spec == nil || strings.TrimSpace(spec.URL) == "" {
		return nil, fmt.Errorf("plugin_url_required")
	}
	parsed, err := url.Parse(strings.TrimSpace(spec.URL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, fmt.Errorf("plugin_url_invalid")
	}
	entry, err := s.catalogEntry(id)
	if err != nil {
		return nil, err
	}
	if entry.Sidecar == nil {
		return nil, fmt.Errorf("plugin_not_sidecar")
	}
	rec, err := s.store.Get(id)
	if err != nil {
		return nil, err
	}
	if rec == nil || rec.Status != StatusInstalled {
		return nil, ErrNotInstalled
	}
	spec.URL = strings.TrimSpace(spec.URL)
	spec.PagePath = strings.TrimPrefix(strings.TrimSpace(spec.PagePath), "/")
	spec.HealthPath = strings.TrimPrefix(strings.TrimSpace(spec.HealthPath), "/")
	spec.APIPrefix = normalizeAPIPrefix(spec.APIPrefix)
	spec.ChannelPath = normalizeChannelPath(spec.ChannelPath)
	if spec.PagePath == "" {
		spec.PagePath = "/"
	}
	if spec.HealthPath == "" {
		spec.HealthPath = "healthz"
	}
	if spec.APIPrefix != "" {
		if err := validateAPIPrefix(spec.APIPrefix); err != nil {
			return nil, err
		}
	}
	// The health check uses the new settings, so a mistyped URL or a dead
	// service is rejected and the old config stays in place.
	if err := s.healthCheck(spec); err != nil {
		return nil, fmt.Errorf("plugin_health_check_failed: %w", err)
	}
	s.mu.Lock()
	for i := range s.remoteCatalog {
		if s.remoteCatalog[i].ID == id {
			if strings.TrimSpace(name) != "" {
				s.remoteCatalog[i].Name = strings.TrimSpace(name)
			}
			s.remoteCatalog[i].Sidecar = spec
			break
		}
	}
	s.mu.Unlock()
	// Persist the refreshed manifest so restart recovery picks up the new spec.
	manifest := Manifest{
		ID:           id,
		Version:      entry.Version,
		Name:         entry.Name,
		Description:  entry.Description,
		Capabilities: entry.Capabilities,
		Admin: map[string]string{
			"route":     "/" + id,
			"nav_label": entry.Name,
		},
		Permissions: []string{"admin_api:" + id},
		Sidecar:     spec,
	}
	body, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := s.store.UpdateMeta(id, string(body)); err != nil {
		return nil, err
	}
	_ = s.store.Checkpoint()
	return s.store.Get(id)
}

// SidecarManifest is the JSON a third-party plugin serves at /plugin.json.
// It embeds the plugin's identity plus optional page/health paths.
type SidecarManifest struct {
	ID           string   `json:"id"`
	Version      string   `json:"version"`
	Name         string   `json:"name"`
	Description  string   `json:"description,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
	PagePath     string   `json:"page_path,omitempty"`
	HealthPath   string   `json:"health_path,omitempty"`
	APIPrefix    string   `json:"api_prefix,omitempty"`
	ChannelPath  string   `json:"channel_path,omitempty"`
}

// SidecarPagePath returns the plugin's embeddable page path (default "/").
func (m *SidecarManifest) SidecarPagePath() string {
	if p := strings.TrimSpace(m.PagePath); p != "" {
		return p
	}
	return "/"
}

// SidecarHealthPath returns the plugin's health path (default "/healthz").
func (m *SidecarManifest) SidecarHealthPath() string {
	if p := strings.TrimSpace(m.HealthPath); p != "" {
		return p
	}
	return "healthz"
}

// normalizeAPIPrefix trims whitespace and guarantees a leading slash with no
// trailing slash ("" stays empty).
func normalizeAPIPrefix(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return strings.TrimRight(p, "/")
}

// normalizeChannelPath is normalizeAPIPrefix for the OpenAI-compatible
// channel prefix (e.g. "/v1").
func normalizeChannelPath(p string) string {
	return normalizeAPIPrefix(p)
}

// validateAPIPrefix rejects prefixes that would shadow gateway surfaces.
func validateAPIPrefix(p string) error {
	if p == "" {
		return nil
	}
	if !strings.HasPrefix(p, "/") {
		return fmt.Errorf("plugin_api_prefix_invalid")
	}
	first := strings.TrimPrefix(p, "/")
	seg := first
	if i := strings.IndexByte(seg, '/'); i >= 0 {
		seg = seg[:i]
	}
	switch seg {
	case "admin", "console", "v1", "readyz", "healthz", "metrics", "cpa":
		return fmt.Errorf("plugin_api_prefix_conflict")
	}
	return nil
}

// PrefixForwarder pairs a root-level API prefix with the sidecar spec that
// serves it.
type PrefixForwarder struct {
	Prefix string
	Spec   *SidecarSpec
}

// PrefixForwarders returns all root-level API prefixes declared by enabled
// sidecar plugins (deduplicated, first plugin wins). It includes plugins
// recovered from the store, so a restart does not lose declared prefixes.
func (s *Service) PrefixForwarders() []PrefixForwarder {
	entries := s.Catalog()
	s.mu.RLock()
	defer s.mu.RUnlock()
	seen := map[string]bool{}
	var out []PrefixForwarder
	for _, entry := range entries {
		if entry.Sidecar == nil || entry.Sidecar.APIPrefix == "" {
			continue
		}
		if !s.enabled[entry.ID] {
			continue
		}
		p := normalizeAPIPrefix(entry.Sidecar.APIPrefix)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		spec := entry.Sidecar
		out = append(out, PrefixForwarder{Prefix: p, Spec: spec})
	}
	return out
}

func (s *Service) fetchSidecarManifest(url string) (*SidecarManifest, error) {
	manifestURL := strings.TrimRight(url, "/") + "/plugin.json"
	req, err := http.NewRequest(http.MethodGet, manifestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("plugin_manifest_request")
	}
	resp, err := s.sidecarClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("plugin_manifest_unreachable")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("plugin_manifest_status_%d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("plugin_manifest_read")
	}
	var manifest SidecarManifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return nil, fmt.Errorf("plugin_manifest_invalid_json")
	}
	if validatePluginID(manifest.ID) != nil {
		return nil, fmt.Errorf("plugin_manifest_invalid_id")
	}
	return &manifest, nil
}

// healthCheck probes the sidecar's health path.
func (s *Service) healthCheck(spec *SidecarSpec) error {
	if spec == nil || spec.URL == "" {
		return fmt.Errorf("no sidecar spec")
	}
	healthURL := strings.TrimRight(spec.URL, "/") + "/" + strings.TrimPrefix(spec.HealthPath, "/")
	client := s.sidecarClient
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
	if err != nil {
		return err
	}
	if spec.APIKey != "" {
		req.Header.Set("X-Plugin-Key", spec.APIKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

// SidecarFor returns the sidecar spec of an installed, enabled plugin, or nil.
func (s *Service) SidecarFor(id string) (*SidecarSpec, error) {
	if !s.IsEnabled(id) {
		return nil, ErrNotInstalled
	}
	entry, err := s.catalogEntry(id)
	if err != nil {
		return nil, err
	}
	if entry.Sidecar == nil {
		return nil, ErrNotFound
	}
	return entry.Sidecar, nil
}

func (s *Service) Install(id string) (*store.PluginRecord, error) {
	entry, err := s.catalogEntry(id)
	if err != nil {
		return nil, err
	}
	if entry.Kind == KindCore || strings.HasPrefix(entry.ID, "core-") {
		return nil, ErrCoreImmutable
	}
	if existing, err := s.store.Get(id); err != nil {
		return nil, err
	} else if existing != nil && existing.Status == StatusInstalled {
		return nil, ErrAlreadyExists
	}

	pluginDir, err := s.safePluginDir(id)
	if err != nil {
		return nil, err
	}
	// Stage under a temp directory, then rename into place for best-effort atomicity.
	stageDir := pluginDir + ".staging"
	_ = os.RemoveAll(stageDir)
	if err := os.MkdirAll(stageDir, 0o700); err != nil {
		return nil, err
	}
	cleanupStage := true
	defer func() {
		if cleanupStage {
			_ = os.RemoveAll(stageDir)
		}
	}()

	manifest := Manifest{
		ID:           entry.ID,
		Version:      entry.Version,
		Name:         entry.Name,
		Description:  entry.Description,
		Capabilities: entry.Capabilities,
		Admin: map[string]string{
			"route":     "/" + entry.ID,
			"nav_label": entry.Name,
		},
		Permissions: []string{"admin_api:" + entry.ID},
		Sidecar:     entry.Sidecar,
	}
	body, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(stageDir, "plugin.json"), body, 0o600); err != nil {
		return nil, err
	}
	readme := fmt.Sprintf("# %s\n\nOfficial Meta Gateway feature module (host-mediated).\nVersion: %s\n", entry.Name, entry.Version)
	if err := os.WriteFile(filepath.Join(stageDir, "README.md"), []byte(readme), 0o600); err != nil {
		return nil, err
	}

	_ = os.RemoveAll(pluginDir)
	if err := os.Rename(stageDir, pluginDir); err != nil {
		return nil, err
	}
	cleanupStage = false

	sum := sha256.Sum256(body)
	now := time.Now().UTC()
	rec := &store.PluginRecord{
		ID:          entry.ID,
		Version:     entry.Version,
		Status:      StatusInstalled,
		Enabled:     false,
		Source:      entry.Source,
		Checksum:    hex.EncodeToString(sum[:]),
		InstalledAt: &now,
		MetaJSON:    string(body),
	}
	if err := s.store.Upsert(rec); err != nil {
		_ = os.RemoveAll(pluginDir)
		return nil, err
	}
	return rec, nil
}

// Activate installs (if needed) then enables a catalog module.
func (s *Service) Activate(id string) (*store.PluginRecord, error) {
	if _, err := s.catalogEntry(id); err != nil {
		return nil, err
	}
	existing, err := s.store.Get(id)
	if err != nil {
		return nil, err
	}
	if existing == nil || existing.Status != StatusInstalled {
		if _, err := s.Install(id); err != nil && !errors.Is(err, ErrAlreadyExists) {
			return nil, err
		}
	}
	return s.Enable(id)
}

func (s *Service) Enable(id string) (*store.PluginRecord, error) {
	// Catalogued modules (embedded or remote cache) can be enabled. Orphans without
	// a catalog entry remain uninstall-only.
	if _, err := s.catalogEntry(id); err != nil {
		return nil, err
	}
	rec, err := s.requireInstalled(id)
	if err != nil {
		return nil, err
	}
	if _, err := s.readManifest(id); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	rec.Enabled = true
	rec.EnabledAt = &now
	rec.Status = StatusInstalled
	if err := s.store.Upsert(rec); err != nil {
		return nil, err
	}
	if err := s.reloadEnabled(); err != nil {
		return nil, err
	}
	s.notifyChange(id, true)
	return rec, nil
}

func (s *Service) Disable(id string) (*store.PluginRecord, error) {
	if entry, err := s.catalogEntry(id); err == nil && entry.Kind == KindCore {
		return nil, ErrCoreImmutable
	}
	if strings.HasPrefix(id, "core-") {
		return nil, ErrCoreImmutable
	}
	rec, err := s.requireInstalled(id)
	if err != nil {
		return nil, err
	}
	rec.Enabled = false
	rec.EnabledAt = nil
	if err := s.store.Upsert(rec); err != nil {
		return nil, err
	}
	if err := s.reloadEnabled(); err != nil {
		return nil, err
	}
	s.notifyChange(id, false)
	return rec, nil
}

func (s *Service) Uninstall(id string) error {
	if err := validatePluginID(id); err != nil {
		return err
	}
	if entry, err := s.catalogEntry(id); err == nil && entry.Kind == KindCore {
		return ErrCoreImmutable
	}
	if strings.HasPrefix(id, "core-") {
		return ErrCoreImmutable
	}
	rec, err := s.store.Get(id)
	if err != nil {
		return err
	}
	if rec == nil {
		return ErrNotInstalled
	}
	// Drop DB state first so enable gates clear even if filesystem cleanup fails.
	if err := s.store.Delete(id); err != nil {
		return err
	}
	if err := s.reloadEnabled(); err != nil {
		return err
	}
	s.notifyChange(id, false)
	pluginDir, err := s.safePluginDir(id)
	if err != nil {
		return err
	}
	_ = os.RemoveAll(pluginDir)
	return nil
}

func (s *Service) reloadEnabled() error {
	ids, err := s.store.EnabledIDs()
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.enabled = ids
	s.mu.Unlock()
	return nil
}

func (s *Service) notifyChange(id string, enabled bool) {
	s.mu.RLock()
	listeners := append([]func(string, bool){}, s.onChange...)
	s.mu.RUnlock()
	for _, fn := range listeners {
		if fn != nil {
			fn(id, enabled)
		}
	}
}

func (s *Service) requireInstalled(id string) (*store.PluginRecord, error) {
	if err := validatePluginID(id); err != nil {
		return nil, err
	}
	rec, err := s.store.Get(id)
	if err != nil {
		return nil, err
	}
	if rec == nil || rec.Status != StatusInstalled {
		return nil, ErrNotInstalled
	}
	return rec, nil
}

func (s *Service) readManifest(id string) (*Manifest, error) {
	pluginDir, err := s.safePluginDir(id)
	if err != nil {
		return nil, err
	}
	body, err := os.ReadFile(filepath.Join(pluginDir, "plugin.json"))
	if err != nil {
		return nil, fmt.Errorf("plugins: read manifest: %w", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return nil, fmt.Errorf("plugins: parse manifest: %w", err)
	}
	if manifest.ID != id {
		return nil, fmt.Errorf("plugins: manifest id mismatch")
	}
	return &manifest, nil
}

func (s *Service) safePluginDir(id string) (string, error) {
	if err := validatePluginID(id); err != nil {
		return "", err
	}
	base, err := filepath.Abs(s.dir)
	if err != nil {
		return "", err
	}
	target, err := filepath.Abs(filepath.Join(base, id))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(base, target)
	if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", fmt.Errorf("plugins: path escape rejected")
	}
	return target, nil
}

func catalogByID(id string) (*CatalogEntry, error) {
	if err := validatePluginID(id); err != nil {
		return nil, err
	}
	for i := range officialCatalog {
		if officialCatalog[i].ID == id {
			entry := officialCatalog[i]
			return &entry, nil
		}
	}
	return nil, ErrNotFound
}

func (s *Service) catalogEntry(id string) (*CatalogEntry, error) {
	if entry, err := catalogByID(id); err == nil {
		return entry, nil
	}
	s.mu.RLock()
	for i := range s.remoteCatalog {
		if s.remoteCatalog[i].ID == id {
			entry := s.remoteCatalog[i]
			s.mu.RUnlock()
			return &entry, nil
		}
	}
	s.mu.RUnlock()
	// Restart recovery: a registered sidecar plugin is persisted in the
	// plugins table (MetaJSON holds its manifest). Rebuild the catalog entry
	// so enable/disable/proxy keep working after a restart without the
	// original registration request.
	rec, err := s.store.Get(id)
	if err != nil || rec == nil || rec.Source != "sidecar" || rec.MetaJSON == "" {
		return nil, ErrNotFound
	}
	var manifest Manifest
	if err := json.Unmarshal([]byte(rec.MetaJSON), &manifest); err != nil || manifest.Sidecar == nil || manifest.Sidecar.URL == "" {
		return nil, ErrNotFound
	}
	entry := CatalogEntry{
		ID:           manifest.ID,
		Name:         manifest.Name,
		Version:      manifest.Version,
		Description:  manifest.Description,
		Kind:         KindAddon,
		Capabilities: manifest.Capabilities,
		Source:       "sidecar",
		Checksum:     rec.Checksum,
		Sidecar:      manifest.Sidecar,
	}
	// Cache it so subsequent lookups skip the DB round-trip.
	s.mu.Lock()
	s.remoteCatalog = append(s.remoteCatalog, entry)
	s.mu.Unlock()
	return &entry, nil
}

func validatePluginID(id string) error {
	id = strings.TrimSpace(id)
	if id == "" || len(id) > 64 {
		return ErrInvalidID
	}
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return ErrInvalidID
	}
	return nil
}

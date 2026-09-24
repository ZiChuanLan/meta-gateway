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
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lan/meta-gateway/internal/store"
)

const (
	StatusInstalled       = "installed"
	StatusAvailable       = "available"
	maxPluginCatalogBytes = 1 << 20
	// MaxPluginConfigBytes bounds a plugin's persisted config JSON so the
	// object stays comfortably inside a single X-Plugin-Config header.
	MaxPluginConfigBytes = 4 << 10 // 4 KiB
)

// XPluginConfigHeader is injected into every proxied sidecar request carrying
// the plugin's persisted config as a base64 JSON header. Plugins read it to
// configure themselves without re-declaring their own settings store.
const XPluginConfigHeader = "X-Plugin-Config"

// SecretMask is the value admin UIs submit for a secret config field to mean
// "keep the stored value". An empty string clears the stored secret.
const SecretMask = "••••••••••"

var (
	ErrNotFound           = errors.New("plugin_not_found")
	ErrNotInstalled       = errors.New("plugin_not_installed")
	ErrAlreadyExists      = errors.New("plugin_already_installed")
	ErrInvalidID          = errors.New("plugin_invalid_id")
	ErrCoreImmutable      = errors.New("plugin_core_immutable")
	ErrMarketSourceNeeded = errors.New("plugin_market_source_required")
)

// Module kinds: core is always-on platform capability; addon is optional store-managed.
const (
	KindCore  = "core"
	KindAddon = "addon"
)

// Official catalog entries embedded for v1.
//
// Deliberately empty: the two official add-ons that used to live here
// (exchange, checkin) are built-in platform features — they ship with the
// gateway, are always on, and have no meaningful off switch to offer. Listing
// them as store extensions produced a toggle that the boot bootstrap kept
// putting back, which is decoration, not capability. Installable plugins come
// from the market registry or the catalog URL instead.
var officialCatalog = []CatalogEntry{}

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

// ConfigFieldType enumerates the supported sidecar configuration input types.
type ConfigFieldType string

const (
	ConfigString ConfigFieldType = "string"
	ConfigText   ConfigFieldType = "text"
	ConfigNumber ConfigFieldType = "number"
	ConfigBool   ConfigFieldType = "bool"
	ConfigSelect ConfigFieldType = "select"
	// ConfigSecret values are masked on read and never echoed back.
	ConfigSecret ConfigFieldType = "secret"
	// ConfigModelGroups is a list of named model groups. Each entry carries a
	// name, an optional description of what the group is for, and the models in
	// it; the stored value is JSON. The console renders it as an editor with a
	// picker fed by the gateway's own model list, because a field whose value is
	// a structured list has no business being hand-written into a textarea.
	ConfigModelGroups ConfigFieldType = "model_groups"
	// ConfigModel picks ONE model from the gateway's routable list. Unlike
	// ConfigString it cannot be set to a name nothing serves — which is the
	// whole point: "the fallback model" is a choice, not a piece of prose.
	ConfigModel ConfigFieldType = "model"
)

// ConfigField declares one configuration input a sidecar plugin accepts.
// The gateway stores the plugin's config object verbatim and injects it into
// proxied requests as a base64 JSON header; it does not validate semantics.
type ConfigField struct {
	Key         string          `json:"key"`
	Type        ConfigFieldType `json:"type,omitempty"` // defaults to string
	Label       string          `json:"label,omitempty"`
	Description string          `json:"description,omitempty"`
	Required    bool            `json:"required,omitempty"`
	Secret      bool            `json:"secret,omitempty"` // implied by Type=secret
	Default     any             `json:"default,omitempty"`
	Options     []string        `json:"options,omitempty"` // for select
	// Advanced fields are tucked into a collapsed group by the console: they
	// matter to someone specific and are noise to everyone else. The plugin's
	// own manifest decides which is which.
	Advanced bool `json:"advanced,omitempty"`
}

// CatalogEntry is one store-listed module. Embedded official entries, remote
// catalog entries, and registered sidecar plugins all surface through this.
type CatalogEntry struct {
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	Version      string        `json:"version"`
	Description  string        `json:"description,omitempty"`
	Kind         string        `json:"kind,omitempty"` // core | addon
	Unlocks      []string      `json:"unlocks,omitempty"`
	Capabilities []string      `json:"capabilities,omitempty"`
	Permissions  []string      `json:"permissions,omitempty"`
	ConfigFields []ConfigField `json:"config_fields,omitempty"`
	Source       string        `json:"source,omitempty"`
	Checksum     string        `json:"checksum,omitempty"`
	// Hooks carries the sidecar's declared intercept points from its manifest
	// through install, so the persisted plugin record keeps them.
	Hooks *HookSet `json:"hooks,omitempty"`
	// Sidecar is set for third-party plugins: an external HTTP service that
	// meta-gateway embeds (iframe) and reverse-proxies. Nil for built-ins.
	Sidecar *SidecarSpec `json:"sidecar,omitempty"`
}

// SidecarSpec describes a third-party sidecar plugin service.
// Config values are stored separately by the host.

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
	// Market source metadata is persisted so a packaged plugin can be matched
	// back to its registry after a restart.
	MarketSourceID  string `json:"market_source_id,omitempty"`
	MarketSourceURL string `json:"market_source_url,omitempty"`
	InstallType     string `json:"install_type,omitempty"`
	ArtifactSHA256  string `json:"artifact_sha256,omitempty"`
	// Managed means the gateway owns the sidecar process and starts it from
	// Entrypoint on boot/enable. Manually registered services remain unmanaged.
	Managed bool `json:"managed,omitempty"`
	// Entrypoint is relative to the installed plugin directory for managed
	// packages. RunArgs are passed without invoking a shell.
	Entrypoint string   `json:"entrypoint,omitempty"`
	RunArgs    []string `json:"run_args,omitempty"`
}

// ModuleStatus is the admin-facing combined view of catalog + install state.
type ModuleStatus struct {
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	Version      string        `json:"version"`
	Description  string        `json:"description,omitempty"`
	Kind         string        `json:"kind"`
	Unlocks      []string      `json:"unlocks,omitempty"`
	Capabilities []string      `json:"capabilities,omitempty"`
	Permissions  []string      `json:"permissions,omitempty"`
	ConfigFields []ConfigField `json:"config_fields,omitempty"`
	Source       string        `json:"source,omitempty"`
	Installed    bool          `json:"installed"`
	Enabled      bool          `json:"enabled"`
	// CanToggle is false for core cards and orphans that cannot be activated.
	CanToggle bool `json:"can_toggle"`
	// OpenPath is a frontend route hint when the add-on is enabled.
	OpenPath string `json:"open_path,omitempty"`
	// HasConfig reports whether a persisted config object exists for this plugin.
	HasConfig bool `json:"has_config,omitempty"`
}

type Manifest struct {
	ID           string            `json:"id"`
	Version      string            `json:"version"`
	Name         string            `json:"name"`
	Description  string            `json:"description,omitempty"`
	Capabilities []string          `json:"capabilities"`
	Admin        map[string]string `json:"admin,omitempty"`
	Permissions  []string          `json:"permissions,omitempty"`
	ConfigFields []ConfigField     `json:"config_fields,omitempty"`
	// Hooks declares the intercept points this plugin serves. Loading them
	// additionally requires the relay:intercept permission.
	Hooks *HookSet `json:"hooks,omitempty"`
	// Sidecar carries the embedded sidecar spec for third-party plugins.
	Sidecar *SidecarSpec `json:"sidecar,omitempty"`
}

type Service struct {
	dir   string
	store *store.PluginStore

	mu      sync.RWMutex
	enabled map[string]bool
	// prefixForwarders is rebuilt with enabled state and persisted manifests.
	// Root-path proxy requests read this snapshot without fetching a remote
	// catalog or querying SQLite on every unmatched request.
	prefixForwarders []PrefixForwarder
	// hookEntries holds the enabled intercept declarations grouped by point and
	// already priority-ordered. Rebuilt with enabled state, so the forward hot
	// path never parses a manifest.
	hookEntries map[string][]hookEntry
	// hookHealth trips a hook that keeps failing. It lives outside s.mu so a
	// slow plugin can never block an enablement reload.
	hookHealth hookBreaker
	// configs caches persisted plugin config JSON keyed by plugin id.
	configs map[string]string
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
	// processMu protects managed sidecar processes independently from the
	// catalog/config mutex, so proxy requests never wait on process I/O.
	processMu sync.Mutex
	processes map[string]*managedProcess
	// market fetches, validates and caches remote plugin registries.
	market *market
	// remoteCatalog caches the last successful remote fetch for install lookups.
	remoteCatalog []CatalogEntry
}

// newSidecarClient builds the dedicated client for plugin traffic: manifest
// fetches, health checks, hook calls, and the reverse proxy.
//
// It deliberately does NOT inherit the ambient HTTP_PROXY. A sidecar runs next
// to the gateway (host.docker.internal, a LAN address, a sibling container),
// and an operator's outbound proxy exists to reach the internet — not the host
// the gateway is already on. Inheriting it broke plugin registration outright
// in a container whose proxy came from the host environment: measured with
// HTTP_PROXY=http://127.0.0.1:7897 and NO_PROXY=127.0.0.1,localhost, every
// manifest fetch became "plugin_manifest_unreachable" while curl reached the
// very same plugin — curl simply does not read the uppercase variable.
func newSidecarClient(timeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// nil means "never proxy". An empty func would fall back to the
	// environment, which is exactly the inheritance being avoided.
	transport.Proxy = nil
	return &http.Client{Timeout: timeout, Transport: transport}
}

// SidecarTransport returns the round tripper every plugin request uses.
//
// The reverse proxy shares it so a plugin PAGE is reached exactly the way a
// health check is: directly, never through the operator's outbound proxy. A
// proxy that only affects some plugin traffic is the worst kind — the plugin
// registers fine and its page hangs.
func (s *Service) SidecarTransport() http.RoundTripper {
	if s.sidecarClient == nil {
		return newSidecarClient(10 * time.Second).Transport
	}
	return s.sidecarClient.Transport
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
		configs:       make(map[string]string),
		catalogURL:    strings.TrimSpace(catalogURL),
		httpClient:    client,
		sidecarClient: newSidecarClient(10 * time.Second),
		processes:     make(map[string]*managedProcess),
		hookEntries:   make(map[string][]hookEntry),
	}
	if err := s.reloadEnabled(); err != nil {
		return nil, err
	}
	s.market = newMarket(s.httpClient, nil)
	return s, nil
}

// SetMarketURLs appends extra registry URLs to the plugin market (called
// with the PLUGIN_MARKET_URLS env value at startup).
func (s *Service) SetMarketURLs(extra []string) {
	s.market = newMarket(s.httpClient, extra)
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

// InstallMarket installs a market entry. Legacy sidecar entries register an
// already-running URL; packaged entries are downloaded, verified, extracted,
// and started through the same sidecar protocol.
func (s *Service) InstallMarket(ctx context.Context, id string) (*store.PluginRecord, error) {
	return s.InstallMarketFrom(ctx, id, "", "")
}

// InstallMarketFrom selects a registry source and optional version explicitly.
// An explicit source is required when two configured registries publish the
// same plugin ID, preventing an accidental source switch during updates.
func (s *Service) InstallMarketFrom(ctx context.Context, id, sourceID, version string) (*store.PluginRecord, error) {
	entries, err := s.market.listAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("plugin_market_unavailable")
	}
	matches := make([]MarketEntry, 0, 1)
	for _, entry := range entries {
		if entry.ID == id && (strings.TrimSpace(sourceID) == "" || entry.Source.ID == strings.TrimSpace(sourceID)) {
			matches = append(matches, entry)
		}
	}
	if len(matches) == 0 {
		return nil, ErrNotFound
	}
	if len(matches) > 1 {
		return nil, ErrMarketSourceNeeded
	}
	entry := matches[0]
	if entry.InstallType() == marketInstallSidecar {
		return s.RegisterSidecar(entry.URL, "", entry.InstallSpec())
	}
	return s.installPackagedMarket(ctx, entry, version)
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

// RetireLegacyModules drops plugin records left behind by store-gated
// features that are built into the gateway today (exchange, check-in) or were
// removed outright (operations, the built-in cliproxyapi add-on). None of them
// has anything to install or enable any more: keeping the record would keep a
// phantom "extension" row in the store that only pretends to gate a feature.
// Safe to call on every boot.
func (s *Service) RetireLegacyModules() error {
	installed, err := s.store.List()
	if err != nil {
		return err
	}
	for _, rec := range installed {
		switch rec.ID {
		case "operations", "cliproxyapi", "exchange", "checkin":
			if err := s.Uninstall(rec.ID); err != nil && err != ErrNotInstalled {
				return fmt.Errorf("plugins: retire legacy module %s: %w", rec.ID, err)
			}
		}
	}
	// Re-list after the cleanup.
	installed, err = s.store.List()
	if err != nil {
		return err
	}
	have := make(map[string]struct{}, len(installed))
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
		if (rec.Source != "sidecar" && !strings.HasPrefix(rec.Source, "market:")) || rec.MetaJSON == "" {
			continue
		}
		if _, exists := seen[rec.ID]; exists {
			continue
		}
		var manifest Manifest
		if err := json.Unmarshal([]byte(rec.MetaJSON), &manifest); err != nil || manifest.Sidecar == nil {
			continue
		}
		out = append(out, CatalogEntry{
			ID:           manifest.ID,
			Name:         manifest.Name,
			Version:      manifest.Version,
			Description:  manifest.Description,
			Kind:         KindAddon,
			Capabilities: manifest.Capabilities,
			ConfigFields: manifest.ConfigFields,
			Source:       rec.Source,
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
			ConfigFields: append([]ConfigField{}, entry.ConfigFields...),
			Source:       entry.Source,
			Installed:    ok && rec.Status == StatusInstalled,
			Enabled:      ok && rec.Enabled,
			CanToggle:    kind == KindAddon,
			OpenPath:     openPathFor(entry.ID, entry.Sidecar != nil),
			HasConfig:    s.PluginConfig(entry.ID) != "",
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
	return ""
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
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPluginCatalogBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxPluginCatalogBytes {
		return nil, fmt.Errorf("plugins: catalog response too large")
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

// PluginConfig returns the persisted config JSON for a plugin ("" when
// unset or the plugin is not installed). Config is safe to return to the
// sidecar via X-Plugin-Config on every proxied request.
func (s *Service) PluginConfig(id string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.configs[id]
}

// SetPluginConfig persists the plugin's runtime config JSON. The value must
// be a non-empty JSON object and is bounded by MaxPluginConfigBytes. Secret
// fields (declared via config_fields type=secret) submitted with SecretMask
// keep their previously stored value; an empty value clears the secret.
func (s *Service) SetPluginConfig(id string, raw string) error {
	if err := validatePluginID(id); err != nil {
		return err
	}
	if _, err := s.requireInstalled(id); err != nil {
		return err
	}
	if len(raw) > MaxPluginConfigBytes {
		return fmt.Errorf("plugin_config_too_large")
	}
	// Must be a valid non-empty JSON object.
	var object map[string]any
	if err := json.Unmarshal([]byte(raw), &object); err != nil || object == nil {
		return fmt.Errorf("plugin_config_invalid")
	}
	if fields := s.ConfigFieldsFor(id); len(fields) > 0 {
		current := map[string]any{}
		if prev := s.PluginConfig(id); prev != "" {
			_ = json.Unmarshal([]byte(prev), &current)
		}
		for i := range fields {
			if fields[i].Type != ConfigSecret {
				continue
			}
			value, ok := object[fields[i].Key]
			if !ok {
				continue
			}
			if text, isString := value.(string); isString && text == SecretMask {
				if old, exists := current[fields[i].Key]; exists {
					object[fields[i].Key] = old
				} else {
					delete(object, fields[i].Key)
				}
			}
		}
		normalized, err := json.Marshal(object)
		if err != nil {
			return fmt.Errorf("plugin_config_invalid")
		}
		raw = string(normalized)
	}
	if err := s.store.SetConfig(id, raw); err != nil {
		return err
	}
	s.mu.Lock()
	s.configs[id] = raw
	s.mu.Unlock()
	return nil
}

// MaskPluginConfigJSON returns config with every non-empty secret field
// replaced by SecretMask so admin responses never carry stored secrets.
func MaskPluginConfigJSON(raw string, fields []ConfigField) (string, error) {
	var object map[string]any
	if err := json.Unmarshal([]byte(raw), &object); err != nil {
		return raw, nil
	}
	masked := false
	for i := range fields {
		if fields[i].Type != ConfigSecret {
			continue
		}
		value, ok := object[fields[i].Key]
		if !ok || value == nil {
			continue
		}
		if text, isString := value.(string); isString && text != "" {
			object[fields[i].Key] = SecretMask
			masked = true
		}
	}
	if !masked {
		return raw, nil
	}
	body, err := json.Marshal(object)
	if err != nil {
		return raw, nil
	}
	return string(body), nil
}

// ConfigFieldsFor returns the ConfigFields declared by a plugin's catalog
// entry (nil if the plugin declares none or does not exist).
func (s *Service) ConfigFieldsFor(id string) []ConfigField {
	entry, err := s.catalogEntry(id)
	if err != nil {
		return nil
	}
	return entry.ConfigFields
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
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.RawPath != "" {
		return nil, fmt.Errorf("plugin_url_invalid")
	}
	if err := validateSidecarPath(parsed.Path); err != nil {
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
	if err := validateConfigFields(manifest.ConfigFields); err != nil {
		return nil, fmt.Errorf("plugin_manifest_invalid_config: %w", err)
	}
	if err := validatePermissions(manifest.Permissions); err != nil {
		return nil, fmt.Errorf("plugin_manifest_invalid_permissions: %w", err)
	}
	if err := ValidateHookSet(manifest.Hooks); err != nil {
		// A declaration the gateway cannot call safely is rejected at
		// registration rather than silently dropped at load time.
		return nil, fmt.Errorf("plugin_manifest_invalid_hooks: %w", err)
	}
	if !hasInterceptPermission(manifest.Permissions) && !manifest.Hooks.Empty() {
		return nil, fmt.Errorf("plugin_manifest_hooks_require_permission: %s", InterceptPermission)
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
	if err := validateSidecarPath(spec.PagePath); err != nil {
		return nil, err
	}
	if err := validateSidecarPath(spec.HealthPath); err != nil {
		return nil, err
	}
	if err := validateSidecarPath(spec.ChannelPath); err != nil {
		return nil, err
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
		Permissions:  manifest.Permissions,
		ConfigFields: manifest.ConfigFields,
		Source:       "sidecar",
		Sidecar:      spec,
		Hooks:        manifest.Hooks,
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
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.RawPath != "" {
		return nil, fmt.Errorf("plugin_url_invalid")
	}
	if err := validateSidecarPath(parsed.Path); err != nil {
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
	if err := validateSidecarPath(spec.PagePath); err != nil {
		return nil, err
	}
	if err := validateSidecarPath(spec.HealthPath); err != nil {
		return nil, err
	}
	if err := validateSidecarPath(spec.ChannelPath); err != nil {
		return nil, err
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
	updatedName := entry.Name
	s.mu.Lock()
	for i := range s.remoteCatalog {
		if s.remoteCatalog[i].ID == id {
			if strings.TrimSpace(name) != "" {
				updatedName = strings.TrimSpace(name)
				s.remoteCatalog[i].Name = updatedName
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
		Name:         updatedName,
		Description:  entry.Description,
		Capabilities: entry.Capabilities,
		ConfigFields: entry.ConfigFields,
		Admin: map[string]string{
			"route":     "/" + id,
			"nav_label": entry.Name,
		},
		Permissions: append([]string{"admin_api:" + id}, entry.Permissions...),
		Hooks:       entry.Hooks,
		Sidecar:     spec,
	}
	body, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := s.store.UpdateMeta(id, string(body)); err != nil {
		return nil, err
	}
	if err := s.reloadEnabled(); err != nil {
		return nil, err
	}
	_ = s.store.Checkpoint()
	return s.store.Get(id)
}

// SidecarManifest is the JSON a third-party plugin serves at /plugin.json.
// It embeds the plugin's identity plus optional page/health paths.
type SidecarManifest struct {
	ID           string        `json:"id"`
	Version      string        `json:"version"`
	Name         string        `json:"name"`
	Description  string        `json:"description,omitempty"`
	Capabilities []string      `json:"capabilities,omitempty"`
	Permissions  []string      `json:"permissions,omitempty"`
	ConfigFields []ConfigField `json:"config_fields,omitempty"`
	PagePath     string        `json:"page_path,omitempty"`
	HealthPath   string        `json:"health_path,omitempty"`
	APIPrefix    string        `json:"api_prefix,omitempty"`
	ChannelPath  string        `json:"channel_path,omitempty"`
	Entrypoint   string        `json:"entrypoint,omitempty"`
	RunArgs      []string      `json:"run_args,omitempty"`
	Hooks        *HookSet      `json:"hooks,omitempty"`
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
	if err := validateSidecarPath(p); err != nil {
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

// validateSidecarPath accepts a URL path without a query or fragment and
// rejects traversal/normalization changes before it reaches a reverse proxy.
// Callers may pass either a leading-slash path or a relative health/page path.
func validateSidecarPath(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "/" {
		return nil
	}
	if strings.ContainsAny(raw, "?#%\\") || strings.Contains(raw, "//") || strings.IndexByte(raw, 0) >= 0 {
		return fmt.Errorf("plugin_path_invalid")
	}
	trimmed := strings.Trim(raw, "/")
	for _, segment := range strings.Split(trimmed, "/") {
		if segment == ".." || segment == "." || segment == "" {
			return fmt.Errorf("plugin_path_invalid")
		}
	}
	normalized := "/" + trimmed
	if path.Clean(normalized) != normalized {
		return fmt.Errorf("plugin_path_invalid")
	}
	return nil
}

// PrefixForwarder pairs a root-level API prefix with the sidecar spec that
// serves it.
type PrefixForwarder struct {
	ID     string
	Prefix string
	Spec   *SidecarSpec
}

// PrefixForwarders returns all root-level API prefixes declared by enabled
// sidecar plugins (deduplicated, first plugin wins). It includes plugins
// recovered from the store, so a restart does not lose declared prefixes.
func (s *Service) PrefixForwarders() []PrefixForwarder {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]PrefixForwarder, 0, len(s.prefixForwarders))
	for _, forwarder := range s.prefixForwarders {
		copyForwarder := forwarder
		if forwarder.Spec != nil {
			copySpec := *forwarder.Spec
			copyForwarder.Spec = &copySpec
		}
		out = append(out, copyForwarder)
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
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPluginCatalogBytes+1))
	if err != nil {
		return nil, fmt.Errorf("plugin_manifest_read")
	}
	if len(body) > maxPluginCatalogBytes {
		return nil, fmt.Errorf("plugin_manifest_too_large")
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
		ConfigFields: entry.ConfigFields,
		Admin: map[string]string{
			"route":     "/" + entry.ID,
			"nav_label": entry.Name,
		},
		Permissions: append([]string{"admin_api:" + entry.ID}, entry.Permissions...),
		Hooks:       entry.Hooks,
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
	manifest, err := s.readManifest(id)
	if err != nil {
		return nil, err
	}
	if manifest.Sidecar != nil && manifest.Sidecar.Managed {
		if err := s.startManagedPlugin(context.Background(), id, manifest.Sidecar); err != nil {
			return nil, err
		}
		if err := s.persistManagedSpec(id, manifest.Sidecar); err != nil {
			_ = s.stopManagedPlugin(context.Background(), id)
			return nil, err
		}
		// Starting a managed process assigns a runtime port and may generate a
		// key. Reload the record so the DB update below preserves those values.
		rec, err = s.store.Get(id)
		if err != nil || rec == nil {
			_ = s.stopManagedPlugin(context.Background(), id)
			if err != nil {
				return nil, err
			}
			return nil, ErrNotInstalled
		}
	}
	now := time.Now().UTC()
	rec.Enabled = true
	rec.EnabledAt = &now
	rec.Status = StatusInstalled
	if err := s.store.Upsert(rec); err != nil {
		if manifest.Sidecar != nil && manifest.Sidecar.Managed {
			_ = s.stopManagedPlugin(context.Background(), id)
		}
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
	if err := s.stopManagedPlugin(context.Background(), id); err != nil {
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
	if err := s.stopManagedPlugin(context.Background(), id); err != nil {
		return err
	}
	// Drop DB state first so enable gates clear even if filesystem cleanup fails.
	if err := s.store.Delete(id); err != nil {
		return err
	}
	// Remove any persisted plugin configuration.
	if err := s.store.DeleteConfig(id); err != nil {
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
	records, err := s.store.List()
	if err != nil {
		return err
	}
	// Refresh the config cache from the store on every reload so config
	// survives restarts and stays in sync with the persisted state.
	configs := make(map[string]string, len(records))
	for _, record := range records {
		if raw, err := s.store.GetConfig(record.ID); err == nil && raw != "" {
			configs[record.ID] = raw
		}
	}
	ids := make(map[string]bool, len(records))
	seenPrefixes := make(map[string]struct{})
	forwarders := make([]PrefixForwarder, 0)
	for _, record := range records {
		if !record.Enabled || record.Status != StatusInstalled {
			continue
		}
		ids[record.ID] = true
		if record.MetaJSON == "" {
			continue
		}
		var manifest Manifest
		if err := json.Unmarshal([]byte(record.MetaJSON), &manifest); err != nil || manifest.Sidecar == nil {
			continue
		}
		prefix := normalizeAPIPrefix(manifest.Sidecar.APIPrefix)
		if prefix == "" || validateAPIPrefix(prefix) != nil {
			continue
		}
		if _, duplicate := seenPrefixes[prefix]; duplicate {
			continue
		}
		seenPrefixes[prefix] = struct{}{}
		spec := *manifest.Sidecar
		spec.APIPrefix = prefix
		forwarders = append(forwarders, PrefixForwarder{ID: record.ID, Prefix: prefix, Spec: &spec})
	}
	s.mu.Lock()
	s.enabled = ids
	s.configs = configs
	s.prefixForwarders = forwarders
	s.rebuildHookEntriesLocked(records, ids)
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
	paths := []string{
		filepath.Join(pluginDir, ".meta-gateway.json"),
		filepath.Join(pluginDir, "plugin.json"),
	}
	var lastErr error
	for _, manifestPath := range paths {
		body, err := os.ReadFile(manifestPath)
		if err != nil {
			lastErr = err
			continue
		}
		var manifest Manifest
		if err := json.Unmarshal(body, &manifest); err != nil {
			lastErr = err
			continue
		}
		if manifest.ID != id {
			lastErr = fmt.Errorf("manifest id mismatch")
			continue
		}
		return &manifest, nil
	}
	return nil, fmt.Errorf("plugins: read manifest: %w", lastErr)
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
	if err != nil || rec == nil || rec.MetaJSON == "" ||
		(rec.Source != "sidecar" && !strings.HasPrefix(rec.Source, "market:")) {
		return nil, ErrNotFound
	}
	var manifest Manifest
	if err := json.Unmarshal([]byte(rec.MetaJSON), &manifest); err != nil || manifest.Sidecar == nil {
		return nil, ErrNotFound
	}
	entry := CatalogEntry{
		ID:           manifest.ID,
		Name:         manifest.Name,
		Version:      manifest.Version,
		Description:  manifest.Description,
		Kind:         KindAddon,
		Capabilities: manifest.Capabilities,
		Permissions:  manifest.Permissions,
		ConfigFields: manifest.ConfigFields,
		Source:       rec.Source,
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
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			continue
		}
		return ErrInvalidID
	}
	return nil
}

// validateConfigFields checks a manifest-declared configuration schema: keys
// are identifiers, unique, of a known type, and select fields carry options.
func validateConfigFields(fields []ConfigField) error {
	if len(fields) > 64 {
		return fmt.Errorf("too many config fields")
	}
	seen := make(map[string]struct{}, len(fields))
	for i, field := range fields {
		if !validConfigFieldKey(field.Key) {
			return fmt.Errorf("config_fields[%d]: invalid key %q", i, field.Key)
		}
		if _, ok := seen[field.Key]; ok {
			return fmt.Errorf("config_fields[%d]: duplicate key %q", i, field.Key)
		}
		seen[field.Key] = struct{}{}
		fieldType := field.Type
		if fieldType == "" {
			fieldType = ConfigString
		}
		switch fieldType {
		case ConfigString, ConfigText, ConfigNumber, ConfigBool, ConfigSelect, ConfigSecret, ConfigModelGroups, ConfigModel:
		default:
			return fmt.Errorf("config_fields[%d]: unsupported type %q", i, fieldType)
		}
		if fieldType == ConfigSelect && len(field.Options) == 0 {
			return fmt.Errorf("config_fields[%d]: select requires options", i)
		}
		if field.Default != nil {
			switch fieldType {
			case ConfigNumber:
				if _, ok := field.Default.(float64); !ok {
					return fmt.Errorf("config_fields[%d]: default must be a number", i)
				}
			case ConfigBool:
				if _, ok := field.Default.(bool); !ok {
					return fmt.Errorf("config_fields[%d]: default must be a boolean", i)
				}
			}
		}
	}
	return nil
}

func validConfigFieldKey(key string) bool {
	key = strings.TrimSpace(key)
	if key == "" || len(key) > 64 || (key[0] >= '0' && key[0] <= '9') {
		return false
	}
	for _, r := range key {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

// validatePermissions constrains manifest permission names to the declared
// "resource:action" shape the gateway may gate later.
func validatePermissions(permissions []string) error {
	if len(permissions) > 64 {
		return fmt.Errorf("too many permissions")
	}
	seen := make(map[string]struct{}, len(permissions))
	for i, permission := range permissions {
		permission = strings.TrimSpace(permission)
		if permission == "" || len(permission) > 128 || (permission[0] >= '0' && permission[0] <= '9') {
			return fmt.Errorf("permissions[%d]: invalid name %q", i, permission)
		}
		if _, ok := seen[permission]; ok {
			return fmt.Errorf("permissions[%d]: duplicate %q", i, permission)
		}
		seen[permission] = struct{}{}
		for _, r := range permission {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == ':' || r == '.' || r == '_' || r == '-' {
				continue
			}
			return fmt.Errorf("permissions[%d]: invalid name %q", i, permission)
		}
	}
	return nil
}

package plugins

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
)

// Official catalog entries embedded for v1.
var officialCatalog = []CatalogEntry{
	{
		ID:           "exchange",
		Name:         "Exchange",
		Version:      "1.0.0",
		Description:  "Import and export channel assets via Meta Gateway exchange format.",
		Capabilities: []string{"admin_page"},
		Source:       "official",
		Checksum:     "embedded:exchange:1.0.0",
	},
	{
		ID:           "operations",
		Name:         "Operations",
		Version:      "1.0.0",
		Description:  "Audit events, backups, and operational maintenance tools.",
		Capabilities: []string{"admin_page", "job"},
		Source:       "official",
		Checksum:     "embedded:operations:1.0.0",
	},
	{
		ID:           "checkin",
		Name:         "Check-in",
		Version:      "1.0.0",
		Description:  "Credential check-in runs and logs for supported platforms.",
		Capabilities: []string{"admin_page", "job"},
		Source:       "official",
		Checksum:     "embedded:checkin:1.0.0",
	},
}

type CatalogEntry struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	Description  string   `json:"description,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
	Source       string   `json:"source,omitempty"`
	Checksum     string   `json:"checksum,omitempty"`
}

type Manifest struct {
	ID           string            `json:"id"`
	Version      string            `json:"version"`
	Name         string            `json:"name"`
	Capabilities []string          `json:"capabilities"`
	Admin        map[string]string `json:"admin,omitempty"`
	Permissions  []string          `json:"permissions,omitempty"`
}

type Service struct {
	dir   string
	store *store.PluginStore

	mu      sync.RWMutex
	enabled map[string]bool
}

func NewService(dir string, pluginStore *store.PluginStore) (*Service, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("plugins: mkdir: %w", err)
	}
	s := &Service{
		dir:     dir,
		store:   pluginStore,
		enabled: make(map[string]bool),
	}
	if err := s.reloadEnabled(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Service) Catalog() []CatalogEntry {
	out := make([]CatalogEntry, len(officialCatalog))
	copy(out, officialCatalog)
	return out
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

func (s *Service) Install(id string) (*store.PluginRecord, error) {
	entry, err := catalogByID(id)
	if err != nil {
		return nil, err
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
		Capabilities: entry.Capabilities,
		Admin: map[string]string{
			"route":     "/" + entry.ID,
			"nav_label": entry.Name,
		},
		Permissions: []string{"admin_api:" + entry.ID},
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
	if _, err := catalogByID(id); err != nil {
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
	// Only official catalog modules can be enabled (orphans are uninstall-only).
	if _, err := catalogByID(id); err != nil {
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
	return rec, nil
}

func (s *Service) Disable(id string) (*store.PluginRecord, error) {
	if _, err := catalogByID(id); err != nil {
		return nil, err
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
	return rec, nil
}

func (s *Service) Uninstall(id string) error {
	if err := validatePluginID(id); err != nil {
		return err
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

func (s *Service) requireInstalled(id string) (*store.PluginRecord, error) {
	if _, err := catalogByID(id); err != nil {
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

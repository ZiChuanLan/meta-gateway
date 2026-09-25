package plugins

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/lan/meta-gateway/internal/store"
)

// Plugin model discovery (host side).
//
// A hook that declares ModelsPath points at a plugin-relative GET endpoint
// that reports the model names the plugin currently answers for. The gateway
// asks that endpoint — never any plugin-specific config key — so a plugin can
// rename its virtual models from its own configuration and the gateway simply
// discovers the new answer. This file owns the asking: when it happens, what
// is accepted, and how failure degrades.
//
// Failure is deliberately invisible: an unreachable endpoint, a malformed
// body, or an unusable list keeps the previously reported names (or the
// manifest's declared match_models) in effect. Discovery can make a rename
// slower; it can never widen what a hook intercepts or empty it.

const (
	// maxModelDiscoveryBytes bounds one discovery answer.
	maxModelDiscoveryBytes = 64 << 10
	// maxDiscoveredModels bounds how many names one plugin may publish, so a
	// runaway endpoint cannot flood /v1/models.
	maxDiscoveredModels = 256
	// modelDiscoveryInterval is how often enabled plugins that declare a
	// models endpoint are re-asked in the background. Renames applied through
	// the console do not wait for this — saving config or enabling asks
	// immediately — the timer only covers changes the plugin makes on its own
	// (or a plugin restarted alongside its gateway).
	modelDiscoveryInterval = 30 * time.Second
	// modelDiscoveryTimeout bounds one discovery GET.
	modelDiscoveryTimeout = 5 * time.Second
)

// discoveredModels is one accepted discovery answer.
type discoveredModels struct {
	// Models are the validated, deduplicated names in stable order.
	Models []string
}

// parseDiscoveredModels validates a discovery answer. Accepted shapes: the
// documented {"models":[…]} and the OpenAI-flavoured {"data":[{"id":…}]} —
// plugins are written in any language and reusing a familiar container costs
// the gateway nothing. A wildcard name is rejected here for the same reason
// the /v1/models catalogue skips them: it is a matcher, not a callable model.
func parseDiscoveredModels(body []byte) (*discoveredModels, error) {
	var payload struct {
		Models []string          `json:"models"`
		Data   []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("models_discovery_invalid_json")
	}
	names := make([]string, 0, len(payload.Models)+len(payload.Data))
	names = append(names, payload.Models...)
	for _, raw := range payload.Data {
		var item struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, fmt.Errorf("models_discovery_invalid_entry")
		}
		if item.ID != "" {
			names = append(names, item.ID)
		} else if item.Name != "" {
			names = append(names, item.Name)
		}
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("models_discovery_empty")
	}
	if len(names) > maxDiscoveredModels {
		return nil, fmt.Errorf("models_discovery_too_many")
	}
	seen := make(map[string]struct{}, len(names))
	out := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if !validMatchPattern(name) || strings.ContainsAny(name, "*?") {
			return nil, fmt.Errorf("models_discovery_invalid_name")
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	sort.Strings(out)
	return &discoveredModels{Models: out}, nil
}

// fetchDiscoveredModels asks one plugin for its current model names. It is a
// plain GET against {sidecar URL}{ModelsPath} with the same credentials the
// hook calls use.
func (s *Service) fetchDiscoveredModels(pluginID string, spec *SidecarSpec, modelsPath string) (*discoveredModels, error) {
	base := strings.TrimRight(strings.TrimSpace(spec.URL), "/")
	if base == "" {
		return nil, fmt.Errorf("models_discovery_no_url")
	}
	ctx, cancel := context.WithTimeout(context.Background(), modelDiscoveryTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+modelsPath, nil)
	if err != nil {
		return nil, fmt.Errorf("models_discovery_request: %w", err)
	}
	if key := strings.TrimSpace(spec.APIKey); key != "" {
		req.Header.Set("X-Plugin-Key", key)
	}
	if config := s.PluginConfig(pluginID); config != "" {
		req.Header.Set(XPluginConfigHeader, base64Config(config))
	}
	resp, err := s.sidecarClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("models_discovery_call: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("models_discovery_status_%d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxModelDiscoveryBytes+1))
	if err != nil {
		return nil, fmt.Errorf("models_discovery_read: %w", err)
	}
	if len(body) > maxModelDiscoveryBytes {
		return nil, fmt.Errorf("models_discovery_too_large")
	}
	return parseDiscoveredModels(body)
}

// refreshPluginModels asks one enabled plugin for its model names and, when
// the answer is new, rebuilds the hook entries so the hot path, /v1/models and
// the console all see it. Returns whether the stored list changed.
func (s *Service) refreshPluginModels(pluginID string) bool {
	decl := s.routeModelsDecl(pluginID)
	if decl == nil {
		return false
	}
	spec, err := s.SidecarFor(pluginID)
	if err != nil || spec == nil {
		return false
	}
	answer, err := s.fetchDiscoveredModels(pluginID, spec, decl.ModelsPath)
	if err != nil {
		// Fail-open: keep serving the last known list and say why, once per
		// failed refresh rather than per request.
		log.Printf("plugins: %s model discovery failed: %v — keeping the last known list", pluginID, err)
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	previous, had := s.dynamicModels[pluginID]
	if had && sameStrings(previous, answer.Models) {
		return false
	}
	s.dynamicModels[pluginID] = answer.Models
	records, err := s.store.List()
	if err != nil {
		log.Printf("plugins: %s model discovery could not reload entries: %v", pluginID, err)
		return false
	}
	s.rebuildHookEntriesLocked(records, s.enabled)
	return true
}

// routeModelsDecl returns the route hook declaration a plugin made, or nil.
func (s *Service) routeModelsDecl(pluginID string) *HookDeclaration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, record := range s.enabledRecordsLocked() {
		if record.ID != pluginID {
			continue
		}
		var manifest Manifest
		if err := json.Unmarshal([]byte(record.MetaJSON), &manifest); err != nil || manifest.Hooks == nil {
			return nil
		}
		if manifest.Hooks.Route == nil || strings.TrimSpace(manifest.Hooks.Route.ModelsPath) == "" {
			return nil
		}
		return manifest.Hooks.Route
	}
	return nil
}

// modelsPathPluginIDs lists enabled plugin ids whose route hook declares a
// models endpoint (single-plugin variant of modelsPathPluginIDs).
func (s *Service) modelsPathPluginIDsHas(id string) bool {
	for _, known := range s.modelsPathPluginIDs() {
		if known == id {
			return true
		}
	}
	return false
}

// refreshAllPluginModels asks every enabled plugin that declares a models
// endpoint. It is the body of the background refresh loop; each plugin is
// bounded by its own timeout, so one dead sidecar cannot stall the others.
func (s *Service) refreshAllPluginModels() {
	for _, id := range s.modelsPathPluginIDs() {
		s.refreshPluginModels(id)
	}
}

// modelsPathPluginIDs lists enabled plugin ids whose route hook declares a
// models endpoint.
func (s *Service) modelsPathPluginIDs() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]string, 0, 4)
	for _, record := range s.enabledRecordsLocked() {
		var manifest Manifest
		if err := json.Unmarshal([]byte(record.MetaJSON), &manifest); err != nil || manifest.Hooks == nil || manifest.Hooks.Route == nil {
			continue
		}
		if strings.TrimSpace(manifest.Hooks.Route.ModelsPath) != "" {
			ids = append(ids, record.ID)
		}
	}
	sort.Strings(ids)
	return ids
}

// enabledRecordsLocked returns installed+enabled records. Callers hold s.mu.
func (s *Service) enabledRecordsLocked() []store.PluginRecord {
	records, err := s.store.List()
	if err != nil {
		return nil
	}
	out := make([]store.PluginRecord, 0, len(records))
	for _, record := range records {
		if s.enabled[record.ID] && record.Status == StatusInstalled {
			out = append(out, record)
		}
	}
	return out
}

// sameStrings compares two string slices by value.
func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// base64Config encodes a stored config JSON the way the hook call and the
// console proxy both carry it.
func base64Config(raw string) string {
	return base64.StdEncoding.EncodeToString([]byte(raw))
}

// StartModelDiscovery runs the background refresh loop until ctx is cancelled.
// It asks once immediately (covering plugins changed while the gateway was
// down), then on the interval. Callers register the returned stop with their
// shutdown machinery.
func (s *Service) StartModelDiscovery(ctx context.Context) func() {
	s.refreshAllPluginModels()
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(modelDiscoveryInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.refreshAllPluginModels()
			}
		}
	}()
	return func() { <-done }
}

package plugins

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lan/meta-gateway/internal/proxy"
	"github.com/lan/meta-gateway/internal/store"
)

// Plugin intercept hooks (host side).
//
// A sidecar plugin declares intercept points in its manifest; the gateway
// offers each point to every interested plugin in priority order and applies
// the first decision it receives. internal/proxy owns the contract and calls
// into this file through the Interceptor interface; this file owns
// declarations, model matching, the HTTP call, and the circuit breaker.
//
// Three invariants, mirrored from the proxy side:
//
//  1. Fail-open. Timeout, transport error, non-200, malformed JSON — every one
//     of them means "no opinion" and the request continues untouched. A broken
//     plugin can make the gateway slower, never wronger.
//  2. Unmatched models never reach a plugin. Wants is a pure in-memory match;
//     Decide is only called when Wants said yes.
//  3. A plugin never intercepts its own nested call. The origin marker travels
//     with the request and is skipped here, matching CLIProxyAPI's
//     HasModelRoutersExcept semantics.
const (
	// Hook point names. These mirror proxy.HookPoint so a manifest key, the
	// wire value, and the proxy's own constant stay one spelling.
	HookPointRoute    = "route"
	HookPointRequest  = "request"
	HookPointResponse = "response"

	// InterceptPermission is the manifest permission a plugin must declare
	// before any of its hooks are loaded. Interception exposes the operator's
	// prompts and answers to the plugin process, so it is opted into
	// explicitly rather than granted by the mere presence of a hooks block.
	InterceptPermission = "relay:intercept"

	// defaultHookTimeoutMs bounds a hook call that declares no timeout.
	defaultHookTimeoutMs = 800
	// minHookTimeoutMs / maxHookTimeoutMs bound what a declaration may ask for.
	// A multi-second hook is indistinguishable from an outage on the hot path.
	minHookTimeoutMs = 50
	maxHookTimeoutMs = 10000
	// maxHookResponseBytes bounds a hook answer body.
	maxHookResponseBytes = 4 << 20
	// hookFailureThreshold is how many consecutive failures trip the breaker.
	hookFailureThreshold = 5
	// hookBreakerCooldown is how long a tripped hook stays open before a single
	// request is allowed through to test it.
	hookBreakerCooldown = 30 * time.Second
	// maxHookDepth stops a chain that keeps calling back into the gateway with
	// the origin marker rewritten. The first hop is depth 1.
	maxHookDepth = 3
)

// XHookOriginHeader carries the id of the plugin whose hook call produced a
// request. A plugin that calls the gateway back (to reach a model the gateway
// routes) forwards this header so the gateway can skip that plugin's own hooks
// for the nested request.
const XHookOriginHeader = "X-Meta-Hook-Origin"

// XHookDepthHeader counts hook-originated hops, so a plugin cannot recurse
// without bound even if it drops the origin header.
const XHookDepthHeader = "X-Meta-Hook-Depth"

// HookDeclaration is one intercept point a plugin declared in its manifest.
type HookDeclaration struct {
	// Path is the plugin-relative endpoint the gateway POSTs the hook input to.
	Path string `json:"path"`
	// MatchModels narrows the point to matching model names: '*' matches any
	// run of runes, '?' exactly one, and a name without wildcards matches
	// exactly (the same matcher route patterns use). For a hook that declares
	// ModelsPath this list is optional — it is the fallback used while the
	// plugin cannot be asked; otherwise an empty list matches nothing and a
	// plugin that names no model never intercepts traffic.
	MatchModels []string `json:"match_models,omitempty"`
	// ModelsPath is a plugin-relative GET endpoint that reports the model
	// names this hook currently answers for ({"models":[…]}). It is the
	// generic discovery half of the plugin protocol: a plugin whose model
	// names follow its own config declares this instead of freezing names in
	// the manifest, and the gateway asks it at registration, on config saves,
	// on enable, for managed starts, and on a slow refresh timer. The gateway
	// is still what matches and advertises the names — the plugin never
	// receives traffic for a model it did not report, and the declared
	// MatchModels stay the fallback while the endpoint is unreachable.
	ModelsPath string `json:"models_path,omitempty"`
	// TimeoutMs bounds one call (default 800; clamped to 50..10000).
	TimeoutMs int `json:"timeout_ms,omitempty"`
	// Priority orders plugins inside a point (higher first). Equal priorities
	// keep declaration order, so the outcome never depends on map iteration.
	Priority int `json:"priority,omitempty"`
}

// HookSet is the manifest's "hooks" object, keyed by point.
type HookSet struct {
	Route    *HookDeclaration `json:"route,omitempty"`
	Request  *HookDeclaration `json:"request,omitempty"`
	Response *HookDeclaration `json:"response,omitempty"`
}

// declarations returns the declared points in a stable order, skipping unset
// ones.
func (h *HookSet) declarations() []struct {
	Point string
	Decl  *HookDeclaration
} {
	if h == nil {
		return nil
	}
	all := []struct {
		Point string
		Decl  *HookDeclaration
	}{
		{HookPointRoute, h.Route},
		{HookPointRequest, h.Request},
		{HookPointResponse, h.Response},
	}
	out := make([]struct {
		Point string
		Decl  *HookDeclaration
	}, 0, len(all))
	for _, entry := range all {
		if entry.Decl != nil {
			out = append(out, entry)
		}
	}
	return out
}

// Empty reports whether the plugin declared no hooks at all.
func (h *HookSet) Empty() bool {
	if h == nil {
		return true
	}
	return h.Route == nil && h.Request == nil && h.Response == nil
}

// validateHookDeclaration rejects a declaration that could not be called
// safely: an unservable path, an unbounded timeout, or a match list that is
// empty or carries control characters.
func validateHookDeclaration(point string, decl *HookDeclaration) error {
	if decl == nil {
		return nil
	}
	path := strings.TrimSpace(decl.Path)
	if path == "" {
		return fmt.Errorf("hook_%s_path_required", point)
	}
	if !strings.HasPrefix(path, "/") {
		return fmt.Errorf("hook_%s_path_invalid", point)
	}
	if err := validateSidecarPath(path); err != nil {
		return fmt.Errorf("hook_%s_path_invalid", point)
	}
	if decl.TimeoutMs != 0 && (decl.TimeoutMs < minHookTimeoutMs || decl.TimeoutMs > maxHookTimeoutMs) {
		return fmt.Errorf("hook_%s_timeout_invalid", point)
	}
	if len(decl.MatchModels) == 0 && strings.TrimSpace(decl.ModelsPath) == "" {
		// Nothing to match means nothing to intercept. Rejecting this is
		// deliberate: an omitted match list must not become "every model".
		// A plugin that publishes its names over ModelsPath is exempt — the
		// reported list takes over before any traffic is served.
		return fmt.Errorf("hook_%s_match_models_required", point)
	}
	for _, pattern := range decl.MatchModels {
		if !validMatchPattern(pattern) {
			return fmt.Errorf("hook_%s_match_models_invalid", point)
		}
	}
	if path := strings.TrimSpace(decl.ModelsPath); path != "" {
		if !strings.HasPrefix(path, "/") || validateSidecarPath(path) != nil {
			return fmt.Errorf("hook_%s_models_path_invalid", point)
		}
	}
	return nil
}

// validMatchPattern reports whether one matcher is safe to put in the hot
// path: non-empty, bounded, and free of the whitespace that would make a
// "model name" several of them.
func validMatchPattern(pattern string) bool {
	trimmed := strings.TrimSpace(pattern)
	if trimmed == "" || len([]byte(trimmed)) > 256 {
		return false
	}
	return !strings.ContainsAny(trimmed, " \t\r\n")
}

// ValidateHookSet validates every declared point on a manifest. Exported so the
// registration path can reject a broken declaration before it is persisted.
func ValidateHookSet(hooks *HookSet) error {
	for _, entry := range hooks.declarations() {
		if err := validateHookDeclaration(entry.Point, entry.Decl); err != nil {
			return err
		}
	}
	return nil
}

// hookEntry is one enabled plugin's declaration for one point, frozen at the
// last enablement reload so the hot path never parses a manifest.
type hookEntry struct {
	pluginID string
	name     string
	spec     *SidecarSpec
	point    string
	decl     HookDeclaration
	order    int
}

// hookTimeout is the effective per-call budget for this declaration.
func (e hookEntry) hookTimeout() time.Duration {
	ms := e.decl.TimeoutMs
	if ms <= 0 {
		ms = defaultHookTimeoutMs
	}
	return time.Duration(ms) * time.Millisecond
}

// hookBreakerState trips one (plugin, point) pair after repeated failures.
type hookBreaker struct {
	mu     sync.Mutex
	fails  map[string]int
	openAt map[string]time.Time
}

// allow reports whether a call may proceed. A tripped hook lets exactly one
// trial through once its cooldown has elapsed.
func (b *hookBreaker) allow(key string, now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	opened, tripped := b.openAt[key]
	if !tripped {
		return true
	}
	return !now.Before(opened.Add(hookBreakerCooldown))
}

// fail records one failure and reports whether the breaker just tripped.
func (b *hookBreaker) fail(key string, now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.fails == nil {
		b.fails = make(map[string]int)
	}
	if b.openAt == nil {
		b.openAt = make(map[string]time.Time)
	}
	if _, open := b.openAt[key]; open {
		// The half-open trial failed: stay open for another cooldown.
		b.openAt[key] = now
		b.fails[key] = 0
		return false
	}
	b.fails[key]++
	if b.fails[key] >= hookFailureThreshold {
		b.fails[key] = 0
		b.openAt[key] = now
		return true
	}
	return false
}

// succeed clears a hook's failure history.
func (b *hookBreaker) succeed(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.fails, key)
	delete(b.openAt, key)
}

// tripped reports whether the hook is currently open (for diagnostics).
func (b *hookBreaker) tripped(key string, now time.Time) bool {
	return !b.allow(key, now)
}

// hookOrigin is the recursion marker carried on a nested request.
type HookOrigin struct {
	// PluginID is the plugin whose hook call produced this request ("" when the
	// request did not originate from a hook call).
	PluginID string
	// Depth counts hook-originated hops.
	Depth int
}

type hookOriginKey struct{}

// WithHookOrigin marks ctx as carrying a hook origin.
func WithHookOrigin(ctx context.Context, origin HookOrigin) context.Context {
	return context.WithValue(ctx, hookOriginKey{}, origin)
}

// HookOriginFrom reads the recursion marker off a request context.
func HookOriginFrom(ctx context.Context) HookOrigin {
	if ctx == nil {
		return HookOrigin{}
	}
	if origin, ok := ctx.Value(hookOriginKey{}).(HookOrigin); ok {
		return origin
	}
	return HookOrigin{}
}

// HookOriginFromHeaders reconstructs the recursion marker from inbound request
// headers, so a plugin that forwards them keeps its own hooks out of the
// nested call.
func HookOriginFromHeaders(header http.Header) HookOrigin {
	if header == nil {
		return HookOrigin{}
	}
	origin := HookOrigin{PluginID: strings.TrimSpace(header.Get(XHookOriginHeader))}
	if origin.PluginID != "" {
		// Only a known plugin id may suppress a hook; a client cannot invent an
		// id that disables nothing, and a stale id simply matches no entry.
		if validatePluginID(origin.PluginID) != nil {
			origin.PluginID = ""
		}
	}
	if raw := strings.TrimSpace(header.Get(XHookDepthHeader)); raw != "" {
		if depth, err := strconv.Atoi(raw); err == nil && depth > 0 {
			origin.Depth = depth
		}
	}
	if origin.PluginID != "" && origin.Depth == 0 {
		origin.Depth = 1
	}
	return origin
}

// rebuildHookEntriesLocked recomputes the enabled hook declarations. Callers
// hold s.mu for writing.
func (s *Service) rebuildHookEntriesLocked(records []store.PluginRecord, enabled map[string]bool) {
	entries := make(map[string][]hookEntry, 3)
	order := 0
	for _, record := range records {
		if !enabled[record.ID] || record.Status != StatusInstalled || record.MetaJSON == "" {
			continue
		}
		var manifest Manifest
		if err := json.Unmarshal([]byte(record.MetaJSON), &manifest); err != nil {
			continue
		}
		if manifest.Hooks.Empty() || manifest.Sidecar == nil {
			continue
		}
		if !hasInterceptPermission(manifest.Permissions) {
			// Declared hooks without the permission: keep them out of the hot
			// path and say so, rather than silently intercepting.
			log.Printf("plugins: %s declares hooks but not the %q permission — hooks disabled", record.ID, InterceptPermission)
			continue
		}
		if err := ValidateHookSet(manifest.Hooks); err != nil {
			log.Printf("plugins: %s hook declaration rejected: %v", record.ID, err)
			continue
		}
		for _, entry := range manifest.Hooks.declarations() {
			spec := *manifest.Sidecar
			decl := *entry.Decl
			// A plugin that publishes its own model names (ModelsPath) is matched
			// against the last reported list instead of the manifest's frozen
			// one — this is the single place the hot path, /v1/models and the
			// console's hook list all read from.
			if reported := s.dynamicModels[record.ID]; reported != nil && entry.Point == HookPointRoute && decl.ModelsPath != "" {
				decl.MatchModels = reported
			}
			entries[entry.Point] = append(entries[entry.Point], hookEntry{
				pluginID: record.ID,
				name:     manifest.Name,
				spec:     &spec,
				point:    entry.Point,
				decl:     decl,
				order:    order,
			})
			order++
		}
	}
	for point, list := range entries {
		// Higher priority first; equal priorities keep declaration order, so the
		// chain is deterministic across restarts and map iteration orders.
		sort.SliceStable(list, func(i, j int) bool {
			if list[i].decl.Priority != list[j].decl.Priority {
				return list[i].decl.Priority > list[j].decl.Priority
			}
			return list[i].order < list[j].order
		})
		entries[point] = list
	}
	s.hookEntries = entries
}

// refreshHookEntries recomputes the enabled hook declarations from the
// persisted records and the current config cache. It is the cheap reload for a
// change that can reach the entries themselves (a saved config), not for one
// that changes which plugins are enabled — that path goes through
// reloadEnabled.
func (s *Service) refreshHookEntries() {
	records, err := s.store.List()
	if err != nil {
		log.Printf("plugins: hook refresh skipped: %v", err)
		return
	}
	s.mu.Lock()
	s.rebuildHookEntriesLocked(records, s.enabled)
	s.mu.Unlock()
}

// hasInterceptPermission reports whether a manifest declared the intercept
// permission.
func hasInterceptPermission(permissions []string) bool {
	for _, permission := range permissions {
		if strings.EqualFold(strings.TrimSpace(permission), InterceptPermission) {
			return true
		}
	}
	return false
}

// HookDeclarations returns the hook set a plugin's manifest declares (nil when
// it declares none). Used by the admin surface and by plugin status views.
func (s *Service) HookDeclarations(id string) *HookSet {
	manifest, err := s.readManifest(id)
	if err != nil || manifest == nil {
		return nil
	}
	return manifest.Hooks
}

// HookPointsFor reports which points an enabled plugin currently serves.
func (s *Service) HookPointsFor(id string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var points []string
	for _, point := range []string{HookPointRoute, HookPointRequest, HookPointResponse} {
		for _, entry := range s.hookEntries[point] {
			if entry.pluginID == id {
				points = append(points, point)
				break
			}
		}
	}
	return points
}

// HookModelPatterns returns the model patterns an enabled plugin's hook
// declares, so the console can tell the operator which models a plugin sees.
func (s *Service) HookModelPatterns(id string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	seen := make(map[string]struct{})
	var patterns []string
	for _, entries := range s.hookEntries {
		for _, entry := range entries {
			if entry.pluginID != id {
				continue
			}
			for _, pattern := range entry.decl.MatchModels {
				if _, ok := seen[pattern]; ok {
					continue
				}
				seen[pattern] = struct{}{}
				patterns = append(patterns, pattern)
			}
		}
	}
	sort.Strings(patterns)
	return patterns
}

// Wants implements proxy.Interceptor. It is a pure in-memory match so an
// unmatched model never touches the plugin path, the DB, or the network.
func (s *Service) Wants(point proxy.HookPoint, model string) bool {
	entries := s.hookEntriesFor(string(point))
	if len(entries) == 0 {
		return false
	}
	for _, entry := range entries {
		if hookMatchesModel(entry.decl.MatchModels, model) {
			return true
		}
	}
	return false
}

// hookEntriesFor snapshots the declarations for one point.
func (s *Service) hookEntriesFor(point string) []hookEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	stored := s.hookEntries[point]
	if len(stored) == 0 {
		return nil
	}
	out := make([]hookEntry, len(stored))
	copy(out, stored)
	return out
}

// hookMatchesModel applies the shared route-pattern matcher to a declaration's
// match list.
func hookMatchesModel(patterns []string, model string) bool {
	for _, pattern := range patterns {
		if store.MatchModelPattern(pattern, model) {
			return true
		}
	}
	return false
}

// Decide implements proxy.Interceptor: it offers the point to every interested
// plugin in priority order and returns the first decision.
//
// Every failure path returns nil or a result with Handled=false, which the
// proxy treats as "no opinion".
func (s *Service) Decide(ctx context.Context, req proxy.HookInput) *proxy.HookResult {
	point := string(req.Point)
	entries := s.hookEntriesFor(point)
	if len(entries) == 0 {
		return nil
	}
	origin := HookOriginFrom(ctx)
	if origin.Depth >= maxHookDepth {
		// A chain that keeps calling back without forwarding the origin marker
		// still has to stop somewhere.
		log.Printf("plugins: hook depth %d reached at %s — skipping plugin hooks for this request", origin.Depth, point)
		return nil
	}
	now := time.Now()
	for _, entry := range entries {
		if origin.PluginID != "" && origin.PluginID == entry.pluginID {
			// The plugin's own nested call: never let a hook intercept the
			// request it is making. Other plugins still get their turn.
			continue
		}
		if !hookMatchesModel(entry.decl.MatchModels, req.Model) {
			continue
		}
		breakerKey := entry.pluginID + "/" + point
		if !s.hookHealth.allow(breakerKey, now) {
			continue
		}
		result := s.callHook(ctx, entry, req, origin)
		if result == nil {
			continue
		}
		if result.Err != nil {
			if tripped := s.hookHealth.fail(breakerKey, time.Now()); tripped {
				log.Printf("plugins: hook %s disabled for %s after %d consecutive failures (last: %v)",
					entry.pluginID, point, hookFailureThreshold, result.Err)
			}
			continue
		}
		s.hookHealth.succeed(breakerKey)
		if !result.Handled {
			// Declined: a lower-priority plugin may still want the point.
			continue
		}
		return result
	}
	return nil
}

// callHook performs one hook call and decodes the answer. Every failure mode
// is folded into a non-nil result carrying Err, so the caller can count it
// against the breaker while still failing open.
func (s *Service) callHook(ctx context.Context, entry hookEntry, input proxy.HookInput, origin HookOrigin) *proxy.HookResult {
	payload, err := json.Marshal(input)
	if err != nil {
		return &proxy.HookResult{PluginID: entry.pluginID, Err: fmt.Errorf("marshal hook input: %w", err)}
	}
	base := strings.TrimRight(strings.TrimSpace(entry.spec.URL), "/")
	if base == "" {
		return &proxy.HookResult{PluginID: entry.pluginID, Err: fmt.Errorf("plugin has no sidecar url")}
	}
	hookCtx, cancel := context.WithTimeout(ctx, entry.hookTimeout())
	defer cancel()
	httpReq, err := http.NewRequestWithContext(hookCtx, http.MethodPost, base+entry.decl.Path, bytes.NewReader(payload))
	if err != nil {
		return &proxy.HookResult{PluginID: entry.pluginID, Err: fmt.Errorf("hook request: %w", err)}
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if key := strings.TrimSpace(entry.spec.APIKey); key != "" {
		httpReq.Header.Set("X-Plugin-Key", key)
	}
	if config := s.PluginConfig(entry.pluginID); config != "" {
		httpReq.Header.Set(XPluginConfigHeader, base64.StdEncoding.EncodeToString([]byte(config)))
	}
	// The origin marker is handed to the plugin so it can forward it on a
	// nested gateway call and keep its own hook out of that request.
	httpReq.Header.Set(XHookOriginHeader, entry.pluginID)
	httpReq.Header.Set(XHookDepthHeader, strconv.Itoa(origin.Depth+1))

	start := time.Now()
	resp, err := s.sidecarClient.Do(httpReq)
	latencyMs := int(time.Since(start).Milliseconds())
	if err != nil {
		return &proxy.HookResult{PluginID: entry.pluginID, LatencyMs: latencyMs, Err: fmt.Errorf("hook call: %w", err)}
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxHookResponseBytes+1))
	if readErr != nil {
		return &proxy.HookResult{PluginID: entry.pluginID, LatencyMs: latencyMs, Err: fmt.Errorf("hook read: %w", readErr)}
	}
	if len(body) > maxHookResponseBytes {
		return &proxy.HookResult{PluginID: entry.pluginID, LatencyMs: latencyMs, Err: fmt.Errorf("hook answer too large")}
	}
	if resp.StatusCode != http.StatusOK {
		return &proxy.HookResult{PluginID: entry.pluginID, LatencyMs: latencyMs, Err: fmt.Errorf("hook status %d", resp.StatusCode)}
	}
	var result proxy.HookResult
	if err := json.Unmarshal(body, &result); err != nil {
		return &proxy.HookResult{PluginID: entry.pluginID, LatencyMs: latencyMs, Err: fmt.Errorf("hook decode: %w", err)}
	}
	result.PluginID = entry.pluginID
	result.LatencyMs = latencyMs
	return &result
}

// HookStatus is the admin-facing view of one enabled hook declaration.
type HookStatus struct {
	PluginID    string   `json:"plugin_id"`
	PluginName  string   `json:"plugin_name,omitempty"`
	Point       string   `json:"point"`
	Path        string   `json:"path"`
	MatchModels []string `json:"match_models"`
	TimeoutMs   int      `json:"timeout_ms"`
	Priority    int      `json:"priority"`
	// Tripped reports an open circuit breaker (the hook is being skipped).
	Tripped bool `json:"tripped,omitempty"`
}

// HookStatuses lists every loaded hook declaration, for the console.
func (s *Service) HookStatuses() []HookStatus {
	s.mu.RLock()
	points := make([]string, 0, len(s.hookEntries))
	for point := range s.hookEntries {
		points = append(points, point)
	}
	sort.Strings(points)
	out := make([]HookStatus, 0, 4)
	for _, point := range points {
		for _, entry := range s.hookEntries[point] {
			timeoutMs := entry.decl.TimeoutMs
			if timeoutMs <= 0 {
				timeoutMs = defaultHookTimeoutMs
			}
			out = append(out, HookStatus{
				PluginID:    entry.pluginID,
				PluginName:  entry.name,
				Point:       entry.point,
				Path:        entry.decl.Path,
				MatchModels: append([]string(nil), entry.decl.MatchModels...),
				TimeoutMs:   timeoutMs,
				Priority:    entry.decl.Priority,
			})
		}
	}
	s.mu.RUnlock()
	now := time.Now()
	for i := range out {
		out[i].Tripped = s.hookHealth.tripped(out[i].PluginID+"/"+out[i].Point, now)
	}
	return out
}

// VirtualModels returns the literal model names an enabled route hook answers
// for. Such a name has no route of its own — the hook rewrites the request
// before selection — but /v1/models must still list it, because a client can
// only send a name it can discover. Wildcard patterns are skipped: "*" is a
// matcher, not a callable model.
//
// Only route hooks contribute. Request and response hooks rewrite traffic that
// is already routed, so they do not make a model reachable.
func (s *Service) VirtualModels() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	seen := make(map[string]struct{})
	out := make([]string, 0, 4)
	for _, entry := range s.hookEntries[HookPointRoute] {
		for _, pattern := range entry.decl.MatchModels {
			name := strings.TrimSpace(pattern)
			if name == "" || strings.ContainsAny(name, "*?") {
				continue
			}
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

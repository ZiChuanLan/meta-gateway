package plugins

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// manifestFor builds a sidecar serving a manifest with the given hook JSON.
func manifestFor(t *testing.T, hookJSON string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/plugin.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":"router-plugin","version":"1.0.0","name":"Router Plugin","description":"test plugin","page_path":"/app","health_path":"healthz","permissions":["relay:intercept"],"hooks":{"route":%s}}`, hookJSON)
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/app", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "plugin-page")
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newServiceFor(t *testing.T) *Service {
	t.Helper()
	db := openPluginTestDB(t)
	svc, err := NewService(filepath.Join(t.TempDir(), "plugins"), db.Plugin)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}

func routeModelsOf(t *testing.T, svc *Service) []string {
	t.Helper()
	svc.mu.RLock()
	defer svc.mu.RUnlock()
	entries := append([]hookEntry(nil), svc.hookEntries[HookPointRoute]...)
	if len(entries) != 1 {
		t.Fatalf("route hook entries = %d, want 1", len(entries))
	}
	return entries[0].decl.MatchModels
}

// A hook that declares models_path has its matchers replaced by the plugin's
// reported list: the plugin owns the names, the gateway discovers them —
// registration, enable and a later refresh all converge on the reported list,
// and the declared match_models only stand in while the endpoint is silent.
func TestHookModelsPathDiscovery(t *testing.T) {
	var modelsPayload atomic.Value // string
	modelsPayload.Store(`{"models":["auto-jev"]}`)
	modelsCalled := atomic.Int64{}
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/plugin.json":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":"router-plugin","version":"1.0.0","name":"Router Plugin","page_path":"/app","health_path":"healthz","permissions":["relay:intercept"],"config_fields":[{"key":"model_name","type":"string","default":"auto-jev"}],"hooks":{"route":{"path":"/hooks/route","match_models":["auto-jev"],"models_path":"/models"}}}`)
		case "/healthz":
			w.WriteHeader(http.StatusOK)
		case "/models":
			modelsCalled.Add(1)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, modelsPayload.Load().(string))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(remote.Close)

	svc := newServiceFor(t)
	if _, err := svc.RegisterSidecar(remote.URL, "", nil); err != nil {
		t.Fatalf("RegisterSidecar: %v", err)
	}

	// Registration already asked the plugin and adopted its answer.
	if got := routeModelsOf(t, svc); len(got) != 1 || got[0] != "auto-jev" {
		t.Fatalf("after register: match models = %v, want [auto-jev]", got)
	}
	if got := svc.VirtualModels(); len(got) != 1 || got[0] != "auto-jev" {
		t.Fatalf("VirtualModels = %v, want [auto-jev]", got)
	}

	// The plugin learned a new name from its own config: saving the config
	// re-asks the plugin, so the gateway adopts the new name with no
	// re-register. The credentials the hook calls use (X-Plugin-Config) are
	// what the discovery call carries, so the plugin computes the answer
	// statelessly.
	modelsPayload.Store(`{"models":["auto-house"]}`)
	if err := svc.SetPluginConfig("router-plugin", `{"model_name":"auto-house"}`); err != nil {
		t.Fatalf("SetPluginConfig: %v", err)
	}
	if got := svc.VirtualModels(); len(got) != 1 || got[0] != "auto-house" {
		t.Fatalf("VirtualModels = %v, want [auto-house]", got)
	}
	statuses := svc.HookStatuses()
	if len(statuses) != 1 || len(statuses[0].MatchModels) != 1 || statuses[0].MatchModels[0] != "auto-house" {
		t.Fatalf("HookStatuses = %+v, want the discovered name", statuses)
	}

	// A config save that does not change the reported answer must not rebuild
	// the entries (the answer is compared by value).
	svc.SetPluginConfig("router-plugin", `{"model_name":"auto-house"}`)
	if got := routeModelsOf(t, svc); len(got) != 1 || got[0] != "auto-house" {
		t.Fatalf("after idempotent save: match models = %v", got)
	}

	// Wildcards are a matcher, not a callable model: discovery rejects the
	// answer and the previous list stays in effect.
	modelsPayload.Store(`{"models":["auto-*"]}`)
	if svc.refreshPluginModels("router-plugin") {
		t.Fatal("a wildcard-only answer must not be adopted")
	}
	if got := routeModelsOf(t, svc); len(got) != 1 || got[0] != "auto-house" {
		t.Fatalf("after wildcard answer: match models = %v, want [auto-house]", got)
	}

	// The OpenAI-shaped container is accepted too, so a plugin in any language
	// can reuse a familiar form.
	modelsPayload.Store(`{"data":[{"id":"auto-jev","name":"Jev"},{"id":"auto-house"}]}`)
	if !svc.refreshPluginModels("router-plugin") {
		t.Fatal("the data-shaped answer should be adopted")
	}
	if got := svc.VirtualModels(); len(got) != 2 || got[0] != "auto-house" || got[1] != "auto-jev" {
		t.Fatalf("VirtualModels = %v, want [auto-house auto-jev]", got)
	}

	// When the endpoint dies, the last known list keeps serving — fail-open.
	remote.Close()
	if svc.refreshPluginModels("router-plugin") {
		t.Fatal("a failed discovery must not report a change")
	}
	if got := routeModelsOf(t, svc); len(got) != 2 {
		t.Fatalf("after endpoint death: match models = %v, want the last known list", got)
	}
}

// A hook with models_path and no declared match_models is valid: the reported
// list is the match list, and the manifest's omission is not "match nothing".
func TestHookModelsPathAllowsEmptyDeclaredList(t *testing.T) {
	hooks := &HookSet{Route: &HookDeclaration{Path: "/hooks/route", ModelsPath: "/models"}}
	if err := ValidateHookSet(hooks); err != nil {
		t.Fatalf("models_path with no match_models rejected: %v", err)
	}
	// Without models_path the old rule still bites.
	if err := ValidateHookSet(&HookSet{Route: &HookDeclaration{Path: "/hooks/route"}}); err == nil {
		t.Fatal("an empty match list without models_path must be rejected")
	}
	// A bad path is still rejected.
	for _, path := range []string{"models", "/models?x=1", "/a/../b"} {
		hooks := &HookSet{Route: &HookDeclaration{Path: "/hooks/route", ModelsPath: path}}
		if err := ValidateHookSet(hooks); err == nil {
			t.Fatalf("models_path %q accepted", path)
		}
	}
}

// The gateway sends the stored config to the models endpoint the same way it
// sends it to hook calls, so a plugin can derive its answer from config alone.
func TestDiscoveryCarriesPluginConfig(t *testing.T) {
	var seenConfig atomic.Value
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/plugin.json":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":"router-plugin","version":"1.0.0","name":"Router Plugin","page_path":"/app","health_path":"healthz","permissions":["relay:intercept"],"hooks":{"route":{"path":"/hooks/route","models_path":"/models"}}}`)
		case "/healthz":
			w.WriteHeader(http.StatusOK)
		case "/models":
			seenConfig.Store(r.Header.Get("X-Plugin-Config"))
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"models":["auto-x"]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(remote.Close)

	svc := newServiceFor(t)
	if _, err := svc.RegisterSidecar(remote.URL, "", nil); err != nil {
		t.Fatalf("RegisterSidecar: %v", err)
	}
	if err := svc.SetPluginConfig("router-plugin", `{"model_name":"auto-x"}`); err != nil {
		t.Fatalf("SetPluginConfig: %v", err)
	}

	raw, _ := seenConfig.Load().(string)
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || string(decoded) != `{"model_name":"auto-x"}` {
		t.Fatalf("models endpoint saw config %q (%v), want the stored config", raw, err)
	}
}

// parseDiscoveredModels accepts the two documented shapes and rejects
// everything that would widen or corrupt the match list.
func TestParseDiscoveredModels(t *testing.T) {
	answer, err := parseDiscoveredModels([]byte(`{"models":["b","a","a"]}`))
	if err != nil {
		t.Fatalf("models shape rejected: %v", err)
	}
	if len(answer.Models) != 2 || answer.Models[0] != "a" || answer.Models[1] != "b" {
		t.Fatalf("models = %v, want deduplicated and sorted", answer.Models)
	}
	if _, err := parseDiscoveredModels([]byte(`{"data":[{"id":"a"},{"name":"b"}]}`)); err != nil {
		t.Fatalf("data shape rejected: %v", err)
	}
	for _, bad := range []string{
		`{"models":[]}`,
		`{"models":[""]}`,
		`{"models":["a b"]}`,
		`{"models":["auto-*"]}`,
		`{"models":42}`,
		`not json`,
		`{}`,
	} {
		if _, err := parseDiscoveredModels([]byte(bad)); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
}

// manifestWithModels is a small helper for asserting the persisted manifest
// keeps the models_path declaration through registration.
func TestManifestKeepsModelsPath(t *testing.T) {
	remote := manifestFor(t, `{"path":"/hooks/route","match_models":["auto-jev"],"models_path":"/models"}`)
	svc := newServiceFor(t)
	// No /models endpoint on this stub: discovery fails and the declared list
	// stays — exactly the fallback behaviour.
	if _, err := svc.RegisterSidecar(remote.URL, "", nil); err != nil {
		t.Fatalf("RegisterSidecar: %v", err)
	}
	decl := svc.routeModelsDecl("router-plugin")
	if decl == nil || decl.ModelsPath != "/models" {
		t.Fatalf("routeModelsDecl = %+v, want models_path /models", decl)
	}
	if got := routeModelsOf(t, svc); len(got) != 1 || got[0] != "auto-jev" {
		t.Fatalf("match models = %v, want the declared fallback", got)
	}
}

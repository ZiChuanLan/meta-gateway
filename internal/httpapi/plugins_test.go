package httpapi

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/lan/meta-gateway/internal/plugins"
	"github.com/lan/meta-gateway/internal/store"
)

func sidecarPluginHandler(t *testing.T) (http.Handler, *plugins.Service) {
	t.Helper()
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc, err := plugins.NewService(filepath.Join(t.TempDir(), "plugins"), db.Plugin)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	h := NewPluginHandler(svc)
	h.SetTokenVerifier(func(token string) bool { return token == "admin-test" })
	r := chi.NewRouter()
	admin := chi.NewRouter()
	h.Register(admin)
	h.RegisterPublic(r)
	r.Mount("/admin", admin)
	return r, svc
}

// fakeSidecarHandler is the plugin side used by the proxy test.
func fakeSidecarHandler(t *testing.T, id string, requireKey bool) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/plugin.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":%q,"version":"1.0.0","name":"Proxy Plugin","page_path":"/app","health_path":"healthz"}`, id)
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if requireKey && r.Header.Get("X-Plugin-Key") != "sekrit" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/app", func(w http.ResponseWriter, r *http.Request) {
		if requireKey && r.Header.Get("X-Plugin-Key") != "sekrit" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprintf(w, "page:%s", id)
	})
	mux.HandleFunc("/api/echo", func(w http.ResponseWriter, r *http.Request) {
		if requireKey && r.Header.Get("X-Plugin-Key") != "sekrit" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"plugin":%q,"key":%q,"config":%q}`, id, r.Header.Get("X-Plugin-Key"), r.Header.Get("X-Plugin-Config"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// TestProxySidecarInjectsConfig verifies the persisted plugin config reaches
// the sidecar as base64 X-Plugin-Config on proxied requests, and that an
// empty/absent config produces no header.
func TestProxySidecarInjectsConfig(t *testing.T) {
	handler, _ := sidecarPluginHandler(t)
	base := fakeSidecarHandler(t, "config-plugin", false)
	body := fmt.Sprintf(`{"url":%q}`, base.URL)
	register := httptest.NewRequest(http.MethodPost, "/admin/plugins/register", strings.NewReader(body))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, register)
	if rr.Code != http.StatusCreated {
		t.Fatalf("register = %d body=%s", rr.Code, rr.Body.String())
	}

	// No config set: header must be absent.
	req := httptest.NewRequest(http.MethodGet, "/admin/plugins/config-plugin/proxy/api/echo?t=admin-test", nil)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("proxy = %d body=%s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), `"config":"`) && !strings.Contains(rr.Body.String(), `"config":""`) {
		t.Fatalf("expected empty config header, got %s", rr.Body.String())
	}

	// Save a config and verify it is forwarded base64-encoded.
	configBody := `{"config":"{\"apiKey\":\"abc123\",\"region\":\"cn\"}"}`
	save := httptest.NewRequest(http.MethodPut, "/admin/plugins/config-plugin/config", strings.NewReader(configBody))
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, save)
	if rr.Code != http.StatusOK {
		t.Fatalf("save config = %d body=%s", rr.Code, rr.Body.String())
	}
	req = httptest.NewRequest(http.MethodGet, "/admin/plugins/config-plugin/proxy/api/echo?t=admin-test", nil)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("proxy = %d body=%s", rr.Code, rr.Body.String())
	}
	want := base64.StdEncoding.EncodeToString([]byte(`{"apiKey":"abc123","region":"cn"}`))
	if !strings.Contains(rr.Body.String(), `"config":"`+want+`"`) {
		t.Fatalf("config header = %s, want base64 %s", rr.Body.String(), want)
	}
}

func TestRegisterSidecarEndpoint(t *testing.T) {
	handler, _ := sidecarPluginHandler(t)
	base := fakeSidecarHandler(t, "proxy-plugin", false)

	body := fmt.Sprintf(`{"url":%q}`, base.URL)
	req := httptest.NewRequest(http.MethodPost, "/admin/plugins/register", strings.NewReader(body))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("register status = %d body=%s", rr.Code, rr.Body.String())
	}

	// Now enabled: proxy to the plugin's page path.
	req = httptest.NewRequest(http.MethodGet, "/admin/plugins/proxy-plugin/proxy/?t=admin-test", nil)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || rr.Body.String() != "page:proxy-plugin" {
		t.Fatalf("proxy page = %d %q", rr.Code, rr.Body.String())
	}

	// API path with X-Plugin-Key injected.
	req = httptest.NewRequest(http.MethodGet, "/admin/plugins/proxy-plugin/proxy/api/echo?t=admin-test", nil)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"plugin":"proxy-plugin"`) {
		t.Fatalf("proxy api = %d %s", rr.Code, rr.Body.String())
	}
}

func TestRegisterSidecarEndpointKeyInjected(t *testing.T) {
	handler, _ := sidecarPluginHandler(t)
	base := fakeSidecarHandler(t, "keyed-plugin", true)

	// Missing key → health check fails → 400.
	body := fmt.Sprintf(`{"url":%q}`, base.URL)
	req := httptest.NewRequest(http.MethodPost, "/admin/plugins/register", strings.NewReader(body))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("register without key = %d body=%s", rr.Code, rr.Body.String())
	}

	// With key → 201, and proxied requests carry X-Plugin-Key.
	body = fmt.Sprintf(`{"url":%q,"api_key":"sekrit"}`, base.URL)
	req = httptest.NewRequest(http.MethodPost, "/admin/plugins/register", strings.NewReader(body))
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("register with key = %d body=%s", rr.Code, rr.Body.String())
	}
	req = httptest.NewRequest(http.MethodGet, "/admin/plugins/keyed-plugin/proxy/api/echo?t=admin-test", nil)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"key":"sekrit"`) {
		t.Fatalf("proxy api = %d %s", rr.Code, rr.Body.String())
	}
}

func TestProxySidecarRejectsUnknownAuthorization(t *testing.T) {
	handler, _ := sidecarPluginHandler(t)
	base := fakeSidecarHandler(t, "keyed-auth-plugin", true)
	body := fmt.Sprintf(`{"url":%q,"api_key":"sekrit"}`, base.URL)
	register := httptest.NewRequest(http.MethodPost, "/admin/plugins/register", strings.NewReader(body))
	registerRR := httptest.NewRecorder()
	handler.ServeHTTP(registerRR, register)
	if registerRR.Code != http.StatusCreated {
		t.Fatalf("register = %d body=%s", registerRR.Code, registerRR.Body.String())
	}

	// An arbitrary bearer must not be treated as a plugin credential.  If the
	// old pass-through path ran, the injected X-Plugin-Key would make the fake
	// sidecar return 200.
	req := httptest.NewRequest(http.MethodGet, "/admin/plugins/keyed-auth-plugin/proxy/api/echo", nil)
	req.Header.Set("Authorization", "Bearer attacker-token")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("unknown authorization = %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestProxySidecarAcceptsRegisteredPluginAuthorization(t *testing.T) {
	handler, _ := sidecarPluginHandler(t)
	base := fakeSidecarHandler(t, "plugin-auth-plugin", true)
	body := fmt.Sprintf(`{"url":%q,"api_key":"sekrit"}`, base.URL)
	register := httptest.NewRequest(http.MethodPost, "/admin/plugins/register", strings.NewReader(body))
	registerRR := httptest.NewRecorder()
	handler.ServeHTTP(registerRR, register)
	if registerRR.Code != http.StatusCreated {
		t.Fatalf("register = %d body=%s", registerRR.Code, registerRR.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/plugins/plugin-auth-plugin/proxy/api/echo", nil)
	req.Header.Set("Authorization", "bEaReR sekrit")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"key":"sekrit"`) {
		t.Fatalf("registered plugin authorization = %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestProxySidecarDoesNotMixInvalidHeaderWithQueryAdminToken(t *testing.T) {
	handler, _ := sidecarPluginHandler(t)
	base := fakeSidecarHandler(t, "mixed-auth-plugin", true)
	body := fmt.Sprintf(`{"url":%q,"api_key":"sekrit"}`, base.URL)
	register := httptest.NewRequest(http.MethodPost, "/admin/plugins/register", strings.NewReader(body))
	registerRR := httptest.NewRecorder()
	handler.ServeHTTP(registerRR, register)
	if registerRR.Code != http.StatusCreated {
		t.Fatalf("register = %d body=%s", registerRR.Code, registerRR.Body.String())
	}

	// A stale/forged Authorization header must not be rescued by a valid ?t=;
	// callers must choose one explicit authentication mode.
	req := httptest.NewRequest(http.MethodGet, "/admin/plugins/mixed-auth-plugin/proxy/api/echo?t=admin-test", nil)
	req.Header.Set("Authorization", "Bearer attacker-token")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("mixed credentials = %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestProxySidecarRejectsDuplicateQueryTokens(t *testing.T) {
	handler, _ := sidecarPluginHandler(t)
	base := fakeSidecarHandler(t, "duplicate-query-plugin", false)
	body := fmt.Sprintf(`{"url":%q}`, base.URL)
	register := httptest.NewRequest(http.MethodPost, "/admin/plugins/register", strings.NewReader(body))
	registerRR := httptest.NewRecorder()
	handler.ServeHTTP(registerRR, register)
	if registerRR.Code != http.StatusCreated {
		t.Fatalf("register = %d body=%s", registerRR.Code, registerRR.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/plugins/duplicate-query-plugin/proxy/?t=admin-test&t=admin-test", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("duplicate query token = %d body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("cache-control=%q, want no-store", got)
	}
}

func TestProxySidecarRequiresEnabledPlugin(t *testing.T) {
	handler, _ := sidecarPluginHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/admin/plugins/nope/proxy/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("proxy disabled plugin = %d", rr.Code)
	}
}

func TestProxySidecarRejectsBadURL(t *testing.T) {
	handler, svc := sidecarPluginHandler(t)
	// Register with a valid manifest but health check against a dead port:
	// use a manifest server that always 404s healthz via a path trick —
	// simpler: register normally then corrupt the stored spec through
	// the public API is not possible, so verify SidecarFor rejects.
	if _, err := svc.RegisterSidecar("http://127.0.0.1:1", "", nil); err == nil {
		t.Fatal("expected registration failure against dead port")
	}
	_ = handler
}

func TestValidPluginProxyPathRejectsTraversal(t *testing.T) {
	for _, value := range []string{"", "/", "/app", "/app/", "v0/management"} {
		if !validPluginProxyPath(value) {
			t.Errorf("valid proxy path %q rejected", value)
		}
	}
	for _, value := range []string{"../admin", "/app/../admin", "/app/./x", "/app//x", `\\admin`, "/app%2f..", "/app?token=x", "/app#fragment"} {
		if validPluginProxyPath(value) {
			t.Errorf("unsafe proxy path %q accepted", value)
		}
	}
}

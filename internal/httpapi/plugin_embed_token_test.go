package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/config"
	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/httpapi"
	"github.com/lan/meta-gateway/internal/plugins"
	"github.com/lan/meta-gateway/internal/store"
)

// A cookie-session console (team mode) has no bearer token, so the iframe used
// to request the plugin page with an empty ?t= and the proxy answered
// {"error":"unauthorized"} — permanently, no matter how often the operator
// signed in again. The console now asks for a short-lived embed token; this
// pins both halves: the empty-token request is still refused, and the minted
// token loads the very page the operator clicked.
func TestEmbedTokenLoadsPluginPageWithoutABearer(t *testing.T) {
	plugin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			_, _ = io.WriteString(w, `{"status":"ok"}`)
		case "/":
			_, _ = io.WriteString(w, "embed-page")
		default:
			_, _ = io.WriteString(w, `{"path":"`+r.URL.Path+`"}`)
		}
	}))
	t.Cleanup(plugin.Close)

	server := newEmbedGateway(t)
	registered := post(t, server+"/admin/plugins/register", map[string]any{
		"id": "embed-plugin", "name": "Embed Plugin", "url": plugin.URL,
		"api_key": "embed-plugin-key", "health_path": "/healthz",
	})
	var pluginRow struct {
		ID string `json:"id"`
	}
	json.Unmarshal(registered, &pluginRow)

	// What the iframe sent before: a token the console does not have.
	status, _, body := pluginProxyGet(t, server, "/admin/plugins/embed-plugin/proxy/?t=")
	if status != http.StatusUnauthorized || !strings.Contains(string(body), "unauthorized") {
		t.Fatalf("empty ?t= = %d %q, want 401 unauthorized", status, body)
	}

	// The console mints one instead.
	var minted struct {
		Token string `json:"token"`
	}
	json.Unmarshal(post(t, server+"/admin/plugins/embed-plugin/embed-token", map[string]any{}), &minted)
	if !strings.HasPrefix(minted.Token, "mg-sess.") {
		t.Fatalf("embed token = %q, want a signed session token", minted.Token)
	}

	status, _, body = pluginProxyGet(t, server, "/admin/plugins/embed-plugin/proxy/?t="+minted.Token)
	if status != http.StatusOK || string(body) != "embed-page" {
		t.Fatalf("page with embed token = %d %q", status, body)
	}
	// The plugin's own API goes through the same route, so the token covers it.
	status, _, body = pluginProxyGet(t, server, "/admin/plugins/embed-plugin/proxy/api/state?t="+minted.Token)
	if status != http.StatusOK || !strings.Contains(string(body), `"/api/state"`) {
		t.Fatalf("api with embed token = %d %q", status, body)
	}
}

// A plugin that is not enabled must not hand out a bearer.
func TestEmbedTokenRefusesAnUnknownPlugin(t *testing.T) {
	server := newEmbedGateway(t)
	req, _ := http.NewRequest(http.MethodPost, server+"/admin/plugins/nope/embed-token", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer admin-test")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown plugin = %d, want 404", resp.StatusCode)
	}
}

func newEmbedGateway(t *testing.T) string {
	t.Helper()
	dataDir := t.TempDir()
	db, err := store.OpenTest(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	enc, err := crypto.New("plugin-embed-test-master-key-32-ch!")
	if err != nil {
		t.Fatal(err)
	}
	pluginService, err := plugins.NewService(filepath.Join(dataDir, "plugins"), db.Plugin)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		AdminToken:         "admin-test",
		AdminTokens:        []string{"admin-test"},
		MetricsToken:       "metrics-test",
		BackupDir:          filepath.Join(dataDir, "backups"),
		MaxAdminBodyBytes:  1 << 20,
		AuditRetentionDays: 90,
		AuditRetentionRows: 100000,
		OutboundAllowCIDRs: []string{"127.0.0.1/32"},
	}
	handler := httpapi.NewWithDependencies(cfg, db, enc, httpapi.Dependencies{PluginService: pluginService})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		httpapi.StopBackground(ctx)
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server.URL
}

// pluginProxyGet walks the public plugin route the iframe navigates to, with no
// Authorization header — that is the whole point: an iframe cannot send one.
func pluginProxyGet(t *testing.T, server, path string) (int, http.Header, []byte) {
	t.Helper()
	resp, err := http.Get(server + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, body
}

var _ = fmt.Sprintf

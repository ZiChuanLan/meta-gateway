package httpapi_test

import (
	"context"
	"encoding/json"
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
	"github.com/lan/meta-gateway/internal/outbound"
	"github.com/lan/meta-gateway/internal/plugins"
	"github.com/lan/meta-gateway/internal/store"
)

// Model discovery goes through the outbound SSRF policy, and a plugin listens on
// a loopback port the gateway assigned (managed) or an operator registered
// (sidecar). Before the plugin endpoints were exempted, "获取模型" answered
// 502 {"error":"transport"} for every plugin channel even though the plugin was
// alive and relay to the same address worked.
//
// Both wirings matter: the router builds the outbound client from its own
// policy when an embedder passes none, while cmd/server passes the policy its
// client dials with. A second policy object would silently lose the exception.
func TestPluginEndpointsAreReachableForModelDiscovery(t *testing.T) {
	for _, inject := range []bool{false, true} {
		name := "router-built outbound client"
		if inject {
			name = "injected policy and client"
		}
		t.Run(name, func(t *testing.T) {
			registered := newModelsUpstream(t)
			server := newPlainOutboundGateway(t, inject)

			post(t, server+"/admin/plugins/register", map[string]any{
				"id": "models-plugin", "name": "Models Plugin", "url": registered.URL,
				"api_key": "models-plugin-key", "page_path": "/", "health_path": "/healthz",
			})

			status, payload := previewModels(t, server, registered.URL+"/v1")
			if status != http.StatusOK {
				t.Fatalf("preview of a registered plugin = %d %s", status, payload)
			}
			if !strings.Contains(string(payload), "opencode-zen-free/big-pickle") {
				t.Fatalf("preview returned %s", payload)
			}

			// The exemption is per endpoint: another live loopback server stays
			// blocked, so this cannot be mistaken for "loopback is open".
			other := newModelsUpstream(t)
			if status, payload := previewModels(t, server, other.URL+"/v1"); status != http.StatusBadGateway {
				t.Fatalf("unregistered loopback port = %d %s, want 502", status, payload)
			}

			// Disabling the plugin retires its endpoint (see
			// TestTargetsFollowPluginEnablement): the next *dial* is refused. A
			// connection already pooled to that endpoint can still be reused, the
			// same way relay traffic to a switched-off plugin is never gated, so
			// this file does not assert on a preview made right after a disable.
		})
	}
}

func newModelsUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			_, _ = io.WriteString(w, `{"status":"ok"}`)
		case "/v1/models":
			_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"opencode-zen-free/big-pickle","object":"model"}]}`)
		default:
			_, _ = io.WriteString(w, `{}`)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// newPlainOutboundGateway deliberately leaves OutboundAllowCIDRs empty: that is
// how a stock deployment looks, and the plugin endpoint must work without it.
func newPlainOutboundGateway(t *testing.T, injectClient bool) string {
	t.Helper()
	dataDir := t.TempDir()
	db, err := store.OpenTest(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	enc, err := crypto.New("plugin-models-test-master-key-32-c!")
	if err != nil {
		t.Fatal(err)
	}
	pluginService, err := plugins.NewService(filepath.Join(dataDir, "plugins"), db.Plugin)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		AdminToken:        "admin-test",
		AdminTokens:       []string{"admin-test"},
		MetricsToken:      "metrics-test",
		BackupDir:         filepath.Join(dataDir, "backups"),
		MaxAdminBodyBytes: 1 << 20,
	}
	dependencies := httpapi.Dependencies{PluginService: pluginService}
	if injectClient {
		policy, err := outbound.NewPolicy(outbound.Options{})
		if err != nil {
			t.Fatal(err)
		}
		dependencies.OutboundPolicy = policy
		dependencies.OutboundClient = outbound.NewClient(policy, outbound.ClientOptions{})
	}
	handler := httpapi.NewWithDependencies(cfg, db, enc, dependencies)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		httpapi.StopBackground(ctx)
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server.URL
}

func previewModels(t *testing.T, server, baseURL string) (int, []byte) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"base_url": baseURL, "secret": "models-plugin-key", "type_hint": "openai-compatible",
	})
	req, _ := http.NewRequest(http.MethodPost, server+"/admin/discovery/models-preview", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer admin-test")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, payload
}

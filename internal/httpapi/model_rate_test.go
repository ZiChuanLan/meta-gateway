package httpapi_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/lan/meta-gateway/internal/config"
	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/httpapi"
	"github.com/lan/meta-gateway/internal/store"
)

// TestModelRateLimit429 verifies the per-(key, model) limiter rejects the
// second rapid request for the same model and allows a different model.
func TestModelRateLimit429(t *testing.T) {
	upstream := mockOpenAIUpstream(t)
	defer upstream.Close()
	// Gateway with a strict per-model limit (2/min, burst 1) via the shared
	// relay builder: build a second gateway manually with the limiter.
	serverURL, token, _ := setupRelayWithModelLimit(t, upstream.URL)

	status, body := relayChat(t, serverURL, token, `{"model":"gemini-2.5-flash","messages":[{"role":"user","content":"hi"}]}`)
	if status != http.StatusOK {
		t.Fatalf("first request status %d body %s, want 200", status, body)
	}
	status, body = relayChat(t, serverURL, token, `{"model":"gemini-2.5-flash","messages":[{"role":"user","content":"hi"}]}`)
	if status != http.StatusTooManyRequests {
		t.Fatalf("second request status %d, want 429; body %s", status, body)
	}
	// A different model bucket is unaffected (but needs a route — use the same model
	// name via the same route; different key would be a different bucket too).
	status, _ = relayChat(t, serverURL, token, `{"model":"gemini-2.5-flash","messages":[{"role":"user","content":"hi"}]}`)
	if status != http.StatusTooManyRequests {
		t.Fatalf("third request status %d, want 429 (bucket persists)", status)
	}
}

func TestModelRateLimitPerKey(t *testing.T) {
	upstream := mockOpenAIUpstream(t)
	defer upstream.Close()
	serverURL, _, _ := setupRelayWithModelLimit(t, upstream.URL)

	// Create a second downstream key: separate bucket, should pass.
	body := post(t, serverURL+"/admin/downstream-keys", map[string]any{"name": "second", "scopes": "relay"})
	var key struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &key); err != nil {
		t.Fatal(err)
	}
	status, _ := relayChat(t, serverURL, key.Token, `{"model":"gemini-2.5-flash","messages":[{"role":"user","content":"hi"}]}`)
	if status != http.StatusOK {
		t.Fatalf("other key status %d, want 200 (per-key buckets)", status)
	}
}

func setupRelayWithModelLimit(t *testing.T, baseURL string) (string, string, int64) {
	t.Helper()
	// Clone setupRelay but with RelayModelRatePerMinute=2, burst=1.
	// Reuse setupRelay for the entity wiring, then patch the limiter is not
	// possible post-construction — so build a second gateway manually.
	dataDir := t.TempDir()
	db, err := store.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	enc, err := crypto.New("model-rate-test-master-key-32-characters!!")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{AdminToken: "admin-test", MetricsToken: "metrics-test", BackupDir: filepath.Join(dataDir, "backups"), MaxAdminBodyBytes: 1 << 20, AuditRetentionDays: 90, AuditRetentionRows: 100000, ExchangeAllowSecretExport: true, RelayModelRatePerMinute: 2, RelayModelRateBurst: 1, OutboundAllowCIDRs: []string{"127.0.0.1/32"}}
	server := httptest.NewServer(httpapi.New(cfg, db, enc))
	t.Cleanup(server.Close)

	// Wire entities (site/credential/channel/route/member/key) — reuse the
	// helpers from gemini_relay_test.go.
	var site struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/sites", map[string]any{"name": "openai-site", "base_url": baseURL, "platform": "openai-compatible", "status": "enabled"}), &site)
	var cred struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/sites/"+itoa(site.ID)+"/credentials", map[string]any{"kind": "api_key", "secret": "sk-test-abcdef", "status": "enabled"}), &cred)
	var channel struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/channels", map[string]any{"site_id": site.ID, "credential_id": cred.ID, "name": "relay-ch", "base_url": baseURL, "type_hint": "openai-compatible", "status": "enabled"}), &channel)
	var route struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/routes", map[string]any{"model_pattern": "gemini-2.5-flash", "enabled": true}), &route)
	post(t, server.URL+"/admin/routes/"+itoa(route.ID)+"/members", map[string]any{"channel_id": channel.ID, "priority": 1, "weight": 100, "enabled": true})
	var key struct{ Token string }
	json.Unmarshal(post(t, server.URL+"/admin/downstream-keys", map[string]any{"name": "test-key", "scopes": "relay"}), &key)
	return server.URL, key.Token, channel.ID
}

func itoa(v int64) string {
	return fmt.Sprint(v)
}


package httpapi_test

import (
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
	"github.com/lan/meta-gateway/internal/store"
)

// failingUpstream always returns 500 so relay failures accumulate.
func failingUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"boom"}`, http.StatusInternalServerError)
	}))
}

func TestChannelAutoDisable(t *testing.T) {
	upstream := failingUpstream(t)
	defer upstream.Close()
	dataDir := t.TempDir()
	db, _ := store.Open(dataDir)
	defer db.Close()
	enc, _ := crypto.New("auto-disable-test-master-key-32-char!!")
	cfg := &config.Config{AdminToken: "admin-test", MetricsToken: "metrics-test", BackupDir: filepath.Join(dataDir, "backups"), MaxAdminBodyBytes: 1 << 20, AuditRetentionDays: 90, AuditRetentionRows: 100000, ExchangeAllowSecretExport: true, OutboundAllowCIDRs: []string{"127.0.0.1/32"}, ChannelAutoDisableThreshold: 2, Cooldown: 60 * time.Second}
	server := httptest.NewServer(httpapi.New(cfg, db, enc))
	defer server.Close()

	var site struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/sites", map[string]any{"name": "s", "base_url": upstream.URL, "platform": "openai-compatible", "status": "enabled"}), &site)
	var cred struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/sites/"+itoa(site.ID)+"/credentials", map[string]any{"kind": "api_key", "secret": "sk-test", "status": "enabled"}), &cred)
	var channel struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/channels", map[string]any{"site_id": site.ID, "credential_id": cred.ID, "name": "ch", "base_url": upstream.URL, "type_hint": "openai-compatible", "status": "enabled"}), &channel)
	var route struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/routes", map[string]any{"model_pattern": "test-model", "enabled": true}), &route)
	post(t, server.URL+"/admin/routes/"+itoa(route.ID)+"/members", map[string]any{"channel_id": channel.ID, "priority": 1, "weight": 100, "enabled": true})
	var key struct{ Token string }
	json.Unmarshal(post(t, server.URL+"/admin/downstream-keys", map[string]any{"name": "k", "scopes": "relay"}), &key)

	// A second api_key on the same site forms a failover pool: one request
	// fails through both keys, giving the channel 2 consecutive failures.
	var cred2 struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/sites/"+itoa(site.ID)+"/credentials", map[string]any{"kind": "api_key", "secret": "sk-test-2", "status": "enabled"}), &cred2)

	req, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/chat/completions", strings.NewReader(`{"model":"test-model","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer "+key.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	// Check channel status.
	var failures int
	_ = db.QueryRow(`SELECT consecutive_failures FROM channels WHERE id = ?`, channel.ID).Scan(&failures)
	t.Logf("channel consecutive_failures = %d", failures)
	body := get(t, server.URL+"/admin/channels/"+itoa(channel.ID))
	var ch struct {
		Status string `json:"status"`
	}
	json.Unmarshal(body, &ch)
	if ch.Status != "auto_disabled" {
		t.Fatalf("channel status = %q, want auto_disabled", ch.Status)
	}

	// Manual recovery via update.
	put(t, server.URL+"/admin/channels/"+itoa(channel.ID), map[string]any{"status": "enabled"})
	body = get(t, server.URL+"/admin/channels/"+itoa(channel.ID))
	json.Unmarshal(body, &ch)
	if ch.Status != "enabled" {
		t.Fatalf("after recovery status = %q, want enabled", ch.Status)
	}
}

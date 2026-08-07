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
	cfg := &config.Config{AdminToken: "admin-test", MetricsToken: "metrics-test", BackupDir: filepath.Join(dataDir, "backups"), MaxAdminBodyBytes: 1 << 20, AuditRetentionDays: 90, AuditRetentionRows: 100000, ExchangeAllowSecretExport: true, OutboundAllowCIDRs: []string{"127.0.0.1/32"}, ChannelAutoDisableThreshold: 2, Cooldown: time.Second}
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

	// Two api_keys on the same site form a failover pool. Each request fails
	// through both keys; the channel failure counter increments once per
	// request, and the per-key counter (same threshold) cascades the channel
	// to auto_disabled once every key has failed twice.
	var cred2 struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/sites/"+itoa(site.ID)+"/credentials", map[string]any{"kind": "api_key", "secret": "sk-test-2", "status": "enabled"}), &cred2)

	for attempt := 0; attempt < 2; attempt++ {
		req, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/chat/completions", strings.NewReader(`{"model":"test-model","messages":[{"role":"user","content":"hi"}]}`))
		req.Header.Set("Authorization", "Bearer "+key.Token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		// A failed request parks the member in cooldown; the next failure must
		// arrive after it expires to count as a consecutive failure.
		if attempt == 0 {
			time.Sleep(1100 * time.Millisecond)
		}
	}
	// Check channel status: both keys failing twice triggers the per-key
	// cascade, which parks the channel even before the counter path.
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

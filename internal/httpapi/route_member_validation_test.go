package httpapi_test

// The manual member endpoint is the console's "add one channel to this route"
// action. A body that names no channel, or a channel that no longer exists,
// used to fall through to the foreign key and come back as an opaque 500
// ("database operation failed"); it is a client error and must say so.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/config"
	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/httpapi"
	"github.com/lan/meta-gateway/internal/store"
)

func TestCreateRouteMemberValidatesChannel(t *testing.T) {
	dataDir := t.TempDir()
	db, _ := store.OpenTest(dataDir)
	defer db.Close()
	enc, _ := crypto.New("route-member-test-master-key-32char")
	cfg := &config.Config{
		AdminToken: "admin-test", MetricsToken: "metrics-test",
		BackupDir: filepath.Join(dataDir, "backups"), MaxAdminBodyBytes: 1 << 20,
		AuditRetentionDays: 90, AuditRetentionRows: 100000,
		ExchangeAllowSecretExport: true, OutboundAllowCIDRs: []string{"127.0.0.1/32"},
		Cooldown: time.Second,
	}
	server := httptest.NewServer(httpapi.NewTestRouter(t, cfg, db, enc))
	defer server.Close()

	var site struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/sites", map[string]any{"name": "s", "base_url": "https://api.example.com", "platform": "openai-compatible", "status": "enabled"}), &site)
	var cred struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/sites/"+itoa(site.ID)+"/credentials", map[string]any{"kind": "api_key", "secret": "sk-test", "status": "enabled"}), &cred)
	var channel struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/channels", map[string]any{"site_id": site.ID, "credential_id": cred.ID, "name": "online", "base_url": "https://api.example.com", "type_hint": "openai-compatible", "status": "enabled", "models_csv": "deepseek-v4-flash"}), &channel)

	var route struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/routes", map[string]any{"model_pattern": "deepseek-v4-flash", "enabled": true}), &route)
	membersURL := server.URL + "/admin/routes/" + itoa(route.ID) + "/members"

	if code := postExpectStatus(t, membersURL, map[string]any{}); code != http.StatusBadRequest {
		t.Fatalf("missing channel_id: status=%d, want 400", code)
	}
	if code := postExpectStatus(t, membersURL, map[string]any{"channel_id": 99999, "weight": 100}); code != http.StatusBadRequest {
		t.Fatalf("unknown channel: status=%d, want 400", code)
	}
	if code := postExpectStatus(t, membersURL, map[string]any{"channel_id": channel.ID, "weight": 100}); code != http.StatusCreated {
		t.Fatalf("valid member: status=%d, want 201", code)
	}
	members := listMemberChannelIDs(t, server.URL, route.ID)
	if len(members) != 1 || members[0] != channel.ID {
		t.Fatalf("members = %v, want [%d]", members, channel.ID)
	}
}

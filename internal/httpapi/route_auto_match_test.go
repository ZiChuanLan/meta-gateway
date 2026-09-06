package httpapi_test

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/config"
	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/httpapi"
	"github.com/lan/meta-gateway/internal/store"
)

// TestRouteAutoMatch covers the add-route dialog's auto-match end to end: the
// preview endpoint, member attachment on create, and the flag being a
// create-time directive rather than stored route state.
func TestRouteAutoMatch(t *testing.T) {
	dataDir := t.TempDir()
	db, _ := store.Open(dataDir)
	defer db.Close()
	enc, _ := crypto.New("auto-match-test-master-key-32-char!")
	cfg := &config.Config{AdminToken: "admin-test", MetricsToken: "metrics-test", BackupDir: filepath.Join(dataDir, "backups"), MaxAdminBodyBytes: 1 << 20, AuditRetentionDays: 90, AuditRetentionRows: 100000, ExchangeAllowSecretExport: true, OutboundAllowCIDRs: []string{"127.0.0.1/32"}, Cooldown: time.Second}
	server := httptest.NewServer(httpapi.New(cfg, db, enc))
	defer server.Close()

	var site struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/sites", map[string]any{"name": "s", "base_url": "https://api.example.com", "platform": "openai-compatible", "status": "enabled"}), &site)
	var cred struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/sites/"+itoa(site.ID)+"/credentials", map[string]any{"kind": "api_key", "secret": "sk-test", "status": "enabled"}), &cred)
	newChannel := func(name, modelsCSV, status string) int64 {
		var channel struct{ ID int64 }
		json.Unmarshal(post(t, server.URL+"/admin/channels", map[string]any{"site_id": site.ID, "credential_id": cred.ID, "name": name, "base_url": "https://api.example.com", "type_hint": "openai-compatible", "status": status, "models_csv": modelsCSV}), &channel)
		return channel.ID
	}
	online := newChannel("online", "deepseek-v4-flash,gpt-4o", "enabled")
	offline := newChannel("offline", "deepseek-v4-flash", "disabled")

	// Preview lists only the enabled channel.
	var preview struct {
		Items []struct {
			ChannelID int64  `json:"channel_id"`
			Source    string `json:"source"`
		}
	}
	json.Unmarshal(get(t, server.URL+"/admin/discovery/model-channels?model=deepseek-v4-flash"), &preview)
	if len(preview.Items) != 1 || preview.Items[0].ChannelID != online || preview.Items[0].Source != "models_csv" {
		t.Fatalf("preview = %+v, want only channel %d via models_csv", preview, online)
	}

	// Create with the ids attaches exactly the matching, enabled channels:
	// the disabled one and the unknown id are skipped.
	var route struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/routes", map[string]any{"model_pattern": "deepseek-v4-flash", "enabled": true, "auto_match_channel_ids": []int64{online, offline, 99999}}), &route)
	members := listMemberChannelIDs(t, server.URL, route.ID)
	if len(members) != 1 || members[0] != online {
		t.Fatalf("members = %v, want [%d]", members, online)
	}

	// The ids are not route state: updating without them changes nothing, and
	// a plain create stays bare.
	var bare struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/routes", map[string]any{"model_pattern": "gpt-4o", "enabled": true}), &bare)
	if bareMembers := listMemberChannelIDs(t, server.URL, bare.ID); len(bareMembers) != 0 {
		t.Fatalf("bare members = %v, want none", bareMembers)
	}
}

func listMemberChannelIDs(t *testing.T, base string, routeID int64) []int64 {
	t.Helper()
	var members []struct {
		ChannelID int64 `json:"channel_id"`
	}
	json.Unmarshal(get(t, fmt.Sprintf("%s/admin/routes/%d/members", base, routeID)), &members)
	ids := make([]int64, 0, len(members))
	for _, member := range members {
		ids = append(ids, member.ChannelID)
	}
	return ids
}

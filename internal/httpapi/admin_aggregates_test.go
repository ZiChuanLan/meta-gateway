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

// TestAggregateModelEndToEnd covers the console's aggregate-model tool over the
// admin API: create with model picks, the members forwarding the real upstream
// names, list, delete (with and without the route).
func TestAggregateModelEndToEnd(t *testing.T) {
	dataDir := t.TempDir()
	db, _ := store.OpenTest(dataDir)
	defer db.Close()
	enc, _ := crypto.New("aggregate-test-master-key-32-char!")
	cfg := &config.Config{AdminToken: "admin-test", MetricsToken: "metrics-test", BackupDir: filepath.Join(dataDir, "backups"), MaxAdminBodyBytes: 1 << 20, AuditRetentionDays: 90, AuditRetentionRows: 100000, ExchangeAllowSecretExport: true, OutboundAllowCIDRs: []string{"127.0.0.1/32"}, Cooldown: time.Second}
	server := httptest.NewServer(httpapi.NewTestRouter(t, cfg, db, enc))
	defer server.Close()

	var site struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/sites", map[string]any{"name": "s", "base_url": "https://api.example.com", "platform": "openai-compatible", "status": "enabled"}), &site)
	var cred struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/sites/"+itoa(site.ID)+"/credentials", map[string]any{"kind": "api_key", "secret": "sk-test", "status": "enabled"}), &cred)
	newChannel := func(name, modelsCSV string) int64 {
		var channel struct{ ID int64 }
		json.Unmarshal(post(t, server.URL+"/admin/channels", map[string]any{"site_id": site.ID, "credential_id": cred.ID, "name": name, "base_url": "https://api.example.com", "type_hint": "openai-compatible", "status": "enabled", "models_csv": modelsCSV}), &channel)
		return channel.ID
	}
	dsA := newChannel("ds", "deepseek-v4.1")
	glm := newChannel("glm", "glm-5.3,deepseek-v4.1")

	// Create the aggregate from two picked models. Every enabled channel serving
	// them becomes a member that forwards the picked name upstream.
	var created struct {
		ID      int64 `json:"id"`
		RouteID int64 `json:"route_id"`
	}
	json.Unmarshal(post(t, server.URL+"/admin/model-aggregates", map[string]any{
		"name": "hermes-model",
		"note": "team alias",
		"members": []map[string]any{
			{"model": "deepseek-v4.1"},
			{"model": "glm-5.3"},
		},
	}), &created)
	if created.RouteID == 0 {
		t.Fatal("aggregate create returned no route id")
	}

	var members []struct {
		ChannelID   int64  `json:"channel_id"`
		MappingJSON string `json:"mapping_json"`
		GroupName   string `json:"group_name"`
	}
	json.Unmarshal(get(t, fmt.Sprintf("%s/admin/routes/%d/members", server.URL, created.RouteID)), &members)
	if len(members) != 3 {
		t.Fatalf("members = %d, want 3 (ds + glm twice: glm-5.3 and deepseek-v4.1)", len(members))
	}
	// Key: channel + the upstream name it forwards. The glm channel serves both
	// picked models, so it appears twice with different redirects — the shape
	// the partial unique index requires and the alias semantics need.
	type memberKey struct {
		channel int64
		real    string
	}
	seen := map[memberKey]bool{}
	for _, m := range members {
		var mapping struct {
			Real string `json:"real"`
		}
		_ = json.Unmarshal([]byte(m.MappingJSON), &mapping)
		if mapping.Real == "" {
			t.Fatalf("member for channel %d has no explicit real-name redirect", m.ChannelID)
		}
		if m.GroupName != "" && m.GroupName != "default" {
			t.Fatalf("group = %q, want default", m.GroupName)
		}
		seen[memberKey{m.ChannelID, mapping.Real}] = true
	}
	if !seen[memberKey{dsA, "deepseek-v4.1"}] {
		t.Fatalf("missing ds -> deepseek-v4.1 member: %v", members)
	}
	if !seen[memberKey{glm, "glm-5.3"}] || !seen[memberKey{glm, "deepseek-v4.1"}] {
		t.Fatalf("missing glm members (both names): %v", members)
	}

	// The list shows the aggregate with a live route.
	var list struct {
		Items []struct {
			ID         int64  `json:"id"`
			Name       string `json:"name"`
			RouteID    int64  `json:"route_id"`
			RouteAlive bool   `json:"route_alive"`
		} `json:"items"`
	}
	json.Unmarshal(get(t, server.URL+"/admin/model-aggregates"), &list)
	if len(list.Items) != 1 || list.Items[0].Name != "hermes-model" || !list.Items[0].RouteAlive {
		t.Fatalf("list = %+v", list.Items)
	}

	// A duplicate aggregate name conflicts rather than double-creating.
	if code := postExpectStatus(t, server.URL+"/admin/model-aggregates", map[string]any{
		"name":    "hermes-model",
		"members": []map[string]any{},
	}); code != 409 && code != 400 {
		t.Fatalf("duplicate create status = %d, want 409", code)
	}

	// Delete with the route removes the route too.
	deleteStatus(t, fmt.Sprintf("%s/admin/model-aggregates/%d", server.URL, created.ID))
	if code := getStatus(t, fmt.Sprintf("%s/admin/routes/%d", server.URL, created.RouteID)); code != 404 {
		t.Fatalf("route status after aggregate delete = %d, want 404", code)
	}
	// Deleting again is 404.
	if code := deleteStatus(t, fmt.Sprintf("%s/admin/model-aggregates/%d", server.URL, created.ID)); code != 404 {
		t.Fatalf("second delete status = %d, want 404", code)
	}
}

// TestAggregateKeepRoute keeps the route when with_route=false: the alias
// survives as a plain route the operator can keep editing.
func TestAggregateKeepRoute(t *testing.T) {
	dataDir := t.TempDir()
	db, _ := store.OpenTest(dataDir)
	defer db.Close()
	enc, _ := crypto.New("aggregate-test-master-key-32-char!")
	cfg := &config.Config{AdminToken: "admin-test", MetricsToken: "metrics-test", BackupDir: filepath.Join(dataDir, "backups"), MaxAdminBodyBytes: 1 << 20, AuditRetentionDays: 90, AuditRetentionRows: 100000, ExchangeAllowSecretExport: true, OutboundAllowCIDRs: []string{"127.0.0.1/32"}, Cooldown: time.Second}
	server := httptest.NewServer(httpapi.NewTestRouter(t, cfg, db, enc))
	defer server.Close()

	var site struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/sites", map[string]any{"name": "s", "base_url": "https://api.example.com", "platform": "openai-compatible", "status": "enabled"}), &site)
	var cred struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/sites/"+itoa(site.ID)+"/credentials", map[string]any{"kind": "api_key", "secret": "sk-test", "status": "enabled"}), &cred)
	var channel struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/channels", map[string]any{"site_id": site.ID, "credential_id": cred.ID, "name": "ch", "base_url": "https://api.example.com", "type_hint": "openai-compatible", "status": "enabled", "models_csv": "glm-5.3"}), &channel)

	var created struct {
		ID      int64 `json:"id"`
		RouteID int64 `json:"route_id"`
	}
	json.Unmarshal(post(t, server.URL+"/admin/model-aggregates", map[string]any{
		"name":    "agg-keep",
		"members": []map[string]any{{"model": "glm-5.3", "source_channel_ids": []int64{channel.ID}}},
	}), &created)

	deleteStatus(t, fmt.Sprintf("%s/admin/model-aggregates/%d?with_route=false", server.URL, created.ID))
	if code := getStatus(t, fmt.Sprintf("%s/admin/routes/%d", server.URL, created.RouteID)); code != 200 {
		t.Fatalf("route status after keep-route delete = %d, want 200", code)
	}
	var list struct {
		Items []struct{ Name string } `json:"items"`
	}
	json.Unmarshal(get(t, server.URL+"/admin/model-aggregates"), &list)
	if len(list.Items) != 0 {
		t.Fatalf("aggregate record survived delete: %+v", list.Items)
	}
}

// TestConsolePinsAPI covers the shared pin list and toggle.
func TestConsolePinsAPI(t *testing.T) {
	dataDir := t.TempDir()
	db, _ := store.OpenTest(dataDir)
	defer db.Close()
	enc, _ := crypto.New("aggregate-test-master-key-32-char!")
	cfg := &config.Config{AdminToken: "admin-test", MetricsToken: "metrics-test", BackupDir: filepath.Join(dataDir, "backups"), MaxAdminBodyBytes: 1 << 20, AuditRetentionDays: 90, AuditRetentionRows: 100000, ExchangeAllowSecretExport: true, OutboundAllowCIDRs: []string{"127.0.0.1/32"}, Cooldown: time.Second}
	server := httptest.NewServer(httpapi.NewTestRouter(t, cfg, db, enc))
	defer server.Close()

	json.Unmarshal(post(t, server.URL+"/admin/console-pins", map[string]any{"kind": "channel", "ref": "5", "pin": true}), &struct{}{})
	json.Unmarshal(post(t, server.URL+"/admin/console-pins", map[string]any{"kind": "model", "ref": "hermes-model", "pin": true}), &struct{}{})

	var pins map[string][]string
	json.Unmarshal(get(t, server.URL+"/admin/console-pins"), &pins)
	if len(pins["channel"]) != 1 || pins["channel"][0] != "5" {
		t.Fatalf("channel pins = %v", pins["channel"])
	}
	if len(pins["model"]) != 1 || pins["model"][0] != "hermes-model" {
		t.Fatalf("model pins = %v", pins["model"])
	}

	// Unpin, and an unknown kind is refused.
	json.Unmarshal(post(t, server.URL+"/admin/console-pins", map[string]any{"kind": "channel", "ref": "5", "pin": false}), &struct{}{})
	pins = nil
	json.Unmarshal(get(t, server.URL+"/admin/console-pins"), &pins)
	if len(pins["channel"]) != 0 {
		t.Fatalf("channel pins after unpin = %v", pins["channel"])
	}
	if code := postExpectStatus(t, server.URL+"/admin/console-pins", map[string]any{"kind": "bogus", "ref": "1", "pin": true}); code != 400 {
		t.Fatalf("unknown kind status = %d, want 400", code)
	}
}

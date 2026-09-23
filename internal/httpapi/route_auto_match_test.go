package httpapi_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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

// postExpectStatus sends a POST and returns the status code instead of failing
// on an error code, so a test can assert the 404 the happy-path helper hides.
func postExpectStatus(t *testing.T, url string, payload any) int {
	t.Helper()
	encoded, _ := json.Marshal(payload)
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(encoded))
	req.Header.Set("Authorization", "Bearer admin-test")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_, _ = io.ReadAll(resp.Body)
	return resp.StatusCode
}

// TestRouteAutoMatch covers the add-route dialog's auto-match end to end: the
// preview endpoint, member attachment on create, and the flag being a
// create-time directive rather than stored route state.
func TestRouteAutoMatch(t *testing.T) {
	dataDir := t.TempDir()
	db, _ := store.Open(dataDir)
	defer db.Close()
	enc, _ := crypto.New("auto-match-test-master-key-32-char!")
	cfg := &config.Config{AdminToken: "admin-test", MetricsToken: "metrics-test", BackupDir: filepath.Join(dataDir, "backups"), MaxAdminBodyBytes: 1 << 20, AuditRetentionDays: 90, AuditRetentionRows: 100000, ExchangeAllowSecretExport: true, OutboundAllowCIDRs: []string{"127.0.0.1/32"}, Cooldown: time.Second}
	server := httptest.NewServer(httpapi.NewTestRouter(t, cfg, db, enc))
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
	members := listMembers(t, base, routeID)
	ids := make([]int64, 0, len(members))
	for _, member := range members {
		ids = append(ids, member.ChannelID)
	}
	return ids
}

func listMembers(t *testing.T, base string, routeID int64) []struct {
	ChannelID int64  `json:"channel_id"`
	GroupName string `json:"group_name"`
} {
	t.Helper()
	var members []struct {
		ChannelID int64  `json:"channel_id"`
		GroupName string `json:"group_name"`
	}
	json.Unmarshal(get(t, fmt.Sprintf("%s/admin/routes/%d/members", base, routeID)), &members)
	return members
}

// TestRouteAutoMatchExistingRoute covers the console's one-click "add every
// channel that serves this model" on a route that already exists: the empty
// body means all matches, an explicit list means the kept ones, and both go
// through the same intersection as creation.
func TestRouteAutoMatchExistingRoute(t *testing.T) {
	dataDir := t.TempDir()
	db, _ := store.Open(dataDir)
	defer db.Close()
	enc, _ := crypto.New("auto-match-attach-master-key-32c!")
	cfg := &config.Config{AdminToken: "admin-test", MetricsToken: "metrics-test", BackupDir: filepath.Join(dataDir, "backups"), MaxAdminBodyBytes: 1 << 20, AuditRetentionDays: 90, AuditRetentionRows: 100000, ExchangeAllowSecretExport: true, OutboundAllowCIDRs: []string{"127.0.0.1/32"}, Cooldown: time.Second}
	server := httptest.NewServer(httpapi.NewTestRouter(t, cfg, db, enc))
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
	serving := newChannel("serving", "deepseek-v4-flash", "enabled")
	alsoServing := newChannel("also-serving", "deepseek-v4-flash,gpt-4o", "enabled")
	offline := newChannel("offline", "deepseek-v4-flash", "disabled")

	var route struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/routes", map[string]any{"model_pattern": "deepseek-v4-flash", "enabled": true}), &route)
	if members := listMemberChannelIDs(t, server.URL, route.ID); len(members) != 0 {
		t.Fatalf("bare members = %v, want none", members)
	}

	// An explicit list is intersected with the enabled matches: the disabled
	// channel is refused, so the response reports the skip instead of silently
	// attaching it.
	var result struct {
		Added   int `json:"added"`
		Skipped int `json:"skipped"`
	}
	json.Unmarshal(post(t, fmt.Sprintf("%s/admin/routes/%d/auto-match", server.URL, route.ID),
		map[string]any{"channel_ids": []int64{serving, offline, 99999}}), &result)
	if result.Added != 1 || result.Skipped != 2 {
		t.Fatalf("attach = %+v, want added 1 skipped 2", result)
	}
	if members := listMemberChannelIDs(t, server.URL, route.ID); len(members) != 1 || members[0] != serving {
		t.Fatalf("members = %v, want [%d]", members, serving)
	}

	// Empty body = "every current match", and re-running adds nothing.
	json.Unmarshal(post(t, fmt.Sprintf("%s/admin/routes/%d/auto-match", server.URL, route.ID), map[string]any{}), &result)
	if result.Added != 1 || result.Skipped != 0 {
		t.Fatalf("attach all = %+v, want added 1 skipped 0", result)
	}
	members := listMemberChannelIDs(t, server.URL, route.ID)
	have := map[int64]bool{}
	for _, id := range members {
		have[id] = true
	}
	if len(members) != 2 || !have[serving] || !have[alsoServing] {
		t.Fatalf("members = %v, want both %d and %d", members, serving, alsoServing)
	}
	json.Unmarshal(post(t, fmt.Sprintf("%s/admin/routes/%d/auto-match", server.URL, route.ID), map[string]any{}), &result)
	if result.Added != 0 || result.Skipped != 0 {
		t.Fatalf("idempotent attach = %+v, want a no-op", result)
	}
	if again := listMemberChannelIDs(t, server.URL, route.ID); len(again) != 2 {
		t.Fatalf("members after re-run = %v, want no duplicates", again)
	}

	// group_name targets a member group instead of the implicit default, so
	// the same channel can be attached to a second group without the default
	// membership absorbing it (groups are additive, and a key bound to one
	// group must not silently miss a channel attached to another).
	json.Unmarshal(post(t, fmt.Sprintf("%s/admin/routes/%d/auto-match", server.URL, route.ID),
		map[string]any{"channel_ids": []int64{serving}, "group_name": "blue"}), &result)
	if result.Added != 1 || result.Skipped != 0 {
		t.Fatalf("grouped attach = %+v, want added 1 skipped 0", result)
	}
	grouped := 0
	for _, member := range listMembers(t, server.URL, route.ID) {
		if member.ChannelID == serving && member.GroupName == "blue" {
			grouped++
		}
	}
	if grouped != 1 {
		t.Fatalf("blue membership = %d, want exactly one row", grouped)
	}
	if rows := listMembers(t, server.URL, route.ID); len(rows) != 3 {
		t.Fatalf("member rows = %d, want one per (channel, group) pair", len(rows))
	}

	// An unknown route is a 404, not a silent success.
	if code := postExpectStatus(t, fmt.Sprintf("%s/admin/routes/99999/auto-match", server.URL), map[string]any{}); code != 404 {
		t.Fatalf("unknown route status = %d, want 404", code)
	}
}

// TestCreateRouteBootstrapsCapabilities covers the half of the auto-sync that
// has to happen inside the request: a model that was just routed gets its
// built-in classification row without anyone pressing a button. The external
// half is covered by the worker's own test — it must never block or fail a
// create, so a deployment without catalogs behaves exactly like this.
func TestCreateRouteBootstrapsCapabilities(t *testing.T) {
	dataDir := t.TempDir()
	db, _ := store.Open(dataDir)
	defer db.Close()
	enc, _ := crypto.New("bootstrap-capability-master-key-32!")
	cfg := &config.Config{AdminToken: "admin-test", MetricsToken: "metrics-test", BackupDir: filepath.Join(dataDir, "backups"), MaxAdminBodyBytes: 1 << 20, AuditRetentionDays: 90, AuditRetentionRows: 100000, ExchangeAllowSecretExport: true, OutboundAllowCIDRs: []string{"127.0.0.1/32"}, Cooldown: time.Second}
	server := httptest.NewServer(httpapi.NewTestRouter(t, cfg, db, enc))
	defer server.Close()

	// A brand-new model name no catalog row can exist for yet.
	json.Unmarshal(post(t, server.URL+"/admin/routes", map[string]any{"model_pattern": "grok-imagine-video", "enabled": true}), &struct{}{})

	var caps struct {
		Items []struct {
			Model    string `json:"model"`
			Kind     string `json:"kind"`
			Source   string `json:"source"`
			Endpoint string `json:"-"`
		} `json:"items"`
	}
	json.Unmarshal(get(t, server.URL+"/admin/model-capabilities"), &caps)
	found := false
	for _, item := range caps.Items {
		if item.Model != "grok-imagine-video" {
			continue
		}
		found = true
		if item.Kind != "video" {
			t.Fatalf("kind = %q, want the classifier's video verdict", item.Kind)
		}
		if item.Source != "discovery" {
			t.Fatalf("source = %q, want discovery (classifier-owned, still refreshable)", item.Source)
		}
	}
	if !found {
		t.Fatalf("route create did not bootstrap a capability row: %+v", caps.Items)
	}

	// A wildcard is a matcher, not a callable model: nothing to look up.
	json.Unmarshal(post(t, server.URL+"/admin/routes", map[string]any{"model_pattern": "grok-*", "enabled": true}), &struct{}{})
	json.Unmarshal(get(t, server.URL+"/admin/model-capabilities"), &caps)
	for _, item := range caps.Items {
		if item.Model == "grok-*" {
			t.Fatal("a wildcard pattern must not become a capability row")
		}
	}
}

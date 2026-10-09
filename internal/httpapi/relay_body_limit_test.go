package httpapi_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lan/meta-gateway/internal/config"
	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/httpapi"
	"github.com/lan/meta-gateway/internal/store"
)

// setupRelayWithBodyLimits builds the standard relay wiring with explicit
// request-body ceilings, in whole megabytes (the unit the console speaks), so a
// test can send a body that is over the limit without allocating tens of
// megabytes.
func setupRelayWithBodyLimits(t *testing.T, baseURL string, jsonMB, imageMB int64) (string, string) {
	t.Helper()
	dataDir := t.TempDir()
	db, err := store.OpenTest(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	enc, err := crypto.New("relay-body-limit-test-master-key-32-chars!!")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		AdminToken: "admin-test", AdminTokens: []string{"admin-test"}, MetricsToken: "metrics-test",
		BackupDir: filepath.Join(dataDir, "backups"), MaxAdminBodyBytes: 1 << 20,
		AuditRetentionDays: 90, AuditRetentionRows: 100000, ExchangeAllowSecretExport: true,
		OutboundAllowCIDRs: []string{"127.0.0.1/32"},
		RelayMaxBodyBytes:  jsonMB << 20, RelayMaxImageBytes: imageMB << 20,
	}
	server := httptest.NewServer(httpapi.NewTestRouter(t, cfg, db, enc))
	t.Cleanup(server.Close)

	var site struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/sites", map[string]any{
		"name": "limit-site", "base_url": baseURL, "platform": "openai-compatible", "status": "enabled",
	}), &site)
	var cred struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/sites/"+itoa(site.ID)+"/credentials", map[string]any{
		"kind": "api_key", "secret": "sk-limit-test", "status": "enabled",
	}), &cred)
	var channel struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/channels", map[string]any{
		"site_id": site.ID, "credential_id": cred.ID, "name": "limit-ch",
		"base_url": baseURL, "type_hint": "openai-compatible", "status": "enabled",
	}), &channel)
	var route struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/routes", map[string]any{
		"model_pattern": "gemini-2.5-flash", "enabled": true,
	}), &route)
	post(t, server.URL+"/admin/routes/"+itoa(route.ID)+"/members", map[string]any{
		"channel_id": channel.ID, "priority": 1, "weight": 100, "enabled": true,
	})
	var key struct{ Token string }
	json.Unmarshal(post(t, server.URL+"/admin/downstream-keys", map[string]any{
		"name": "limit-key", "scopes": "relay,images",
	}), &key)
	return server.URL, key.Token
}

// relayPost sends any body to any /v1 path with a downstream token.
func relayPost(t *testing.T, serverURL, token, path, body string) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, serverURL+path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw
}

// The ceiling is configuration, and hitting it has to say what the ceiling is:
// a bare "body too large" (HTTP 400, no limit, no endpoint) left the operator
// guessing which of the gateway's limits they hit and how to raise it — and a
// client that retries prints the same opaque line every time.
func TestRelayBodyLimitIsConfiguredAndExplained(t *testing.T) {
	upstream := mockOpenAIUpstream(t)
	defer upstream.Close()
	serverURL, token := setupRelayWithBodyLimits(t, upstream.URL, 1, 2)

	// Just under the JSON limit: the gateway's own ceiling lets it through.
	small := `{"model":"gemini-2.5-flash","messages":[{"role":"user","content":"hi"}]}`
	if status, body := relayPost(t, serverURL, token, "/v1/chat/completions", small); status != http.StatusOK {
		t.Fatalf("small request status %d body %s, want 200", status, body)
	}

	// Over it: 413 (not 400 — the request is understood, it is too big), with
	// the limit and the endpoint named.
	oversized := `{"model":"gemini-2.5-flash","messages":[{"role":"user","content":"` +
		strings.Repeat("x", 1500<<10) + `"}]}`
	status, body := relayPost(t, serverURL, token, "/v1/chat/completions", oversized)
	if status != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized request status %d body %s, want 413", status, body)
	}
	var payload struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Limit   int64  `json:"limit_bytes"`
			Path    string `json:"endpoint"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("unmarshal refusal %s: %v", body, err)
	}
	if payload.Error.Type != "body_too_large" {
		t.Errorf("type = %q, want body_too_large", payload.Error.Type)
	}
	if payload.Error.Limit != 1<<20 {
		t.Errorf("limit_bytes = %d, want the configured %d", payload.Error.Limit, 1<<20)
	}
	if payload.Error.Path != "/v1/chat/completions" {
		t.Errorf("endpoint = %q, want the path that refused", payload.Error.Path)
	}
	for _, want := range []string{"1 MB", "RELAY_MAX_BODY_MB"} {
		if !strings.Contains(payload.Error.Message, want) {
			t.Errorf("message %q does not mention %q", payload.Error.Message, want)
		}
	}

	// The same body on a custom path uses the same ceiling...
	if status, body := relayPost(t, serverURL, token, "/v1/some-custom/path", oversized); status != http.StatusRequestEntityTooLarge {
		t.Fatalf("custom path status %d body %s, want 413", status, body)
	}

	// ...while the image surface has its own, larger one: the same 1.5 MB body
	// is accepted there, so a single "raise the limit" knob cannot quietly cut
	// image uploads in half.
	status, body = relayPost(t, serverURL, token, "/v1/images/generations", `{"model":"gemini-2.5-flash","prompt":"`+strings.Repeat("x", 1500<<10)+`"}`)
	if status == http.StatusRequestEntityTooLarge {
		t.Fatalf("image request status 413 body %s, want it accepted under its own limit", body)
	}
	if status != http.StatusOK {
		t.Fatalf("image request status %d body %s, want 200 under its own limit", status, body)
	}
}

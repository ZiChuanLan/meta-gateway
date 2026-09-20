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

	"github.com/lan/meta-gateway/internal/config"
	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/httpapi"
	"github.com/lan/meta-gateway/internal/store"
)

// imageUpstream records what the gateway actually sent upstream: path,
// content-type and body. That is the whole point of the capability registry,
// so the assertions are about the wire contract rather than the payload.
type imageUpstream struct {
	path        string
	contentType string
	body        []byte
}

func newImageUpstream(t *testing.T, captured *imageUpstream) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		captured.path = r.URL.Path
		captured.contentType = r.Header.Get("Content-Type")
		captured.body = body
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/images/edits", "/v1/images/generations":
			_, _ = io.WriteString(w, `{"created":1,"data":[{"b64_json":"QUFBQQ==","revised_prompt":"a purple cube"}],"usage":{"prompt_tokens":10,"completion_tokens":0,"total_tokens":10}}`)
		default:
			_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"done ![cube](data:image/png;base64,QUFBQQ==)"}}],"usage":{"prompt_tokens":5,"completion_tokens":1,"total_tokens":6}}`)
		}
	}))
}

// Returns (server URL, downstream key token, route id).
func setupImageRelay(t *testing.T, baseURL, model string) (string, string, int64) {
	t.Helper()
	serverURL, token, routeID, _ := setupImageRelayWithStore(t, baseURL, model)
	return serverURL, token, routeID
}

func setupImageRelayWithStore(t *testing.T, baseURL, model string) (string, string, int64, *store.DB) {
	t.Helper()
	dataDir := t.TempDir()
	db, err := store.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	enc, err := crypto.New("image-test-master-key-at-least-32-characters!!")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		AdminToken: "admin-test", AdminTokens: []string{"admin-test"}, MetricsToken: "metrics-test",
		BackupDir: filepath.Join(dataDir, "backups"), MaxAdminBodyBytes: 8 << 20,
		AuditRetentionDays: 90, AuditRetentionRows: 100000,
		OutboundAllowCIDRs: []string{"127.0.0.1/32"},
	}
	server := httptest.NewServer(httpapi.NewTestRouter(t, cfg, db, enc))
	t.Cleanup(server.Close)

	var site struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/sites", map[string]any{
		"name": "img-site", "base_url": baseURL, "platform": "openai", "status": "enabled",
	}), &site)
	var cred struct{ ID int64 }
	json.Unmarshal(post(t, fmt.Sprintf("%s/admin/sites/%d/credentials", server.URL, site.ID), map[string]any{
		"kind": "api_key", "secret": "test-image-key-abcdefgh", "status": "enabled",
	}), &cred)
	var channel struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/channels", map[string]any{
		"site_id": site.ID, "credential_id": cred.ID, "name": "img-ch",
		"base_url": baseURL, "type_hint": "openai-compatible", "status": "enabled",
	}), &channel)
	var route struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/routes", map[string]any{
		"model_pattern": model, "enabled": true,
	}), &route)
	post(t, fmt.Sprintf("%s/admin/routes/%d/members", server.URL, route.ID), map[string]any{
		"channel_id": channel.ID, "priority": 1, "weight": 100, "enabled": true,
	})
	var key struct{ Token string }
	json.Unmarshal(post(t, server.URL+"/admin/downstream-keys", map[string]any{
		"name": "img-key", "scopes": "relay",
	}), &key)
	return server.URL, key.Token, route.ID, db
}

func TestTryImageRoutesByCapability(t *testing.T) {
	cases := []struct {
		model      string
		mode       string
		wantPath   string
		wantMultip bool
	}{
		// grok's editor is JSON-only: multipart gets a 415 upstream.
		{"grok-imagine-image-edit", "edit", "/v1/images/edits", false},
		// gpt-image advertises both encodings, so multipart wins (it is the
		// documented contract for file uploads).
		{"gpt-image-2", "edit", "/v1/images/edits", true},
		{"gpt-image-2", "generate", "/v1/images/generations", false},
		{"dall-e-3", "generate", "/v1/images/generations", false},
		// Gemini image models never leave the chat protocol.
		{"gemini-2.5-flash-image", "edit", "/v1/chat/completions", false},
	}
	for _, tc := range cases {
		captured := &imageUpstream{}
		upstream := newImageUpstream(t, captured)
		serverURL, _, _ := setupImageRelay(t, upstream.URL, tc.model)

		request := map[string]any{
			"model":  tc.model,
			"prompt": "make the cube purple",
			"mode":   tc.mode,
		}
		if tc.mode == "edit" {
			request["images"] = []map[string]any{
				{"data_url": "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8AAAwAB/AF+msuWAAAAAElFTkSuQmCC"},
			}
		}
		body := post(t, serverURL+"/admin/try/image", request)
		var out struct {
			Status int `json:"status"`
			Plan   struct {
				Endpoint         string `json:"endpoint"`
				Format           string `json:"format"`
				UsesChatProtocol bool   `json:"uses_chat_protocol"`
			} `json:"plan"`
			Images []struct {
				DataURL string `json:"data_url"`
			} `json:"images"`
		}
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("%s: %v (%s)", tc.model, err, body)
		}
		if captured.path != tc.wantPath {
			t.Errorf("%s: upstream path = %s, want %s", tc.model, captured.path, tc.wantPath)
		}
		isMultipart := len(captured.contentType) > 19 && captured.contentType[:19] == "multipart/form-data"
		if isMultipart != tc.wantMultip {
			t.Errorf("%s: multipart = %v (%s), want %v", tc.model, isMultipart, captured.contentType, tc.wantMultip)
		}
		if len(out.Images) != 1 {
			t.Errorf("%s: extracted %d images", tc.model, len(out.Images))
		} else if out.Images[0].DataURL != "data:image/png;base64,QUFBQQ==" {
			t.Errorf("%s: image = %q", tc.model, out.Images[0].DataURL)
		}
		upstream.Close()
	}
}

// An explicit format override is the escape hatch for a mislabelled upstream.
func TestTryImageFormatOverride(t *testing.T) {
	captured := &imageUpstream{}
	upstream := newImageUpstream(t, captured)
	defer upstream.Close()
	serverURL, _, _ := setupImageRelay(t, upstream.URL, "gpt-image-2")

	post(t, serverURL+"/admin/try/image", map[string]any{
		"model":  "gpt-image-2",
		"prompt": "a cube",
		"mode":   "edit",
		"format": "json",
		"images": []map[string]any{
			{"data_url": "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8AAAwAB/AF+msuWAAAAAElFTkSuQmCC"},
		},
	})
	if captured.contentType != "application/json" {
		t.Fatalf("override ignored: content-type = %q", captured.contentType)
	}
	var sent map[string]any
	if err := json.Unmarshal(captured.body, &sent); err != nil {
		t.Fatalf("body was not JSON: %v", err)
	}
	if _, ok := sent["image"]; !ok {
		t.Errorf("json body missing image: %v", sent)
	}
}

func TestTryImageRejectsNonImageModel(t *testing.T) {
	captured := &imageUpstream{}
	upstream := newImageUpstream(t, captured)
	defer upstream.Close()
	serverURL, _, _ := setupImageRelay(t, upstream.URL, "gpt-4o")

	encoded, _ := json.Marshal(map[string]any{"model": "gpt-4o", "prompt": "hello"})
	req, _ := http.NewRequest(http.MethodPost, serverURL+"/admin/try/image",
		bytes.NewReader(encoded))
	req.Header.Set("Authorization", "Bearer admin-test")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode)
	}
}

func TestTryImageAutoGeneratesJSONWithoutDuplicatingImagePayload(t *testing.T) {
	captured := &imageUpstream{}
	upstream := newImageUpstream(t, captured)
	defer upstream.Close()
	serverURL, _, _ := setupImageRelay(t, upstream.URL, "gpt-image-2")
	for _, includeRaw := range []bool{false, true} {
		body := post(t, serverURL+"/admin/try/image", map[string]any{
			"model": "gpt-image-2", "prompt": "a cube", "mode": "auto", "include_raw_response": includeRaw,
		})
		if captured.path != "/v1/images/generations" || captured.contentType != "application/json" {
			t.Fatalf("auto generation: path=%s type=%s", captured.path, captured.contentType)
		}
		var sent, result map[string]json.RawMessage
		if err := json.Unmarshal(captured.body, &sent); err != nil {
			t.Fatal(err)
		}
		if _, found := sent["response_format"]; found {
			t.Fatal("injected a legacy response_format into GPT-Image")
		}
		if err := json.Unmarshal(body, &result); err != nil {
			t.Fatal(err)
		}
		if _, found := result["body"]; found != includeRaw {
			t.Fatalf("raw response included=%v want=%v", found, includeRaw)
		}
	}
}

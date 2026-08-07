package httpapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
)

// connectionResponse mirrors the POST /admin/connections payload.
type connectionResponse struct {
	Channel          domain.Channel `json:"channel"`
	Site             domain.Site    `json:"site"`
	CredentialID     int64          `json:"credential_id"`
	ReusedSite       bool           `json:"reused_site"`
	HasSecret        bool           `json:"has_secret"`
	Platform         string         `json:"platform"`
	DetectionMatched bool           `json:"detection_matched"`
}

func adminJSONBody(t *testing.T, base, token, method, path string, body any) (int, []byte) {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal admin body: %v", err)
	}
	req, err := http.NewRequest(method, base+path, bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("create admin request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("admin request: %v", err)
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read admin response: %v", err)
	}
	return resp.StatusCode, payload
}

func postConnection(t *testing.T, base, token string, body map[string]any) (int, connectionResponse) {
	t.Helper()
	status, payload := adminJSONBody(t, base, token, http.MethodPost, "/admin/connections", body)
	var out connectionResponse
	if status < 400 {
		if err := json.Unmarshal(payload, &out); err != nil {
			t.Fatalf("decode connection response: %v", err)
		}
	}
	return status, out
}

func TestConnectionCreateHappyPath(t *testing.T) {
	base, _, _ := setupServer(t, "http://127.0.0.1:1")

	status, conn := postConnection(t, base, "admin-secret", map[string]any{
		"name":      "demo",
		"base_url":  "https://api.example.com",
		"secret":    "sk-live",
		"type_hint": "openai-compatible",
	})
	if status != http.StatusCreated {
		t.Fatalf("status = %d, want 201", status)
	}
	if conn.Channel.ID == 0 || conn.CredentialID == 0 || conn.Site.ID == 0 {
		t.Fatalf("expected channel, credential and site ids, got %+v", conn)
	}
	if !conn.HasSecret {
		t.Fatal("expected has_secret true")
	}
	if conn.ReusedSite {
		t.Fatal("expected reused_site false on first create")
	}
	if conn.Channel.SiteID == nil || *conn.Channel.SiteID != conn.Site.ID {
		t.Fatalf("channel not linked to site: %+v", conn.Channel)
	}
	if conn.Channel.CredentialID == nil || *conn.Channel.CredentialID != conn.CredentialID {
		t.Fatalf("channel not linked to credential: %+v", conn.Channel)
	}
	if conn.Platform == "" {
		t.Fatal("expected platform to be populated")
	}
}

func TestConnectionCreateReusesSiteByNormalizedURL(t *testing.T) {
	base, _, _ := setupServer(t, "http://127.0.0.1:1")

	_, first := postConnection(t, base, "admin-secret", map[string]any{
		"base_url": "https://api.example.com",
		"secret":   "sk-one",
	})
	status, second := postConnection(t, base, "admin-secret", map[string]any{
		"base_url": "https://api.example.com/", // trailing slash should normalize
		"secret":   "sk-two",
	})
	if status != http.StatusCreated {
		t.Fatalf("status = %d, want 201", status)
	}
	if !second.ReusedSite {
		t.Fatal("expected site reuse for normalized URL")
	}
	if second.Site.ID != first.Site.ID {
		t.Fatalf("expected same site id, got %d vs %d", second.Site.ID, first.Site.ID)
	}
}

func TestConnectionCreateValidation(t *testing.T) {
	base, _, _ := setupServer(t, "http://127.0.0.1:1")

	cases := []map[string]any{
		{"base_url": "", "secret": "sk"},
		{"base_url": "https://api.example.com", "secret": ""},
		{"base_url": "ftp://example.com", "secret": "sk"},
	}
	for i, body := range cases {
		if status, _ := postConnection(t, base, "admin-secret", body); status != http.StatusBadRequest {
			t.Fatalf("case %d: status = %d, want 400", i, status)
		}
	}
}

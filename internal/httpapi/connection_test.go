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
		"name":       "demo",
		"base_url":   "https://api.example.com",
		"secret":     "sk-live",
		"type_hint":  "openai-compatible",
		"models_csv": "gpt-4o,gpt-4o-mini",
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
	if conn.Channel.ModelsCSV != "gpt-4o,gpt-4o-mini" {
		t.Fatalf("models_csv = %q", conn.Channel.ModelsCSV)
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

// A pasted complete endpoint is the new-api Custom habit: the operator supplies
// one URL and expects it to work. Splitting it at create time is what keeps the
// relay from building `/v1/systemone/v1/chat/completions`, and the site must
// store the ROOT so a second connection to the same upstream still reuses it.
func TestConnectionCreateSplitsEndpointBaseURL(t *testing.T) {
	base, _, _ := setupServer(t, "http://127.0.0.1:1")

	status, conn := postConnection(t, base, "admin-secret", map[string]any{
		"name":      "typesafe",
		"base_url":  "https://api.typesafe.ai/v1/systemone",
		"secret":    "sk-live",
		"type_hint": "openai-compatible",
	})
	if status != http.StatusCreated {
		t.Fatalf("status = %d, want 201", status)
	}
	// A connection-created channel inherits its base URL from the site, so the
	// split has to happen before the site is created/reused — that is exactly
	// what makes the pasted endpoint land as an override on the channel.
	if conn.Site.BaseURL != "https://api.typesafe.ai" {
		t.Fatalf("site base_url = %q, want the root", conn.Site.BaseURL)
	}
	if conn.Channel.UpstreamPathOverride != "/v1/systemone" {
		t.Fatalf("upstream_path_override = %q, want /v1/systemone", conn.Channel.UpstreamPathOverride)
	}

	// The same upstream pasted again must reuse the site rather than creating a
	// second one for the same host.
	_, second := postConnection(t, base, "admin-secret", map[string]any{
		"name":      "typesafe-2",
		"base_url":  "https://api.typesafe.ai/v1/systemone",
		"secret":    "sk-live-2",
		"type_hint": "openai-compatible",
	})
	if !second.ReusedSite || second.Site.ID != conn.Site.ID {
		t.Fatalf("expected site reuse, got %+v", second)
	}

	// A version-root base URL is not an endpoint: nothing may be split off.
	_, ark := postConnection(t, base, "admin-secret", map[string]any{
		"name":      "ark",
		"base_url":  "https://ark.cn-beijing.volces.com/api/v3",
		"secret":    "sk-ark",
		"type_hint": "openai-compatible",
	})
	if ark.Site.BaseURL != "https://ark.cn-beijing.volces.com/api/v3" || ark.Channel.UpstreamPathOverride != "" {
		t.Fatalf("version-root base URL was split: site=%q override=%q", ark.Site.BaseURL, ark.Channel.UpstreamPathOverride)
	}
}

// Picking "TypeSafe" as the provider must be enough: the connection comes out
// already carrying the endpoint and field mapping that provider's wire contract
// needs, so the operator never writes a mapping by hand and never has to know
// that `questions` is a map. The documented URL the console pre-fills is the
// pasted-endpoint form, so both entry shapes are asserted here.
func TestConnectionCreateAppliesTheProviderProfile(t *testing.T) {
	base, _, _ := setupServer(t, "http://127.0.0.1:1")

	for _, baseURL := range []string{
		"https://api.typesafe.ai",
		"https://api.typesafe.ai/v1/systemone",
	} {
		status, conn := postConnection(t, base, "admin-secret", map[string]any{
			"name":      "typesafe",
			"base_url":  baseURL,
			"secret":    "sk-live",
			"type_hint": "typesafe",
		})
		if status != http.StatusCreated {
			t.Fatalf("%s: status = %d, want 201", baseURL, status)
		}
		if conn.Channel.UpstreamPathOverride != "v1/systemone" {
			t.Fatalf("%s: override = %q, want v1/systemone", baseURL, conn.Channel.UpstreamPathOverride)
		}
		if conn.Channel.UpstreamRequestMap == "" || conn.Channel.UpstreamResponseMap == "" {
			t.Fatalf("%s: provider profile did not fill the field maps", baseURL)
		}
	}

	// A plain OpenAI-compatible channel must stay mapping-free: the profile is
	// keyed on the provider, not on the shape of the URL.
	_, plain := postConnection(t, base, "admin-secret", map[string]any{
		"name":      "plain",
		"base_url":  "https://api.example.com/v1",
		"secret":    "sk-plain",
		"type_hint": "openai-compatible",
	})
	if plain.Channel.UpstreamRequestMap != "" || plain.Channel.UpstreamPathOverride != "" {
		t.Fatalf("openai-compatible channel got a mapping: %+v", plain.Channel)
	}
}

package httpapi_test

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

// The endpoint preview is the guard against a whole class of silent breakage:
// a provider preset whose base URL produced the wrong upstream path. It must
// report what the relay would really call, including the case where the base
// carries its own endpoint.
func TestEndpointPreviewReportsResolvedURL(t *testing.T) {
	base, _, _ := setupServer(t, "http://127.0.0.1:1")

	cases := []struct {
		url        string
		wantChat   string
		wantModels string
	}{
		{"https://open.bigmodel.cn/api/paas/v4", "https://open.bigmodel.cn/api/paas/v4/chat/completions", "https://open.bigmodel.cn/api/paas/v4/models"},
		{"https://ark.cn-beijing.volces.com/api/v3", "https://ark.cn-beijing.volces.com/api/v3/chat/completions", "https://ark.cn-beijing.volces.com/api/v3/models"},
		{"https://api.deepseek.com/v1", "https://api.deepseek.com/v1/chat/completions", "https://api.deepseek.com/v1/models"},
		{"https://api.example.com", "https://api.example.com/v1/chat/completions", "https://api.example.com/v1/models"},
		// Perplexity documents a base with no /v1, so its preset supplies the
		// endpoint; the preview must report the endpoint itself, not a /v1 join.
		{"https://api.perplexity.ai/chat/completions", "https://api.perplexity.ai/chat/completions", "https://api.perplexity.ai/chat/completions/models"},
	}
	for _, tc := range cases {
		body := adminGet(t, base, "admin-secret", "/admin/endpoint-preview?url="+tc.url)
		var got map[string]any
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("decode preview: %v", err)
		}
		if got["chat_url"] != tc.wantChat {
			t.Errorf("%s chat_url = %v, want %s", tc.url, got["chat_url"], tc.wantChat)
		}
		if got["models_url"] != tc.wantModels {
			t.Errorf("%s models_url = %v, want %s", tc.url, got["models_url"], tc.wantModels)
		}
	}

	// A base that carries a complete endpoint reports the SPLIT result, because
	// that is what the relay builds after the save-time split.
	body := adminGet(t, base, "admin-secret", "/admin/endpoint-preview?url=https://api.typesafe.ai/v1/systemone")
	var split map[string]any
	if err := json.Unmarshal(body, &split); err != nil {
		t.Fatal(err)
	}
	if split["base_url"] != "https://api.typesafe.ai" || split["endpoint_override"] != "/v1/systemone" {
		t.Fatalf("split preview = %v", split)
	}
	if split["chat_url"] != "https://api.typesafe.ai/v1/systemone" {
		t.Fatalf("chat_url = %v, want the endpoint itself", split["chat_url"])
	}

	status, _ := adminJSONBody(t, base, "admin-secret", http.MethodGet, "/admin/endpoint-preview?url=", nil)
	if status != http.StatusBadRequest {
		t.Fatalf("missing url status = %d, want 400", status)
	}
}

// adminGet fetches an admin endpoint with an explicit token.
func adminGet(t *testing.T, base, token, path string) []byte {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, base+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode >= 300 {
		t.Fatalf("GET %s: %d %s", path, resp.StatusCode, payload)
	}
	return payload
}

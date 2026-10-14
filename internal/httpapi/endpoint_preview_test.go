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
		// A mount prefix is not an endpoint: the /v1 root still goes after it, and
		// the preview is what shows that rule to the operator.
		{"https://proxy.example.com/prefix", "https://proxy.example.com/prefix/v1/chat/completions", "https://proxy.example.com/prefix/v1/models"},
		// Perplexity documents a base with no /v1, so its preset supplies the
		// endpoint; the preview must report the endpoint itself, not a /v1 join.
		// The model list stays at the root, because that is where discovery looks
		// — `…/chat/completions/models` is not a URL any provider serves.
		{"https://api.perplexity.ai/chat/completions", "https://api.perplexity.ai/chat/completions", "https://api.perplexity.ai/v1/models"},
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
	if split["models_url"] != "https://api.typesafe.ai/v1/models" {
		t.Fatalf("models_url = %v, want the root's model list", split["models_url"])
	}

	status, _ := adminJSONBody(t, base, "admin-secret", http.MethodGet, "/admin/endpoint-preview?url=", nil)
	if status != http.StatusBadRequest {
		t.Fatalf("missing url status = %d, want 400", status)
	}
}

// The upstream can speak one of three wire shapes, and the dialog has to say
// which one it will be: the same base URL joins differently per type, so a
// type-blind preview promises an endpoint the relay never calls.
func TestEndpointPreviewSpeaksTheChannelsProtocol(t *testing.T) {
	base, _, _ := setupServer(t, "http://127.0.0.1:1")

	cases := []struct {
		name       string
		url        string
		typeHint   string
		wantProto  string
		wantChat   string
		wantModels string
	}{
		{
			name: "anthropic under a vendor mount prefix", url: "https://api.deepseek.com/anthropic",
			typeHint: "anthropic", wantProto: "anthropic",
			wantChat:   "https://api.deepseek.com/anthropic/v1/messages",
			wantModels: "https://api.deepseek.com/anthropic/v1/models",
		},
		{
			name: "anthropic official root", url: "https://api.anthropic.com",
			typeHint: "anthropic", wantProto: "anthropic",
			wantChat:   "https://api.anthropic.com/v1/messages",
			wantModels: "https://api.anthropic.com/v1/models",
		},
		{
			name: "a pasted anthropic endpoint does not double the version", url: "https://api.anthropic.com/v1/messages",
			typeHint: "anthropic", wantProto: "anthropic",
			wantChat:   "https://api.anthropic.com/v1/messages",
			wantModels: "https://api.anthropic.com/v1/models",
		},
		{
			name: "openai compatible", url: "https://api.example.com",
			typeHint: "openai-compatible", wantProto: "openai",
			wantChat:   "https://api.example.com/v1/chat/completions",
			wantModels: "https://api.example.com/v1/models",
		},
		{
			// A vendor brand still speaks OpenAI; only the base URL differs.
			name: "deepseek brand", url: "https://api.deepseek.com",
			typeHint: "deepseek", wantProto: "openai",
			wantChat:   "https://api.deepseek.com/v1/chat/completions",
			wantModels: "https://api.deepseek.com/v1/models",
		},
		{
			name: "gemini rides its own version root and addresses the model", url: "https://generativelanguage.googleapis.com/v1beta",
			typeHint: "gemini", wantProto: "gemini",
			wantChat:   "https://generativelanguage.googleapis.com/v1beta/models/{model}:generateContent",
			wantModels: "https://generativelanguage.googleapis.com/v1beta/models",
		},
		{
			// TypeSafe's endpoint comes from the provider profile, which is applied
			// at save time — a root-only base must not preview `/v1/chat/completions`.
			name: "provider profile owns the endpoint", url: "https://api.typesafe.ai",
			typeHint: "typesafe", wantProto: "openai",
			wantChat:   "https://api.typesafe.ai/v1/systemone",
			wantModels: "https://api.typesafe.ai/v1/models",
		},
		{
			name: "an unknown type falls back to the passthrough protocol", url: "https://api.example.com",
			typeHint: "not-a-real-type", wantProto: "openai",
			wantChat:   "https://api.example.com/v1/chat/completions",
			wantModels: "https://api.example.com/v1/models",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := "/admin/endpoint-preview?url=" + tc.url + "&type=" + tc.typeHint
			var got map[string]any
			if err := json.Unmarshal(adminGet(t, base, "admin-secret", path), &got); err != nil {
				t.Fatalf("decode preview: %v", err)
			}
			if got["protocol"] != tc.wantProto {
				t.Errorf("%s protocol = %v, want %s", tc.url, got["protocol"], tc.wantProto)
			}
			if got["chat_url"] != tc.wantChat {
				t.Errorf("%s chat_url = %v, want %s", tc.url, got["chat_url"], tc.wantChat)
			}
			if got["models_url"] != tc.wantModels {
				t.Errorf("%s models_url = %v, want %s", tc.url, got["models_url"], tc.wantModels)
			}
		})
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

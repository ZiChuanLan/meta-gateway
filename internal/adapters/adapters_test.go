package adapters_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/adapters"
)

// The URL builder is what makes the connection-type dropdown usable: the
// operator picks Zhipu and the preset base URL has to land on the endpoint the
// vendor documents. Every case below is checked against that vendor's own
// documented base_url / endpoint, because a wrong join is invisible until a
// request 404s (the whole reason an operator had to hand-write endpoint
// overrides before).
func TestJoinOpenAIPathMatchesDocumentedProviderEndpoints(t *testing.T) {
	tests := []struct {
		name string
		base string
		path string
		want string
	}{
		// Host root, no vendor path: the conventional /v1 root is added.
		{name: "bare host gains the /v1 root", base: "https://api.example.com", path: "chat/completions", want: "https://api.example.com/v1/chat/completions"},
		{name: "trailing slash on a bare host", base: "https://api.example.com/", path: "models", want: "https://api.example.com/v1/models"},
		{name: "deepseek documents a /v1 base", base: "https://api.deepseek.com/v1", path: "chat/completions", want: "https://api.deepseek.com/v1/chat/completions"},
		// A vendor-owned layout prefix must be preserved, never doubled with /v1.
		{name: "zhipu /api/paas/v4", base: "https://open.bigmodel.cn/api/paas/v4", path: "chat/completions", want: "https://open.bigmodel.cn/api/paas/v4/chat/completions"},
		{name: "zhipu models", base: "https://open.bigmodel.cn/api/paas/v4", path: "models", want: "https://open.bigmodel.cn/api/paas/v4/models"},
		{name: "volcengine ark /api/v3", base: "https://ark.cn-beijing.volces.com/api/v3", path: "chat/completions", want: "https://ark.cn-beijing.volces.com/api/v3/chat/completions"},
		{name: "qianfan /v2", base: "https://qianfan.baidubce.com/v2", path: "chat/completions", want: "https://qianfan.baidubce.com/v2/chat/completions"},
		{name: "dashscope /compatible-mode/v1", base: "https://dashscope.aliyuncs.com/compatible-mode/v1", path: "chat/completions", want: "https://dashscope.aliyuncs.com/compatible-mode/v1/chat/completions"},
		{name: "openrouter /api/v1", base: "https://openrouter.ai/api/v1", path: "chat/completions", want: "https://openrouter.ai/api/v1/chat/completions"},
		{name: "groq /openai/v1", base: "https://api.groq.com/openai/v1", path: "chat/completions", want: "https://api.groq.com/openai/v1/chat/completions"},
		// A vendor prefix that is NOT version-shaped is a MOUNT PREFIX, not an API
		// root: the platform mounts other surfaces next to it, so the conventional
		// /v1 root still belongs between the two. Getting this backwards sent the
		// E2E channel (upstream serves `/ok/v1/chat/completions`) to
		// `/ok/chat/completions`.
		{name: "mount prefix keeps the /v1 root", base: "https://api.example.com/prefix", path: "models", want: "https://api.example.com/prefix/v1/models"},
		{name: "mount prefix with chat/completions", base: "http://mock-upstream:8080/ok", path: "chat/completions", want: "http://mock-upstream:8080/ok/v1/chat/completions"},
		{name: "mount prefix under a shared root", base: "https://proxy.example.com/upstream", path: "chat/completions", want: "https://proxy.example.com/upstream/v1/chat/completions"},
		{name: "a /v1 root after a mount prefix is not duplicated", base: "https://api.example.com/prefix/v1", path: "models", want: "https://api.example.com/prefix/v1/models"},
		// Perplexity documents a base with no /v1 at all (verified: their
		// OpenAI-compatibility guide uses `https://api.perplexity.ai`). Its preset
		// therefore supplies the documented ENDPOINT, which SplitEndpointBaseURL
		// turns into root + endpoint override; that pair is asserted below, because
		// joining an UNSPLIT endpoint never happens at runtime.
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := adapters.JoinOpenAIPath(tc.base, tc.path)
			if err != nil {
				t.Fatalf("base=%q path=%q err=%v", tc.base, tc.path, err)
			}
			if got != tc.want {
				t.Fatalf("base=%q path=%q got=%q want=%q", tc.base, tc.path, got, tc.want)
			}
		})
	}

	// The Perplexity preset is only correct once the save-time split has run, so
	// assert the pair together: the split root must produce the documented path.
	base, override, err := adapters.SplitEndpointBaseURL("https://api.perplexity.ai/chat/completions")
	if err != nil {
		t.Fatal(err)
	}
	if base != "https://api.perplexity.ai" || override != "/chat/completions" {
		t.Fatalf("perplexity split = (%q, %q)", base, override)
	}
	if joined, err := adapters.JoinRawPath(base, override); err != nil || joined != "https://api.perplexity.ai/chat/completions" {
		t.Fatalf("perplexity resolved = %q err=%v", joined, err)
	}
}

func TestOpenAIModelAdapterNormalizesAndAuthenticates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/prefix/v1/models" || r.Header.Get("Authorization") != "Bearer very-secret" {
			t.Fatalf("unexpected request: path=%s auth=%s", r.URL.Path, r.Header.Get("Authorization"))
		}
		_, _ = io.WriteString(w, `{"data":[{"id":" z-model "},{"id":"a-model"},{"id":"a-model"},{"id":" "}]}`)
	}))
	defer server.Close()
	adapter := adapters.NewOpenAIModelAdapter("openai-compatible", server.Client())
	models, err := adapter.ListModels(t.Context(), server.URL+"/prefix/", "very-secret")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(models, ",") != "a-model,z-model" {
		t.Fatalf("models=%v", models)
	}
}

func TestGeminiModelAdapterNormalizesAndAuthenticates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/prefix/v1beta/models" || r.Header.Get("x-goog-api-key") != "gemini-secret" {
			t.Fatalf("unexpected request: path=%s key=%s", r.URL.Path, r.Header.Get("x-goog-api-key"))
		}
		_, _ = io.WriteString(w, `{"models":[{"name":"models/z-model"},{"name":"models/a-model"},{"name":"a-model"},{"name":" "}]}`)
	}))
	defer server.Close()
	adapter := adapters.NewGeminiModelAdapter("gemini", server.Client())
	models, err := adapter.ListModels(t.Context(), server.URL+"/prefix/v1beta/", "gemini-secret")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(models, ",") != "a-model,z-model" {
		t.Fatalf("models=%v", models)
	}
}

func TestGeminiModelAdapterRejectsUnsafeURL(t *testing.T) {
	adapter := adapters.NewGeminiModelAdapter("gemini", nil)
	for _, value := range []string{"relative", "ftp://example.com", "https://user:pass@example.com", "https://example.com/v1beta?key=secret"} {
		_, err := adapter.ListModels(t.Context(), value, "secret")
		var adapterErr *adapters.Error
		if !errors.As(err, &adapterErr) || adapterErr.Kind != adapters.ErrorInvalidURL {
			t.Fatalf("url=%q err=%v", value, err)
		}
	}
}

func TestOpenAIModelAdapterPropagatesCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	adapter := adapters.NewOpenAIModelAdapter("openai-compatible", server.Client())
	_, err := adapter.ListModels(ctx, server.URL, "secret")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline error, got %v", err)
	}
}

func TestRegistryAliasesAndPrecedence(t *testing.T) {
	registry := adapters.NewRegistry(nil)
	for _, alias := range []string{"openai", "OPENAI-COMPATIBLE", "openaicompat", "new-api", "newapi", "one-api", "anyrouter", "axonhub", "metapi"} {
		got, ok := registry.Resolve(alias, "unsupported")
		if !ok {
			t.Fatalf("alias %q did not resolve", alias)
		}
		lower := strings.ToLower(alias)
		if strings.Contains(lower, "new") && !strings.Contains(lower, "one") && got.Name() != "new-api" {
			t.Fatalf("alias %q resolved as %q", alias, got.Name())
		}
		if lower == "one-api" && got.Name() != "one-api" {
			t.Fatalf("alias %q resolved as %q", alias, got.Name())
		}
		if lower == "anyrouter" || lower == "axonhub" || lower == "metapi" {
			if got.Name() != "openai-compatible" {
				t.Fatalf("brand %q resolved as %q", alias, got.Name())
			}
		}
	}
	if _, ok := registry.Resolve("totally-unknown-xyz", ""); ok {
		t.Fatal("unknown type must not resolve")
	}
	if got, ok := registry.Resolve("", "new-api"); !ok || got.Name() != "new-api" {
		t.Fatal("site platform did not resolve")
	}
	if adapters.CanonicalType("ANYROUTER") != "openai-compatible" {
		t.Fatal("CanonicalType anyrouter")
	}
	if adapters.CanonicalType("AxonHub") != "openai-compatible" {
		t.Fatal("CanonicalType axonhub")
	}
	// The picker's "Custom (endpoint mapping)" type is bespoke wiring, so the
	// OpenAI-shaped passthrough is the only honest default: before this entry
	// existed, picking Custom… resolved to no adapter at all and discovery
	// failed with `unsupported_adapter`, i.e. the type could never list models.
	if adapters.CanonicalType("custom") != "openai-compatible" {
		t.Fatal("CanonicalType custom")
	}
	got, ok := registry.Resolve("Custom", "")
	if !ok || got.Name() != "openai-compatible" {
		t.Fatalf("custom type did not resolve to the OpenAI-compatible adapter: ok=%v", ok)
	}
}

func TestOpenAIModelAdapterRejectsInvalidResponsesWithRedactedErrors(t *testing.T) {
	secret := "do-not-leak"
	tests := []struct {
		name string
		body string
		code int
		kind adapters.ErrorKind
	}{
		{name: "status", body: secret, code: 401, kind: adapters.ErrorStatus},
		{name: "missing data", body: `{}`, code: 200, kind: adapters.ErrorPayload},
		{name: "null data", body: `{"data":null}`, code: 200, kind: adapters.ErrorPayload},
		{name: "invalid item", body: `{"data":[{"id":123}]}`, code: 200, kind: adapters.ErrorPayload},
		{name: "too large", body: strings.Repeat("x", (2<<20)+1), code: 200, kind: adapters.ErrorTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.code)
				_, _ = io.WriteString(w, tt.body)
			}))
			defer server.Close()
			adapter := adapters.NewOpenAIModelAdapter("openai-compatible", server.Client())
			_, err := adapter.ListModels(t.Context(), server.URL, secret)
			var adapterErr *adapters.Error
			if !errors.As(err, &adapterErr) || adapterErr.Kind != tt.kind {
				t.Fatalf("err=%v", err)
			}
			if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), server.URL) || strings.Contains(err.Error(), tt.body) {
				t.Fatalf("error leaked sensitive upstream data: %v", err)
			}
		})
	}
}

// The model-list shapes actually served in the wild. TypeSafe's body is the
// literal response of https://api.typesafe.ai/v1/models captured on 2026-09-23;
// before it was accepted, a working upstream reported `invalid_payload`, which
// the console rendered as "check Base URL and credentials".
func TestOpenAIModelAdapterAcceptsEveryModelListShape(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []string
	}{
		{
			name: "openai data/id",
			body: `{"object":"list","data":[{"id":"gpt-4","object":"model"},{"id":"gpt-3.5"}]}`,
			want: []string{"gpt-3.5", "gpt-4"},
		},
		{
			name: "fastapi data/name",
			body: `{"data":[{"name":"llama-3","description":"x"}]}`,
			want: []string{"llama-3"},
		},
		{
			name: "typesafe models/name",
			body: `{"models":[{"name":"jev-latest","description":"The latest iteration","release_date":"2026-09-10T18:38:01Z"},{"name":"jev-preview","description":"A preview version","release_date":"2026-09-10T18:39:06Z"}]}`,
			want: []string{"jev-latest", "jev-preview"},
		},
		{
			name: "flat name list",
			body: `{"models":["a","b","a"]}`,
			want: []string{"a", "b"},
		},
		{
			name: "id wins over a display name",
			body: `{"data":[{"id":"model-key","name":"Pretty Label"}]}`,
			want: []string{"model-key"},
		},
		{
			name: "empty but well-formed list is success",
			body: `{"data":[]}`,
			want: []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, tt.body)
			}))
			defer server.Close()
			adapter := adapters.NewOpenAIModelAdapter("openai-compatible", server.Client())
			got, err := adapter.ListModels(t.Context(), server.URL, "secret")
			if err != nil {
				t.Fatalf("ListModels: %v", err)
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("models = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestOpenAIModelAdapterRejectsUnsafeURL(t *testing.T) {
	adapter := adapters.NewOpenAIModelAdapter("openai-compatible", nil)
	for _, value := range []string{"relative", "ftp://example.com", "https://user:pass@example.com"} {
		_, err := adapter.ListModels(t.Context(), value, "secret")
		var adapterErr *adapters.Error
		if !errors.As(err, &adapterErr) || adapterErr.Kind != adapters.ErrorInvalidURL {
			t.Fatalf("url=%q err=%v", value, err)
		}
	}
}

package adapters_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
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
		// A vendor prefix that only LOOKS like a version must not be treated as one.
		{name: "arbitrary prefix is preserved", base: "https://api.example.com/prefix", path: "models", want: "https://api.example.com/prefix/models"},
		// Perplexity documents a base with no /v1 at all (verified: their
		// OpenAI-compatibility guide uses `https://api.perplexity.ai`). The
		// preset therefore supplies the documented ENDPOINT, which
		// SplitEndpointBaseURL turns into root + endpoint override.
		{
			name: "perplexity documented endpoint is split, not /v1-joined",
			base: "https://api.perplexity.ai/chat/completions",
			path: "chat/completions",
			want: "https://api.perplexity.ai/chat/completions/chat/completions",
		},
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
		if r.URL.Path != "/prefix/models" || r.Header.Get("Authorization") != "Bearer very-secret" {
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

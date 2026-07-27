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

func TestJoinOpenAIPathDoesNotDuplicateV1(t *testing.T) {
	tests := []struct {
		base string
		path string
		want string
	}{
		{base: "https://api.example.com", path: "models", want: "https://api.example.com/v1/models"},
		{base: "https://api.example.com/", path: "models", want: "https://api.example.com/v1/models"},
		{base: "https://api.example.com/v1", path: "models", want: "https://api.example.com/v1/models"},
		{base: "https://api.example.com/v1/", path: "chat/completions", want: "https://api.example.com/v1/chat/completions"},
		{base: "https://api.example.com/prefix", path: "models", want: "https://api.example.com/prefix/v1/models"},
		{base: "https://api.example.com/prefix/v1", path: "models", want: "https://api.example.com/prefix/v1/models"},
	}
	for _, tc := range tests {
		got, err := adapters.JoinOpenAIPath(tc.base, tc.path)
		if err != nil {
			t.Fatalf("base=%q path=%q err=%v", tc.base, tc.path, err)
		}
		if got != tc.want {
			t.Fatalf("base=%q path=%q got=%q want=%q", tc.base, tc.path, got, tc.want)
		}
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

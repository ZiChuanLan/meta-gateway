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

// previewRouter builds a router whose outbound policy allows the loopback
// upstream this test stands up (the default policy refuses private addresses,
// which is why a preview test needs its own allow list).
func previewRouter(t *testing.T) (*httptest.Server, map[string]any) {
	t.Helper()
	dataDir := t.TempDir()
	db, err := store.OpenTest(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	enc, err := crypto.New("preview-master-key-32-chars-long!!")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		AdminToken:         "admin-test",
		AdminTokens:        []string{"admin-test"},
		MetricsToken:       "metrics-test",
		BackupDir:          filepath.Join(dataDir, "backups"),
		MaxAdminBodyBytes:  1 << 20,
		AuditRetentionDays: 90,
		AuditRetentionRows: 100000,
		OutboundAllowCIDRs: []string{"127.0.0.1/32"},
	}
	return httptest.NewServer(httpapi.NewTestRouter(t, cfg, db, enc)), nil
}

// The create dialog's 获取模型 asks an upstream what it serves before any channel
// exists. What matters beyond "it lists models": the list is deduped and sorted
// (the console renders it directly as the picker), a pasted endpoint is split off
// the base the same way the relay splits it, and a failure arrives in the
// console's own error vocabulary instead of a preview-only one.
func TestModelsPreviewListsUpstreamModelsWithoutStoringAnything(t *testing.T) {
	var sawPath, sawAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path
		sawAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"id":"zeta"},{"id":"alpha"},{"id":"alpha"},{"id":"mid"}]}`)
	}))
	defer upstream.Close()

	router, _ := previewRouter(t)
	defer router.Close()

	status, body := adminJSONBody(t, router.URL, "admin-test", http.MethodPost, "/admin/discovery/models-preview", map[string]any{
		"base_url":  upstream.URL,
		"secret":    "sk-preview",
		"type_hint": "openai-compatible",
	})
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	var out struct {
		Adapter string   `json:"adapter"`
		Models  []string `json:"models"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if out.Adapter != "openai-compatible" {
		t.Fatalf("adapter=%q", out.Adapter)
	}
	if strings.Join(out.Models, ",") != "alpha,mid,zeta" {
		t.Fatalf("models=%v, want deduped and sorted", out.Models)
	}
	if sawPath != "/v1/models" {
		t.Fatalf("upstream path=%q, want /v1/models", sawPath)
	}
	if sawAuth != "Bearer sk-preview" {
		t.Fatalf("authorization header=%q", sawAuth)
	}

	// A pasted endpoint is split off the base before listing, exactly as the
	// create path splits it before storing: the listing must describe the URL a
	// saved channel would actually use.
	status, body = adminJSONBody(t, router.URL, "admin-test", http.MethodPost, "/admin/discovery/models-preview", map[string]any{
		"base_url":  upstream.URL + "/v1/systemone",
		"secret":    "sk-preview",
		"type_hint": "openai-compatible",
	})
	if status != http.StatusOK {
		t.Fatalf("pasted endpoint: status=%d body=%s", status, body)
	}
	if sawPath != "/v1/models" {
		t.Fatalf("pasted endpoint listed from %q, want /v1/models", sawPath)
	}

	// Nothing was created by any of this.
	for _, path := range []string{"/admin/channels", "/admin/sites"} {
		list := adminGet(t, router.URL, "admin-test", path)
		if strings.Contains(string(list), `"id"`) {
			t.Fatalf("preview stored something: %s = %s", path, list)
		}
	}
}

func TestModelsPreviewReportsFailuresInTheConsoleVocabulary(t *testing.T) {
	router, _ := previewRouter(t)
	defer router.Close()

	cases := []struct {
		name     string
		payload  map[string]any
		status   int
		category string
	}{
		{
			name:    "missing secret",
			payload: map[string]any{"base_url": "https://api.example.com", "type_hint": "openai-compatible"},
			status:  http.StatusBadRequest,
		},
		{
			name:     "invalid base url",
			payload:  map[string]any{"base_url": "not-a-url", "secret": "sk-x", "type_hint": "openai-compatible"},
			status:   http.StatusUnprocessableEntity,
			category: "invalid_base_url",
		},
		{
			name:     "unknown provider",
			payload:  map[string]any{"base_url": "https://api.example.com", "secret": "sk-x", "type_hint": "definitely-not-a-provider"},
			status:   http.StatusUnprocessableEntity,
			category: "unsupported_adapter",
		},
	}
	for _, tc := range cases {
		status, body := adminJSONBody(t, router.URL, "admin-test", http.MethodPost, "/admin/discovery/models-preview", tc.payload)
		if status != tc.status {
			t.Fatalf("%s: status=%d body=%s", tc.name, status, body)
		}
		if tc.category != "" && !strings.Contains(string(body), tc.category) {
			t.Fatalf("%s: body=%s, want category %s", tc.name, body, tc.category)
		}
	}

	// A rejected key is the same message a refresh would show for the same
	// upstream answer — that is the point of sharing the mapping.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"bad key"}}`)
	}))
	defer upstream.Close()
	status, body := adminJSONBody(t, router.URL, "admin-test", http.MethodPost, "/admin/discovery/models-preview", map[string]any{
		"base_url":  upstream.URL,
		"secret":    "sk-rejected",
		"type_hint": "openai-compatible",
	})
	if status != http.StatusBadGateway {
		t.Fatalf("rejected key: status=%d body=%s", status, body)
	}
	if !strings.Contains(string(body), "upstream_unauthorized") {
		t.Fatalf("rejected key: body=%s, want upstream_unauthorized", body)
	}
}

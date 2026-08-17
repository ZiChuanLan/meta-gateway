package httpapi_test

import (
	"bytes"
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

func TestOperationsEndpointsAndAdminAudit(t *testing.T) {
	dataDir := t.TempDir()
	db, err := store.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	enc, err := crypto.New("operations-test-master-key-at-least-32-characters")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{AdminToken: "admin-test", AdminTokens: []string{"admin-test"}, MetricsToken: "metrics-test", BackupDir: filepath.Join(dataDir, "backups"), MaxAdminBodyBytes: 1024, AuditRetentionDays: 90, AuditRetentionRows: 100000, ExchangeAllowSecretExport: true}
	server := httptest.NewServer(httpapi.New(cfg, db, enc))
	defer server.Close()

	assertStatus(t, http.MethodGet, server.URL+"/healthz", "", nil, http.StatusOK)
	assertStatus(t, http.MethodGet, server.URL+"/readyz", "", nil, http.StatusOK)
	adminShell := assertStatus(t, http.MethodGet, server.URL+"/console/", "", nil, http.StatusOK)
	if !bytes.Contains(adminShell, []byte(`<div id="root"></div>`)) {
		t.Fatalf("admin shell=%s", adminShell)
	}
	assertStatus(t, http.MethodGet, server.URL+"/console/routing", "", nil, http.StatusOK)
	assertStatus(t, http.MethodGet, server.URL+"/console/assets/missing.js", "", nil, http.StatusNotFound)
	assertStatus(t, http.MethodGet, server.URL+"/metrics", "", nil, http.StatusUnauthorized)
	metrics := assertStatus(t, http.MethodGet, server.URL+"/metrics", "metrics-test", nil, http.StatusOK)
	if !strings.Contains(string(metrics), "meta_gateway_ready 1") {
		t.Fatalf("metrics=%s", metrics)
	}

	assertStatus(t, http.MethodGet, server.URL+"/admin/sites", "wrong", nil, http.StatusUnauthorized)
	site := []byte(`{"name":"audited","base_url":"https://example.com","platform":"openai-compatible","status":"enabled"}`)
	assertStatus(t, http.MethodPost, server.URL+"/admin/sites", "admin-test", site, http.StatusCreated)
	auditRaw := assertStatus(t, http.MethodGet, server.URL+"/admin/audit-events?limit=20", "admin-test", nil, http.StatusOK)
	var events []store.AuditEvent
	if err := json.Unmarshal(auditRaw, &events); err != nil {
		t.Fatal(err)
	}
	var foundSuccess, foundAuth bool
	for _, event := range events {
		if event.Action == "admin.site.create" && event.Outcome == "success" && event.StatusCode == http.StatusCreated {
			foundSuccess = true
		}
		if event.Action == "admin.auth" && event.Category == "unauthorized" {
			foundAuth = true
		}
	}
	if !foundSuccess || !foundAuth {
		t.Fatalf("events=%+v", events)
	}

	backupRaw := assertStatus(t, http.MethodPost, server.URL+"/admin/backups", "admin-test", []byte(`{"path":"ignored"}`), http.StatusCreated)
	var backupRecord store.BackupRecord
	if err := json.Unmarshal(backupRaw, &backupRecord); err != nil {
		t.Fatal(err)
	}
	if backupRecord.Status != "success" || !strings.HasPrefix(backupRecord.Name, "meta-gateway-") {
		t.Fatalf("backup=%+v", backupRecord)
	}
	listRaw := assertStatus(t, http.MethodGet, server.URL+"/admin/backups", "admin-test", nil, http.StatusOK)
	if !bytes.Contains(listRaw, []byte(backupRecord.Name)) || bytes.Contains(listRaw, []byte("ignored")) {
		t.Fatalf("backups=%s", listRaw)
	}
}

func TestAdminBodyLimitAndTrailingJSON(t *testing.T) {
	dataDir := t.TempDir()
	db, err := store.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	enc, _ := crypto.New("body-limit-test-master-key-at-least-32-characters")
	cfg := &config.Config{AdminToken: "admin", AdminTokens: []string{"admin"}, MetricsToken: "metrics", BackupDir: filepath.Join(dataDir, "backups"), MaxAdminBodyBytes: 64, ExchangeAllowSecretExport: true}
	server := httptest.NewServer(httpapi.New(cfg, db, enc))
	defer server.Close()
	assertStatus(t, http.MethodPost, server.URL+"/admin/sites", "admin", []byte(`{"name":"one"}{"name":"two"}`), http.StatusBadRequest)
	oversized := []byte(`{"name":"` + strings.Repeat("a", 128) + `"}`)
	body := assertStatus(t, http.MethodPost, server.URL+"/admin/sites", "admin", oversized, http.StatusBadRequest)
	if bytes.Contains(bytes.ToLower(body), []byte("request body too large")) {
		t.Fatalf("leaked decoder detail: %s", body)
	}
	// Runtime settings must use the same contextual admin body limit; this
	// endpoint previously decoded r.Body directly and could bypass it.
	runtimeBody := []byte(`{"retry_times":1,"padding":"` + strings.Repeat("x", 128) + `"}`)
	assertStatus(t, http.MethodPut, server.URL+"/admin/runtime-settings", "admin", runtimeBody, http.StatusBadRequest)
}

func assertStatus(t *testing.T, method, url, token string, body []byte, status int) []byte {
	t.Helper()
	request, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	if response.StatusCode != status {
		t.Fatalf("%s %s status=%d want=%d body=%s", method, url, response.StatusCode, status, raw)
	}
	return raw
}

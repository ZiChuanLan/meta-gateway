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

func newLimitTestServer(t *testing.T, cfg *config.Config) *httptest.Server {
	t.Helper()
	dataDir := t.TempDir()
	db, err := store.OpenTest(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	enc, err := crypto.New("audit-flood-test-master-key-at-least-32-characters")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BackupDir == "" {
		cfg.BackupDir = filepath.Join(dataDir, "backups")
	}
	server := httptest.NewServer(httpapi.NewTestRouter(t, cfg, db, enc))
	t.Cleanup(server.Close)
	return server
}

func limitTestConfig() *config.Config {
	return &config.Config{AdminToken: "admin-test", AdminTokens: []string{"admin-test"}, MetricsToken: "metrics-test",
		MaxAdminBodyBytes: 1024, AuditRetentionDays: 90, AuditRetentionRows: 100000, ExchangeAllowSecretExport: true}
}

func auditEvents(t *testing.T, serverURL string) []store.AuditEvent {
	t.Helper()
	raw := assertStatus(t, http.MethodGet, serverURL+"/admin/audit-events?limit=500", "admin-test", nil, http.StatusOK)
	var events []store.AuditEvent
	if err := json.Unmarshal(raw, &events); err != nil {
		t.Fatal(err)
	}
	return events
}

// TestAuditFailureWritesAreBoundedPerClient: rejected admin probes are the one
// audited class an unauthenticated client drives, so the writer must bound how
// many rows one client can add — while authenticated activity keeps its trail.
func TestAuditFailureWritesAreBoundedPerClient(t *testing.T) {
	server := newLimitTestServer(t, limitTestConfig())

	const flood = 60
	for i := 0; i < flood; i++ {
		assertStatus(t, http.MethodPost, server.URL+"/admin/nonexistent", "", []byte(`{}`), http.StatusUnauthorized)
	}
	failures := 0
	for _, event := range auditEvents(t, server.URL) {
		if event.Outcome == "failure" {
			failures++
		}
	}
	// Burst 10 with a 30/min refill: a tight loop stays far below the flood it sent.
	if failures == 0 || failures > 20 {
		t.Fatalf("audit failure rows=%d after %d unauthenticated probes", failures, flood)
	}

	// Authenticated activity is never throttled by the failure budget.
	site := []byte(`{"name":"after-flood","base_url":"https://example.com","platform":"openai-compatible","status":"enabled"}`)
	assertStatus(t, http.MethodPost, server.URL+"/admin/sites", "admin-test", site, http.StatusCreated)
	foundSuccess := false
	for _, event := range auditEvents(t, server.URL) {
		if event.Action == "admin.site.create" && event.Outcome == "success" {
			foundSuccess = true
		}
	}
	if !foundSuccess {
		t.Fatal("authenticated audit entry went missing after an unauthenticated flood")
	}
}

// TestAuditRequestIDIsTruncated: chi copies the client's X-Request-Id into the
// audit row verbatim, and that header may be as large as MaxHeaderBytes.
func TestAuditRequestIDIsTruncated(t *testing.T) {
	server := newLimitTestServer(t, limitTestConfig())

	request, err := http.NewRequest(http.MethodPost, server.URL+"/admin/nonexistent", bytes.NewReader([]byte(`{}`)))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Request-Id", strings.Repeat("A", 4096))
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status=%d", response.StatusCode)
	}

	stored := ""
	for _, event := range auditEvents(t, server.URL) {
		if len(event.RequestID) > 128 {
			t.Fatalf("audit row kept %d bytes of request_id", len(event.RequestID))
		}
		if strings.HasPrefix(event.RequestID, "AAAA") {
			stored = event.RequestID
		}
	}
	if len(stored) != 128 {
		t.Fatalf("stored request_id=%d bytes, want the 128-byte cap", len(stored))
	}
}

// TestLoginLockoutIsPerClientNotGlobal: the shared login ceiling is a flood break,
// never the thing one unauthenticated client drains — otherwise that client locks
// every operator out of the console.
func TestLoginLockoutIsPerClientNotGlobal(t *testing.T) {
	cfg := limitTestConfig()
	cfg.TrustedProxyCIDRs = []string{"127.0.0.1/32"}
	server := newLimitTestServer(t, cfg)

	login := func(clientIP string) int {
		t.Helper()
		request, err := http.NewRequest(http.MethodPost, server.URL+"/admin/session",
			strings.NewReader(`{"username":"admin","password":"wrong-password"}`))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Forwarded-For", clientIP)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		response.Body.Close()
		return response.StatusCode
	}

	// One source spends its own budget (per-IP burst is 5)...
	var last int
	for i := 0; i < 6; i++ {
		last = login("203.0.113.9")
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("flooding client was not rate limited: status=%d", last)
	}
	// ...and another source is still served.
	if code := login("203.0.113.10"); code != http.StatusUnauthorized {
		t.Fatalf("second client locked out by the first client's traffic: status=%d", code)
	}
}

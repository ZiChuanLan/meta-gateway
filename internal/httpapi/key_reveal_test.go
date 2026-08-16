package httpapi_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/config"
	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/httpapi"
	"github.com/lan/meta-gateway/internal/store"
)

// revealTestServer boots the full router with a fresh store, mirroring
// setupServer but without fixtures, so reveal/rotate tests control their own
// credentials.
func revealTestServer(t *testing.T) (*httptest.Server, *store.DB, *crypto.Encrypter) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(dir)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	enc, err := crypto.New("reveal-test-master-key-32b!!")
	if err != nil {
		t.Fatalf("crypto: %v", err)
	}
	cfg := &config.Config{
		HTTPAddr:                   ":0",
		DataDir:                    dir,
		AdminToken:                 "admin-secret",
		AdminTokens:                []string{"admin-secret"},
		MasterKey:                  "reveal-test-master-key-32b!!",
		OutboundAllowCIDRs:         []string{"127.0.0.0/8", "::1/128"},
		OutboundConnectTimeout:     2 * time.Second,
		OutboundTLSHandshakeTimeout: 2 * time.Second,
		OutboundResponseHeaderTimeout: 2 * time.Second,
	}
	handler := httpapi.New(cfg, db, enc)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv, db, enc
}

// adminCall performs an authenticated admin request and returns the status
// plus parsed JSON body (without failing on >= 300).
func adminCall(t *testing.T, base, method, path string, body any) (int, map[string]any, string) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, base+path, rdr)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer admin-secret")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return resp.StatusCode, out, string(raw)
}

func TestDownstreamKeyRevealAndRotate(t *testing.T) {
	srv, _, _ := revealTestServer(t)
	base := srv.URL

	status, created, _ := adminCall(t, base, "POST", "/admin/downstream-keys", map[string]string{"name": "viewable"})
	if status != http.StatusCreated {
		t.Fatalf("create status %d body %s", status, created)
	}
	keyID := int64(created["id"].(float64))
	original := created["token"].(string)
	if original == "" {
		t.Fatal("expected generated token")
	}

	// List marks the key as having re-viewable plaintext (bare array body).
	_, _, raw := adminCall(t, base, "GET", "/admin/downstream-keys", nil)
	if !strings.Contains(raw, `"has_token":true`) {
		t.Fatalf("expected has_token:true in list, got %s", raw)
	}

	// Reveal returns the exact same plaintext.
	status, revealed, _ := adminCall(t, base, "POST", fmt.Sprintf("/admin/downstream-keys/%d/reveal", keyID), nil)
	if status != http.StatusOK {
		t.Fatalf("reveal status %d", status)
	}
	if revealed["token"] != original {
		t.Fatalf("reveal mismatch: got %q want %q", revealed["token"], original)
	}

	// Rotate replaces the token; the old one must fail auth.
	status, rotated, _ := adminCall(t, base, "POST", fmt.Sprintf("/admin/downstream-keys/%d/rotate", keyID), nil)
	if status != http.StatusOK {
		t.Fatalf("rotate status %d body %v", status, rotated)
	}
	newToken := rotated["token"].(string)
	if newToken == "" || newToken == original {
		t.Fatalf("expected a fresh token, got %q", newToken)
	}
	status, revealed2, _ := adminCall(t, base, "POST", fmt.Sprintf("/admin/downstream-keys/%d/reveal", keyID), nil)
	if status != http.StatusOK || revealed2["token"] != newToken {
		t.Fatalf("reveal after rotate: status %d token %v", status, revealed2["token"])
	}

	// The old token no longer authenticates /v1 requests.
	req, _ := http.NewRequest(http.MethodGet, base+"/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+original)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old token expected 401, got %d", resp.StatusCode)
	}
	// And the new one does.
	req2, _ := http.NewRequest(http.MethodGet, base+"/v1/models", nil)
	req2.Header.Set("Authorization", "Bearer "+newToken)
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("new token expected 200, got %d", resp2.StatusCode)
	}

	// Missing key returns 404.
	status, _, _ = adminCall(t, base, "POST", "/admin/downstream-keys/999999/reveal", nil)
	if status != http.StatusNotFound {
		t.Fatalf("missing reveal expected 404, got %d", status)
	}
}

func TestCredentialSecretReveal(t *testing.T) {
	srv, _, _ := revealTestServer(t)
	base := srv.URL

	status, site, _ := adminCall(t, base, "POST", "/admin/sites", map[string]string{
		"name":     "up",
		"base_url": "https://example.com",
		"platform": "openai-compatible",
		"status":   "enabled",
	})
	if status != http.StatusCreated {
		t.Fatalf("create site status %d", status)
	}
	siteID := int64(site["id"].(float64))

	status, cred, _ := adminCall(t, base, "POST", fmt.Sprintf("/admin/sites/%d/credentials", siteID), map[string]string{
		"kind":   "api_key",
		"secret": "sk-view-me-1234567890",
	})
	if status != http.StatusCreated {
		t.Fatalf("create credential status %d", status)
	}
	credID := int64(cred["id"].(float64))

	status, revealed, _ := adminCall(t, base, "POST", fmt.Sprintf("/admin/sites/%d/credentials/%d/reveal", siteID, credID), nil)
	if status != http.StatusOK {
		t.Fatalf("reveal status %d", status)
	}
	if revealed["secret"] != "sk-view-me-1234567890" {
		t.Fatalf("reveal mismatch: got %q", revealed["secret"])
	}

	// Revealing a credential with a bogus id is a 404.
	status, _, _ = adminCall(t, base, "POST", fmt.Sprintf("/admin/sites/%d/credentials/999999/reveal", siteID), nil)
	if status != http.StatusNotFound {
		t.Fatalf("missing credential reveal expected 404, got %d", status)
	}
}

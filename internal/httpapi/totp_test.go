package httpapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/config"
	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/httpapi"
	"github.com/lan/meta-gateway/internal/store"
	"github.com/lan/meta-gateway/internal/totp"
)

func setupTOTPServer(t *testing.T) *httptest.Server {
	t.Helper()
	dataDir := t.TempDir()
	db, err := store.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	enc, err := crypto.New("totp-test-master-key-at-least-32-characters!")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		AdminToken:   "admin-totp",
		AdminTokens:  []string{"admin-totp"},
		MetricsToken: "metrics-test",
		BackupDir:    dataDir + "/backups",
	}
	srv := httptest.NewServer(httpapi.New(cfg, db, enc))
	t.Cleanup(srv.Close)
	return srv
}

func TestTOTPFullFlow(t *testing.T) {
	srv := setupTOTPServer(t)

	login := func(code string) (int, map[string]any) {
		body := map[string]any{"token": "admin-totp"}
		if code != "" {
			body["totp_code"] = code
		}
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/admin/session", bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}

	// Before setup: login works with raw token only.
	status, out := login("")
	if status != http.StatusOK {
		t.Fatalf("pre-setup login status = %d", status)
	}
	if session, _ := out["session_token"].(string); !strings.HasPrefix(session, "mg-sess.") {
		t.Fatalf("session token = %q", session)
	}

	// Setup (admin auth still raw-token based).
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/admin/totp/setup", nil)
	req.Header.Set("Authorization", "Bearer admin-totp")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var setup map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&setup)
	secret := setup["secret"]
	if secret == "" || !strings.Contains(setup["otpauth_uri"], "otpauth://") {
		t.Fatalf("setup = %+v", setup)
	}

	// Enable with a valid code.
	code, _ := totp.Code(secret, time.Now())
	enableBody, _ := json.Marshal(map[string]string{"code": code})
	req, _ = http.NewRequest(http.MethodPost, srv.URL+"/admin/totp/enable", bytes.NewReader(enableBody))
	req.Header.Set("Authorization", "Bearer admin-totp")
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("enable status = %d", resp.StatusCode)
	}

	// Login without code is rejected with totp_required.
	status, out = login("")
	if status != http.StatusUnauthorized || out["error"] != "totp_required" {
		t.Fatalf("login without code: status=%d out=%v", status, out)
	}
	// Wrong code rejected.
	status, _ = login("000000")
	if status != http.StatusUnauthorized {
		t.Fatalf("login wrong code status = %d", status)
	}
	// Correct code succeeds.
	code, _ = totp.Code(secret, time.Now())
	status, out = login(code)
	if status != http.StatusOK {
		t.Fatalf("login with code status = %d", status)
	}
	sessionToken, _ := out["session_token"].(string)

	// Session token works on the admin wall.
	req, _ = http.NewRequest(http.MethodGet, srv.URL+"/admin/totp/status", nil)
	req.Header.Set("Authorization", "Bearer "+sessionToken)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin with session token status = %d", resp.StatusCode)
	}
	// Raw token still works (existing sessions unaffected).
	req, _ = http.NewRequest(http.MethodGet, srv.URL+"/admin/totp/status", nil)
	req.Header.Set("Authorization", "Bearer admin-totp")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin with raw token status = %d", resp.StatusCode)
	}
	// Tampered session token rejected.
	req, _ = http.NewRequest(http.MethodGet, srv.URL+"/admin/totp/status", nil)
	req.Header.Set("Authorization", "Bearer "+sessionToken+"x")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("tampered session token status = %d", resp.StatusCode)
	}

	// Disable requires a valid code.
	code, _ = totp.Code(secret, time.Now())
	disableBody, _ := json.Marshal(map[string]string{"code": code})
	req, _ = http.NewRequest(http.MethodPost, srv.URL+"/admin/totp/disable", bytes.NewReader(disableBody))
	req.Header.Set("Authorization", "Bearer "+sessionToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("disable status = %d", resp.StatusCode)
	}
	// After disable, raw login works again.
	status, _ = login("")
	if status != http.StatusOK {
		t.Fatalf("post-disable login status = %d", status)
	}
}

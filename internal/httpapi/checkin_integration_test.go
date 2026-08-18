package httpapi_test

import (
	"bytes"
	"encoding/json"
	"fmt"
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

func TestCheckinAdminWorkflow(t *testing.T) {
	var gotAuth, gotUser string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotUser = r.Header.Get("New-Api-User")
		_, _ = io.WriteString(w, `{"success":true,"message":"private upstream body","data":{"reward":3.5}}`)
	}))
	defer upstream.Close()

	db, err := store.Open(filepath.Join(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	enc, err := crypto.New("checkin-http-test-key")
	if err != nil {
		t.Fatal(err)
	}
	handler := httpapi.New(&config.Config{
		AdminToken:                "admin-secret",
		AdminTokens:               []string{"admin-secret"},
		ExchangeAllowSecretExport: true,
		OutboundAllowCIDRs:        []string{"127.0.0.0/8", "::1/128"},
	}, db, enc)
	server := httptest.NewServer(handler)
	defer server.Close()

	requestJSON := func(method, path string, body any, authorized bool) (int, []byte) {
		t.Helper()
		var reader io.Reader
		if body != nil {
			encoded, marshalErr := json.Marshal(body)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			reader = bytes.NewReader(encoded)
		}
		request, requestErr := http.NewRequest(method, server.URL+path, reader)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		if authorized {
			request.Header.Set("Authorization", "Bearer admin-secret")
		}
		response, requestErr := http.DefaultClient.Do(request)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		defer response.Body.Close()
		raw, _ := io.ReadAll(response.Body)
		return response.StatusCode, raw
	}

	status, raw := requestJSON(http.MethodPost, "/admin/sites", map[string]any{
		"name": "checkin", "base_url": upstream.URL, "platform": "new-api", "status": "enabled",
	}, true)
	if status != http.StatusCreated {
		t.Fatalf("create site status=%d body=%s", status, raw)
	}
	var site map[string]any
	_ = json.Unmarshal(raw, &site)
	siteID := asInt64(t, site["id"])

	status, raw = requestJSON(http.MethodPost, fmt.Sprintf("/admin/sites/%d/credentials", siteID), map[string]any{
		"kind": "session", "secret": "session-secret", "meta_json": `{"platform_user_id":42}`,
	}, true)
	if status != http.StatusCreated {
		t.Fatalf("create credential status=%d body=%s", status, raw)
	}
	var credential map[string]any
	_ = json.Unmarshal(raw, &credential)
	credentialID := asInt64(t, credential["id"])
	if enabled, _ := credential["checkin_enabled"].(bool); enabled {
		t.Fatal("credential check-in must default disabled")
	}

	for _, endpoint := range []string{
		fmt.Sprintf("/admin/checkin/credentials/%d/run", credentialID),
		"/admin/checkin/run",
		"/admin/checkin/logs",
		fmt.Sprintf("/admin/credentials/%d/checkin", credentialID),
	} {
		method := http.MethodPost
		if strings.HasSuffix(endpoint, "/logs") {
			method = http.MethodGet
		} else if strings.Contains(endpoint, "/credentials/") && strings.HasSuffix(endpoint, "/checkin") {
			method = http.MethodPut
		}
		status, _ = requestJSON(method, endpoint, map[string]bool{"enabled": true}, false)
		if status != http.StatusUnauthorized {
			t.Fatalf("unauthorized %s %s status=%d", method, endpoint, status)
		}
	}

	status, raw = requestJSON(http.MethodPost, fmt.Sprintf("/admin/checkin/credentials/%d/run", credentialID), nil, true)
	if status != http.StatusOK {
		t.Fatalf("single run status=%d body=%s", status, raw)
	}
	var result map[string]any
	_ = json.Unmarshal(raw, &result)
	if result["status"] != "success" || result["reward"] != "3.5" || gotAuth != "Bearer session-secret" || gotUser != "42" {
		t.Fatalf("result=%v auth=%q user=%q", result, gotAuth, gotUser)
	}

	status, raw = requestJSON(http.MethodPut, fmt.Sprintf("/admin/credentials/%d/checkin", credentialID), map[string]bool{"enabled": true}, true)
	if status != http.StatusOK || !bytes.Contains(raw, []byte(`"checkin_enabled":true`)) {
		t.Fatalf("enable status=%d body=%s", status, raw)
	}
	status, raw = requestJSON(http.MethodPost, "/admin/checkin/run", nil, true)
	if status != http.StatusOK || !bytes.Contains(raw, []byte(`"success_count":1`)) {
		t.Fatalf("batch status=%d body=%s", status, raw)
	}

	status, raw = requestJSON(http.MethodGet, fmt.Sprintf("/admin/checkin/logs?credential_id=%d&status=success&source=manual&limit=10", credentialID), nil, true)
	if status != http.StatusOK {
		t.Fatalf("logs status=%d body=%s", status, raw)
	}
	var logs []map[string]any
	if err := json.Unmarshal(raw, &logs); err != nil || len(logs) != 2 {
		t.Fatalf("logs=%s err=%v", raw, err)
	}
	if strings.Contains(string(raw), "session-secret") || strings.Contains(string(raw), "private upstream body") {
		t.Fatalf("check-in response leaked upstream material: %s", raw)
	}
	if logs[0]["id"].(float64) <= logs[1]["id"].(float64) {
		t.Fatalf("logs not newest first: %v", logs)
	}

	for _, query := range []string{"?limit=501", "?status=unknown", "?source=unknown", "?site_id=0"} {
		status, _ = requestJSON(http.MethodGet, "/admin/checkin/logs"+query, nil, true)
		if status != http.StatusBadRequest {
			t.Fatalf("invalid query %s status=%d", query, status)
		}
	}
	status, _ = requestJSON(http.MethodPost, "/admin/checkin/credentials/999999/run", nil, true)
	if status != http.StatusNotFound {
		t.Fatalf("missing credential status=%d", status)
	}
}

func TestExternalCheckinAdminWorkflow(t *testing.T) {
	var gotCookie, gotOrigin, gotReferer string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/checkin/spin" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		gotCookie = r.Header.Get("Cookie")
		gotOrigin = r.Header.Get("Origin")
		gotReferer = r.Header.Get("Referer")
		_, _ = io.WriteString(w, `{"success":true,"message":"ok","data":{"reward":"100 积分"}}`)
	}))
	defer upstream.Close()

	db, err := store.Open(filepath.Join(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	enc, err := crypto.New("external-checkin-test-key")
	if err != nil {
		t.Fatal(err)
	}
	handler := httpapi.New(&config.Config{
		AdminToken:                "admin-secret",
		AdminTokens:               []string{"admin-secret"},
		ExchangeAllowSecretExport: true,
		OutboundAllowCIDRs:        []string{"127.0.0.0/8", "::1/128"},
	}, db, enc)
	server := httptest.NewServer(handler)
	defer server.Close()

	requestJSON := func(method, path string, body any, authorized bool) (int, []byte) {
		t.Helper()
		var reader io.Reader
		if body != nil {
			encoded, marshalErr := json.Marshal(body)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			reader = bytes.NewReader(encoded)
		}
		request, requestErr := http.NewRequest(method, server.URL+path, reader)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		if authorized {
			request.Header.Set("Authorization", "Bearer admin-secret")
		}
		response, requestErr := http.DefaultClient.Do(request)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		defer response.Body.Close()
		raw, _ := io.ReadAll(response.Body)
		return response.StatusCode, raw
	}

	const cookie = "auth_token=t0ken-secret"
	status, raw := requestJSON(http.MethodPost, "/admin/checkin/external", map[string]any{
		"name": "薄荷公益站", "base_url": upstream.URL,
		"checkin_path": "/api/checkin/spin", "checkin_method": "POST",
		"cookie": cookie, "enabled": true,
	}, true)
	if status != http.StatusCreated {
		t.Fatalf("create external status=%d body=%s", status, raw)
	}
	var created struct {
		SiteID         int64 `json:"site_id"`
		CredentialID   int64 `json:"credential_id"`
		CheckinEnabled bool  `json:"checkin_enabled"`
	}
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatalf("create response: %v body=%s", err, raw)
	}
	if !created.CheckinEnabled {
		t.Fatal("external check-in must honor enabled=true")
	}

	// List must never expose the cookie.
	status, raw = requestJSON(http.MethodGet, "/admin/checkin/external", nil, true)
	if status != http.StatusOK || strings.Contains(string(raw), "t0ken-secret") {
		t.Fatalf("external list status=%d body=%s", status, raw)
	}
	if !strings.Contains(string(raw), "薄荷公益站") {
		t.Fatalf("external list missing entry: %s", raw)
	}

	// Run once: adapter must receive the raw cookie + browser headers.
	status, raw = requestJSON(http.MethodPost, fmt.Sprintf("/admin/checkin/credentials/%d/run", created.CredentialID), nil, true)
	if status != http.StatusOK {
		t.Fatalf("external run status=%d body=%s", status, raw)
	}
	if gotCookie != cookie || gotOrigin != upstream.URL || gotReferer != upstream.URL+"/" {
		t.Fatalf("upstream saw cookie=%q origin=%q referer=%q", gotCookie, gotOrigin, gotReferer)
	}
	if !bytes.Contains(raw, []byte(`"reward":"100 积分"`)) {
		t.Fatalf("external run missing reward: %s", raw)
	}

	// Update name + keep cookie untouched (empty cookie field = keep).
	status, raw = requestJSON(http.MethodPut, fmt.Sprintf("/admin/checkin/external/%d", created.SiteID), map[string]any{
		"name": "薄荷 2", "checkin_path": "/api/checkin/spin",
	}, true)
	if status != http.StatusOK || !strings.Contains(string(raw), "薄荷 2") {
		t.Fatalf("external update status=%d body=%s", status, raw)
	}

	// Unauthorized access fails.
	status, _ = requestJSON(http.MethodGet, "/admin/checkin/external", nil, false)
	if status != http.StatusUnauthorized {
		t.Fatalf("external list without auth status=%d", status)
	}

	// Delete removes site + cascaded credential.
	status, _ = requestJSON(http.MethodDelete, fmt.Sprintf("/admin/checkin/external/%d", created.SiteID), nil, true)
	if status != http.StatusNoContent {
		t.Fatalf("external delete status=%d", status)
	}
	status, raw = requestJSON(http.MethodGet, "/admin/checkin/external", nil, true)
	if status != http.StatusOK || strings.Contains(string(raw), "薄荷") {
		t.Fatalf("external list after delete status=%d body=%s", status, raw)
	}
}

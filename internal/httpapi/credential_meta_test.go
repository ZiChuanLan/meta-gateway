package httpapi_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lan/meta-gateway/internal/config"
	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/httpapi"
	"github.com/lan/meta-gateway/internal/store"
)

// credentialMetaTestServer boots the admin router over a fresh store so the
// credential meta contract can be exercised end to end.
func credentialMetaTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(dir)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	enc, err := crypto.New("credential-meta-test-key-32ch!")
	if err != nil {
		t.Fatalf("crypto: %v", err)
	}
	handler := httpapi.NewTestRouter(t, &config.Config{
		AdminToken:         "admin-secret",
		AdminTokens:        []string{"admin-secret"},
		OutboundAllowCIDRs: []string{"127.0.0.0/8", "::1/128"},
	}, db, enc)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

// adminGetRaw performs an authenticated GET and returns the raw body.
func adminGetRaw(t *testing.T, base, path string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, base+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer admin-secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw
}

// TestCredentialMetaPlatformUserIDIsCanonicalized pins the admin contract for
// the hand-entered New-API user id: a numeric or quoted id is accepted and
// stored as a bare JSON number (the compat user-id headers are derived from
// it), a non-positive/garbage id is rejected, and unrelated keys survive.
func TestCredentialMetaPlatformUserIDIsCanonicalized(t *testing.T) {
	srv := credentialMetaTestServer(t)
	base := srv.URL

	status, site, raw := adminCall(t, base, http.MethodPost, "/admin/sites", map[string]any{
		"name": "meta-site", "base_url": "https://api.example.com",
		"platform": "new-api", "status": "enabled",
	})
	if status != http.StatusCreated {
		t.Fatalf("create site status=%d body=%s", status, raw)
	}
	siteID := int64(site["id"].(float64))

	// A quoted id (AAH export style) plus an unrelated key.
	status, cred, raw := adminCall(t, base, http.MethodPost,
		fmt.Sprintf("/admin/sites/%d/credentials", siteID), map[string]any{
			"kind": "access_token", "secret": "user-token", "status": "enabled",
			"meta_json": `{"platform_user_id":"1544","keep_me":"yes"}`,
		})
	if status != http.StatusCreated {
		t.Fatalf("create credential status=%d body=%s", status, raw)
	}
	if got := cred["meta_json"]; got != `{"keep_me":"yes","platform_user_id":1544}` {
		t.Fatalf("meta_json = %v, want canonical number with keep_me preserved", got)
	}
	credID := int64(cred["id"].(float64))

	// Rotating the id onto an existing credential keeps the other key.
	status, cred, raw = adminCall(t, base, http.MethodPut,
		fmt.Sprintf("/admin/credentials/%d", credID), map[string]any{
			"meta_json": `{"platform_user_id":77,"keep_me":"yes"}`,
		})
	if status != http.StatusOK {
		t.Fatalf("update credential status=%d body=%s", status, raw)
	}
	if got := cred["meta_json"]; got != `{"keep_me":"yes","platform_user_id":77}` {
		t.Fatalf("updated meta_json = %v", got)
	}

	// An empty object clears every key. (An empty meta_json string means
	// "field not supplied" and could never clear anything.)
	status, cred, raw = adminCall(t, base, http.MethodPut,
		fmt.Sprintf("/admin/credentials/%d", credID), map[string]any{
			"meta_json": "{}",
		})
	if status != http.StatusOK {
		t.Fatalf("clear credential meta status=%d body=%s", status, raw)
	}
	if got := cred["meta_json"]; got != "" {
		t.Fatalf("cleared meta_json = %v, want empty", got)
	}

	for _, bad := range []string{
		`{"platform_user_id":0}`,
		`{"platform_user_id":-3}`,
		`{"platform_user_id":"abc"}`,
		`{"platform_user_id":{}`,
		`{"platform_user_id":12}{"x":1}`,
		`"not-an-object"`,
	} {
		status, _, body := adminCall(t, base, http.MethodPut,
			fmt.Sprintf("/admin/credentials/%d", credID), map[string]any{
				"meta_json": bad,
			})
		if status != http.StatusBadRequest {
			t.Fatalf("meta_json %s: status=%d body=%s, want 400", bad, status, body)
		}
	}
}

// TestCheckinUsesStoredPlatformUserIDFromQuotedMeta covers the manual-add path:
// an operator types the numeric user id, and check-in must send it as the
// New-API compat headers without probing /api/user/self first. The upstream
// 404s every account endpoint so a regression there fails loudly.
func TestCheckinUsesStoredPlatformUserIDFromQuotedMeta(t *testing.T) {
	var gotUser, gotAuth string
	probeCalls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/user/checkin":
			gotUser = r.Header.Get("New-Api-User")
			gotAuth = r.Header.Get("Authorization")
			_, _ = io.WriteString(w, `{"success":true,"message":"ok","data":{"reward":1}}`)
		case "/api/user/self":
			probeCalls++
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)

	srv := credentialMetaTestServer(t)
	base := srv.URL

	status, site, raw := adminCall(t, base, http.MethodPost, "/admin/sites", map[string]any{
		"name": "checkin-site", "base_url": upstream.URL, "platform": "new-api", "status": "enabled",
	})
	if status != http.StatusCreated {
		t.Fatalf("create site status=%d body=%s", status, raw)
	}
	siteID := int64(site["id"].(float64))

	status, cred, raw := adminCall(t, base, http.MethodPost,
		fmt.Sprintf("/admin/sites/%d/credentials", siteID), map[string]any{
			"kind": "access_token", "secret": "user-token", "status": "enabled",
			// Quoted, exactly as an older AAH export / a manual paste may look.
			"meta_json": `{"platform_user_id":"1544"}`,
		})
	if status != http.StatusCreated {
		t.Fatalf("create credential status=%d body=%s", status, raw)
	}
	credID := int64(cred["id"].(float64))

	status, result, raw := adminCall(t, base, http.MethodPost,
		fmt.Sprintf("/admin/checkin/credentials/%d/run", credID), nil)
	if status != http.StatusOK {
		t.Fatalf("check-in run status=%d body=%s", status, raw)
	}
	if result["status"] != "success" {
		t.Fatalf("check-in status=%v body=%s", result["status"], raw)
	}
	if gotUser != "1544" {
		t.Fatalf("New-Api-User = %q, want 1544", gotUser)
	}
	if gotAuth != "Bearer user-token" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if probeCalls != 0 {
		t.Fatalf("stored user id must skip /api/user/self, got %d probe calls", probeCalls)
	}

	// The stored document is normalized in place so the FK headers stay
	// derivable on later runs.
	status, listRaw := adminGetRaw(t, base, fmt.Sprintf("/admin/sites/%d/credentials", siteID))
	if status != http.StatusOK {
		t.Fatalf("list credentials status=%d body=%s", status, listRaw)
	}
	var list []map[string]any
	if err := json.Unmarshal(listRaw, &list); err != nil || len(list) != 1 {
		t.Fatalf("credentials=%s err=%v", listRaw, err)
	}
	if got := list[0]["meta_json"]; got != `{"platform_user_id":1544}` {
		t.Fatalf("stored meta_json = %v, want canonical number", got)
	}
}

// TestCheckinWithoutUserIDFailsWithActionableCategory covers the dead end the
// UI now fixes: no stored id and an unreachable /api/user/self must surface as
// user_id_unavailable rather than a generic failure.
func TestCheckinWithoutUserIDFailsWithActionableCategory(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	t.Cleanup(upstream.Close)

	srv := credentialMetaTestServer(t)
	base := srv.URL

	status, site, raw := adminCall(t, base, http.MethodPost, "/admin/sites", map[string]any{
		"name": "no-id-site", "base_url": upstream.URL, "platform": "new-api", "status": "enabled",
	})
	if status != http.StatusCreated {
		t.Fatalf("create site status=%d body=%s", status, raw)
	}
	siteID := int64(site["id"].(float64))
	status, cred, raw := adminCall(t, base, http.MethodPost,
		fmt.Sprintf("/admin/sites/%d/credentials", siteID), map[string]any{
			"kind": "access_token", "secret": "user-token", "status": "enabled",
		})
	if status != http.StatusCreated {
		t.Fatalf("create credential status=%d body=%s", status, raw)
	}
	credID := int64(cred["id"].(float64))

	status, result, raw := adminCall(t, base, http.MethodPost,
		fmt.Sprintf("/admin/checkin/credentials/%d/run", credID), nil)
	if status != http.StatusOK {
		t.Fatalf("check-in run status=%d body=%s", status, raw)
	}
	if result["status"] != "failed" || result["category"] != "user_id_unavailable" {
		t.Fatalf("status/category = %v/%v body=%s", result["status"], result["category"], raw)
	}
}

package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/totp"

	"github.com/lan/meta-gateway/internal/auth"
)

func TestUnifiedLoginRolesAndIsolation(t *testing.T) {
	for _, role := range []string{"owner", "admin", "member"} {
		t.Run(role, func(t *testing.T) {
			e := newTeamTestEnv(t)
			e.enable()
			username, password := "owner", "owner-password-123"
			if role != "owner" {
				username, password = "account-"+role, "member-password-123"
				e.member(username, role)
			}
			b := e.browser()
			code, raw, _ := e.call(b.client, "POST", "/admin/session", map[string]string{"username": strings.ToUpper(username), "password": password}, "", "")
			if code != 200 {
				t.Fatalf("login: %d %s", code, raw)
			}
			var result struct {
				User         TeamUser
				CSRF         string
				SessionToken string `json:"session_token"`
			}
			if err := json.Unmarshal(raw, &result); err != nil {
				t.Fatal(err)
			}
			if result.User.Role != role || result.CSRF == "" || result.SessionToken != "" {
				t.Fatalf("unexpected identity: %s", raw)
			}
			b.csrf = result.CSRF
			b.request("GET", "/me", nil, 200)
			if role == "member" {
				code, _, _ = e.call(b.client, "GET", "/admin/sites", nil, "", b.csrf)
				if code != 403 {
					t.Fatalf("member reached admin sites: %d", code)
				}
			}
		})
	}
}

func TestUnifiedLoginOperatorAndLegacy(t *testing.T) {
	e := newTeamTestEnv(t)
	for _, body := range []map[string]string{
		{"username": "admin", "password": "team-admin-secret"},
		{"token": "team-admin-secret"},
	} {
		code, raw, _ := e.call(http.DefaultClient, "POST", "/admin/session", body, "", "")
		var result struct {
			Token string `json:"session_token"`
		}
		json.Unmarshal(raw, &result)
		if code != 200 || !strings.HasPrefix(result.Token, auth.SessionPrefix) {
			t.Fatalf("login: %d %s", code, raw)
		}
	}
}

func TestUnifiedLoginDoesNotElevateAccountNameCollision(t *testing.T) {
	e := newTeamTestEnv(t)
	e.enable()
	e.member("admin", "member")
	for _, password := range []string{"wrong-password", "team-admin-secret"} {
		code, raw, _ := e.call(http.DefaultClient, "POST", "/admin/session", map[string]string{"username": "admin", "password": password}, "", "")
		if code != 401 || !strings.Contains(string(raw), "invalid_credentials") {
			t.Fatalf("account fallback: %d %s", code, raw)
		}
	}
}

func TestUnifiedLoginCustomOperatorName(t *testing.T) {
	e := newTeamTestEnv(t)
	h := &sessionHandler{db: e.db, enc: e.enc, adminTokens: []string{"operator-secret"}, adminUsername: "operator", sessionKey: []byte("test-key")}
	for _, username := range []string{"operator", "admin"} {
		req := httptest.NewRequest("POST", "/admin/session", strings.NewReader(`{"username":"`+username+`","password":"operator-secret"}`))
		w := httptest.NewRecorder()
		h.login(w, req)
		want := 200
		if username == "admin" {
			want = 401
		}
		if w.Code != want {
			t.Fatalf("%s: %d %s", username, w.Code, w.Body.String())
		}
	}
	req := httptest.NewRequest("POST", "/admin/session", strings.NewReader(`{"username":"operator","password":"operator-secret"}`))
	req.Header.Set("Origin", "https://other.example")
	w := httptest.NewRecorder()
	h.login(w, req)
	if w.Code != 403 {
		t.Fatalf("cross-origin login: %d", w.Code)
	}
}

func TestUnifiedLoginPreservesOperatorTOTP(t *testing.T) {
	e := newTeamTestEnv(t)
	secret, err := totp.NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := e.enc.Encrypt([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	if err = e.db.AdminTOTP.SetSecret(encrypted); err != nil {
		t.Fatal(err)
	}
	if err = e.db.AdminTOTP.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	body := map[string]string{"username": "admin", "password": "team-admin-secret"}
	code, raw, _ := e.call(http.DefaultClient, "POST", "/admin/session", body, "", "")
	if code != 401 || !strings.Contains(string(raw), "totp_required") {
		t.Fatalf("TOTP bypass: %d %s", code, raw)
	}
	body["totp_code"], err = totp.Code(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	code, raw, _ = e.call(http.DefaultClient, "POST", "/admin/session", body, "", "")
	if code != 200 {
		t.Fatalf("TOTP login: %d %s", code, raw)
	}
}

func TestUnifiedLoginPausedAccountDoesNotFallBack(t *testing.T) {
	e := newTeamTestEnv(t)
	e.enable()
	e.member("admin", "member")
	if _, err := e.db.Exec(`UPDATE team_users SET status='paused' WHERE username='admin'`); err != nil {
		t.Fatal(err)
	}
	for _, password := range []string{"member-password-123", "team-admin-secret"} {
		code, raw, _ := e.call(http.DefaultClient, "POST", "/admin/session", map[string]string{"username": "admin", "password": password}, "", "")
		if code != 401 {
			t.Fatalf("paused login: %d %s", code, raw)
		}
	}
}

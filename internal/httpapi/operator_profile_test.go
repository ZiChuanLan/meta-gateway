package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/totp"
)

func TestOperatorUsernameSaveOverridesEnvironmentAndSurvivesReload(t *testing.T) {
	e := newTeamTestEnv(t)
	raw := e.admin("GET", "/admin/operator-profile", nil, 200)
	var profile struct {
		Username   string
		Configured bool
	}
	json.Unmarshal(raw, &profile)
	if profile.Username != "admin" || profile.Configured {
		t.Fatal(profile)
	}
	e.admin("POST", "/admin/operator-profile", map[string]string{"username": "new-admin", "token": "wrong"}, 403)
	e.admin("POST", "/admin/operator-profile", map[string]string{"username": " New-Admin ", "token": "team-admin-secret"}, 200)
	name, err := e.db.OperatorUsername("different-env")
	if err != nil || name != "new-admin" {
		t.Fatalf("%s %v", name, err)
	}
	for _, tc := range []struct {
		name string
		want int
	}{{"new-admin", 200}, {"admin", 401}} {
		status, _, _ := e.call(http.DefaultClient, "POST", "/admin/session", map[string]string{"username": tc.name, "password": "team-admin-secret"}, "", "")
		if status != tc.want {
			t.Fatalf("%s %d", tc.name, status)
		}
	}
	prefs, err := e.db.OperatorPreferences()
	if err != nil || prefs.AdminUsername != "new-admin" {
		t.Fatal(prefs, err)
	}
	// A later team signup cannot claim the explicitly saved administrator name.
	e.admin("POST", "/admin/team/bootstrap", map[string]string{"username": "new-admin", "name": "Owner", "password": "owner-password-123"}, 409)
}
func TestOperatorUsernameRejectsTeamCollisionAndRequiresTOTP(t *testing.T) {
	e := newTeamTestEnv(t)
	e.enable()
	e.member("member", "member")
	e.admin("POST", "/admin/operator-profile", map[string]string{"username": "member", "token": "team-admin-secret"}, 409)
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
	e.admin("POST", "/admin/operator-profile", map[string]string{"username": "safe-admin", "token": "team-admin-secret"}, 403)
	code, err := totp.Code(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	e.admin("POST", "/admin/operator-profile", map[string]string{"username": "safe-admin", "token": "team-admin-secret", "totp_code": code}, 200)
}
func TestOperatorPreferencesAreOwnerOnly(t *testing.T) {
	e := newTeamTestEnv(t)
	e.enable()
	member := e.member("ordinary", "member")
	admin := e.member("limited", "admin")
	for _, b := range []*teamTestBrowser{member, admin} {
		b.request("GET", "/admin/operator-profile", nil, 403)
		b.request("POST", "/admin/operator-profile", map[string]string{"username": "nope", "token": "team-admin-secret"}, 403)
	}
	// The update channel is not a preference any more: it is the deployment's
	// tag, so there is no endpoint left to write it through. Non-owners are
	// stopped by the admin guard before routing; the owner sees the route gone.
	for _, b := range []*teamTestBrowser{member, admin} {
		b.request("GET", "/admin/update-channel", nil, 403)
		b.request("PUT", "/admin/update-channel", map[string]string{"channel": "beta"}, 403)
	}
	e.admin("GET", "/admin/update-channel", nil, 404)
	e.admin("PUT", "/admin/update-channel", map[string]string{"channel": "beta"}, 404)
}

func TestOAuthUsernameSkipsReservedOperator(t *testing.T) {
	e := newTeamTestEnv(t)
	e.admin("POST", "/admin/operator-profile", map[string]string{"username": "github-alice", "token": "team-admin-secret"}, 200)
	h := NewTeamHandler(e.db, e.enc)
	name, err := h.uniqueUsername("github-alice")
	if err != nil || name != "github-alice-2" {
		t.Fatalf("%q %v", name, err)
	}
}

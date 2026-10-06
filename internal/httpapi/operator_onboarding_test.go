package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/lan/meta-gateway/internal/auth"
)

// The upgrade path: a deployment whose only credential is ADMIN_TOKEN can still
// sign in, and the console can then claim one real account. Once that account
// exists the token stops being a password — which is what makes the upgrade
// window close by itself instead of needing a flag.
func TestOperatorClaimClosesTheUpgradePath(t *testing.T) {
	e := newTeamTestEnv(t)

	// The shape the upgrade help dialog sends: the token, no username.
	if code, raw, _ := e.call(http.DefaultClient, "POST", "/admin/session", map[string]string{"token": "team-admin-secret"}, "", ""); code != 200 {
		t.Fatalf("pre-claim upgrade login: %d %s", code, raw)
	}

	var before onboardingState
	if err := json.Unmarshal(e.admin("GET", "/admin/operator/onboarding", nil, 200), &before); err != nil {
		t.Fatal(err)
	}
	if !before.Required || before.HasOwner || !before.TokenLogin {
		t.Fatalf("pre-claim state: %+v", before)
	}
	// The prefill is the name this deployment already answers to.
	if before.Username != "admin" {
		t.Fatalf("prefill = %q, want the operator name", before.Username)
	}

	e.admin("POST", "/admin/operator/claim", map[string]string{
		"username": "boss", "password": "boss-password-123", "token": "team-admin-secret",
	}, 200)

	var after onboardingState
	if err := json.Unmarshal(e.admin("GET", "/admin/operator/onboarding", nil, 200), &after); err != nil {
		t.Fatal(err)
	}
	if after.Required || !after.HasOwner || after.TokenLogin {
		t.Fatalf("post-claim state: %+v", after)
	}
	if after.Username != "boss" {
		t.Fatalf("owner name = %q, want the claimed one", after.Username)
	}

	// The claimed credential signs in even though this gateway is still personal:
	// the member module's switch is not what authenticates the owner.
	code, raw, _ := e.call(http.DefaultClient, "POST", "/admin/session",
		map[string]string{"username": "boss", "password": "boss-password-123"}, "", "")
	if code != 200 {
		t.Fatalf("owner login: %d %s", code, raw)
	}
	var session struct {
		Token string `json:"session_token"`
	}
	if err := json.Unmarshal(raw, &session); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(session.Token, auth.SessionPrefix) {
		t.Fatalf("owner login did not issue a deployment session: %s", raw)
	}
	// The raw token still authorizes the API (it is the deployment secret); only
	// the sign-in form stops accepting it.
	e.admin("GET", "/admin/sites", nil, 200)
}

// Every shape of token sign-in is refused once the credential exists. Kept in
// its own environment because the sign-in endpoint has a small brute-force
// budget and a single test must not spend all of it.
func TestClaimedOwnerRefusesEveryTokenShape(t *testing.T) {
	e := newTeamTestEnv(t)
	e.admin("POST", "/admin/operator/claim", map[string]string{
		"username": "boss", "password": "boss-password-123", "token": "team-admin-secret",
	}, 200)
	body := map[string]string{"username": "boss", "password": "team-admin-secret"}
	if code, _, _ := e.call(http.DefaultClient, "POST", "/admin/session", body, "", ""); code != 401 {
		t.Fatalf("token login survived the claim: %d", code)
	}
	// The operator name the deployment used before the claim is not a way back in
	// either.
	legacy := map[string]string{"username": "admin", "password": "team-admin-secret"}
	if code, _, _ := e.call(http.DefaultClient, "POST", "/admin/session", legacy, "", ""); code != 401 {
		t.Fatalf("the old operator name still signs in: %d", code)
	}
}

// Re-running the claim is how the owner changes their own username or password,
// and it always costs the current token — a hijacked session must not be able to
// swap the credential.
func TestOperatorClaimRequiresTheTokenAndReplacesTheCredential(t *testing.T) {
	e := newTeamTestEnv(t)
	// A wrong token never reaches the handler: the admin wall rejects an
	// unauthenticated request first, which is the stronger answer.
	if code, _, _ := e.call(http.DefaultClient, "POST", "/admin/operator/claim",
		map[string]string{"username": "boss", "password": "boss-password-123", "token": "wrong"}, "", ""); code != 401 {
		t.Fatalf("claim without the token: %d", code)
	}
	// An empty password is refused before anything is written. A short one is
	// accepted on purpose: hashTeamPassword documents that the operator decides
	// their own deployment's strength, so this endpoint must not invent a floor
	// that the rest of the module does not have.
	if code, _, _ := e.call(http.DefaultClient, "POST", "/admin/operator/claim",
		map[string]string{"username": "boss", "password": "", "token": "team-admin-secret"}, "team-admin-secret", ""); code != 400 {
		t.Fatalf("claim with an empty password: %d", code)
	}

	e.admin("POST", "/admin/operator/claim", map[string]string{
		"username": "boss", "password": "boss-password-123", "token": "team-admin-secret",
	}, 200)
	e.admin("POST", "/admin/operator/claim", map[string]string{
		"username": "chief", "password": "chief-password-123", "token": "team-admin-secret",
	}, 200)

	if code, _, _ := e.call(http.DefaultClient, "POST", "/admin/session",
		map[string]string{"username": "boss", "password": "boss-password-123"}, "", ""); code != 401 {
		t.Fatalf("the replaced credential still signs in: %d", code)
	}
	if code, _, _ := e.call(http.DefaultClient, "POST", "/admin/session",
		map[string]string{"username": "chief", "password": "chief-password-123"}, "", ""); code != 200 {
		t.Fatalf("the replacement credential does not sign in: %d", code)
	}
	var count int
	if err := e.db.QueryRow(`SELECT count(*) FROM team_users WHERE role='owner' AND status='active'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("owner rows = %d, want exactly one identity", count)
	}
}

// Claiming a name a member already uses would shadow that account (the unique
// index would reject the insert anyway; the operator should get a plain answer).
func TestOperatorClaimRefusesAMembersName(t *testing.T) {
	e := newTeamTestEnv(t)
	e.enable()
	e.member("alice", "member")
	if code, raw, _ := e.call(http.DefaultClient, "POST", "/admin/operator/claim",
		map[string]string{"username": "alice", "password": "alice-password-123", "token": "team-admin-secret"},
		"team-admin-secret", ""); code != 409 {
		t.Fatalf("claim over a member name: %d %s", code, raw)
	}
}

// The documented way back in when the owner password is lost.
func TestBreakGlassReopensTokenLogin(t *testing.T) {
	e := newTeamTestEnv(t)
	e.admin("POST", "/admin/operator/claim", map[string]string{
		"username": "boss", "password": "boss-password-123", "token": "team-admin-secret",
	}, 200)

	closed := &sessionHandler{db: e.db, adminTokens: []string{"team-admin-secret"}, adminUsername: "admin", sessionKey: []byte("key")}
	if closed.tokenLoginAllowed() {
		t.Fatal("token login stayed open after the claim")
	}
	open := &sessionHandler{db: e.db, adminTokens: []string{"team-admin-secret"}, adminUsername: "admin", sessionKey: []byte("key"), tokenLoginBreakGlass: "break-glass"}
	if !open.tokenLoginAllowed() {
		t.Fatal("break-glass did not reopen the token path")
	}
}

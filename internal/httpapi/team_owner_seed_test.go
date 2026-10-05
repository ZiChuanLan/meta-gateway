package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSwitchingToTeamSeedsTheOwnerFromTheDeploymentAdmin covers the flow an
// operator actually takes: flip team mode on, without inventing a second account
// for themselves. They are already signed in as the deployment administrator, so
// that identity becomes the owner.
//
// Before this, the switch was gated on an owner existing, so the operator had to
// fill in a username/name/password form for a second account — and because the
// operator's own name is reserved, they could not even reuse it (a real
// deployment ended up with an owner called "admin1" next to an operator called
// "admin").
func TestSwitchingToTeamSeedsTheOwnerFromTheDeploymentAdmin(t *testing.T) {
	e := newTeamTestEnv(t)

	// No owner exists yet. The switch itself is what creates one.
	e.setMode("team")

	var username, role, hash string
	if err := e.db.QueryRow(`SELECT username,role,password_hash FROM team_users`).Scan(&username, &role, &hash); err != nil {
		t.Fatalf("owner row: %v", err)
	}
	if username != "admin" || role != "owner" {
		t.Fatalf("seeded owner = %q/%q, want admin/owner", username, role)
	}

	// The deployment admin token is the seeded password, so the operator signs in
	// with the same secret they already have.
	if !checkTeamPassword(hash, "team-admin-secret") {
		t.Fatal("seeded password is not the deployment admin token")
	}

	// The name reservation is cleared: the operator and the owner are now one
	// identity, and the trigger that keeps team accounts from shadowing the
	// operator credential is exactly what would have rejected the insert.
	prefs, err := e.db.OperatorPreferences()
	if err != nil {
		t.Fatal(err)
	}
	if prefs.AdminUsername != "" {
		t.Fatalf("admin_username = %q, want it cleared once the operator is the owner", prefs.AdminUsername)
	}

	// And the account is real: logging in by name resolves to the owner.
	code, raw, _ := e.call(http.DefaultClient, "POST", "/admin/session",
		map[string]string{"username": "admin", "password": "team-admin-secret"}, "", "")
	if code != 200 {
		t.Fatalf("owner login: %d %s", code, raw)
	}
	if !strings.Contains(string(raw), `"role":"owner"`) {
		t.Fatalf("login did not resolve to the owner: %s", raw)
	}

	// Switching away and back must not seed a second owner.
	e.setMode("personal")
	e.setMode("team")
	var owners int
	if err := e.db.QueryRow(`SELECT count(*) FROM team_users WHERE role='owner'`).Scan(&owners); err != nil {
		t.Fatal(err)
	}
	if owners != 1 {
		t.Fatalf("owner rows = %d, want 1", owners)
	}
}

// TestSwitchingToTeamRefusesWithoutAnAdminSecret keeps the fallback honest: with
// nothing to derive a first password from, the switch still refuses, and the
// explicit bootstrap form is how the operator gets through.
func TestSwitchingToTeamRefusesWithoutAnAdminSecret(t *testing.T) {
	e := newTeamTestEnv(t)
	h := NewTeamHandler(e.db, e.enc)
	h.deploymentAdmin = func() (string, string) { return "", "" }

	req := httptest.NewRequest(http.MethodPatch, "/admin/mode", strings.NewReader(`{"mode":"team"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.patchMode(w, req)

	if w.Code != 409 || !strings.Contains(w.Body.String(), "create_owner_first") {
		t.Fatalf("switch without an admin secret: %d %s", w.Code, w.Body.String())
	}
	var users int
	if err := e.db.QueryRow(`SELECT count(*) FROM team_users`).Scan(&users); err != nil {
		t.Fatal(err)
	}
	if users != 0 {
		t.Fatalf("refused switch still created %d user(s)", users)
	}
}

// TestPasswordMinimumIsGone pins the removal of the length floor. A deployment's
// admin token is routinely shorter than the old 10-character minimum (the
// deployment that prompted this had a 6-character one), and it has to be usable
// as the owner's first password. An empty password is still refused.
func TestPasswordMinimumIsGone(t *testing.T) {
	e := newTeamTestEnv(t)
	e.admin("POST", "/admin/team/bootstrap",
		map[string]any{"username": "owner", "name": "Owner", "password": "abc123"}, 201)

	var hash string
	if err := e.db.QueryRow(`SELECT password_hash FROM team_users WHERE username='owner'`).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if !checkTeamPassword(hash, "abc123") {
		t.Fatal("six-character password was not stored correctly")
	}

	empty := newTeamTestEnv(t)
	code, raw, _ := empty.call(http.DefaultClient, "POST", "/admin/team/bootstrap",
		map[string]any{"username": "owner", "name": "Owner", "password": ""}, "team-admin-secret", "")
	if code != 400 || !strings.Contains(string(raw), "password_length") {
		t.Fatalf("empty password: %d %s", code, raw)
	}
}

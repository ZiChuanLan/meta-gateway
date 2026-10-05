package backup

import (
	"path/filepath"
	"testing"

	"github.com/lan/meta-gateway/internal/store"
)

func TestRestoreDoesNotResurrectTeamSessionsOrInvitations(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`INSERT INTO team_users(username,name,password_hash,role,policy_id) VALUES('owner','Owner','test-hash','owner',1);
		INSERT INTO team_sessions(token_hash,user_id,version,created_at,expires_at) VALUES('session-hash',1,1,1,9999999999);
		INSERT INTO team_invites(token_hash,policy_id,expires_at) VALUES('invite-hash',1,9999999999)`); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "backups")
	record, err := New(db, dir).Create(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	if _, err = Restore(target, dir, record.Name); err != nil {
		t.Fatal(err)
	}
	restored, err := store.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	var sessions, invites, users int
	if err = restored.QueryRow(`SELECT count(*) FROM team_sessions`).Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if err = restored.QueryRow(`SELECT count(*) FROM team_invites WHERE revoked=0`).Scan(&invites); err != nil {
		t.Fatal(err)
	}
	if err = restored.QueryRow(`SELECT count(*) FROM team_users`).Scan(&users); err != nil {
		t.Fatal(err)
	}
	if sessions != 0 || invites != 0 || users != 1 {
		t.Fatalf("restored sessions=%d invitations=%d users=%d", sessions, invites, users)
	}
}

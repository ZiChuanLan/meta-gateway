package httpapi

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lan/meta-gateway/internal/backup"
	"github.com/lan/meta-gateway/internal/store"
)

// The console decides from these fields whether it may offer an install and what
// it may promise. In watchtower mode the executor updates the deployment's
// floating tag — not the release the console named — so the tag, and the channel
// that tag delivers, have to reach the browser. Until they did, the dialog could
// offer a beta on a `latest`-tracked deployment, get a refusal it did not
// explain, or (worse) watch for a version the executor would never bring up.
//
// This payload is now the only place they come from: the console has no channel
// switch, because the channel is the deployment's tag and only the deployment
// file can change it.
func TestSelfUpdateStatusExposesTheTrackedTag(t *testing.T) {
	// A closed local port, so the watchtower probe fails instantly instead of
	// waiting on DNS for a compose hostname that does not exist here.
	t.Setenv("WATCHTOWER_URL", "127.0.0.1:1")
	t.Setenv("SELFUPDATE_TRACK_TAG", "beta")
	e := newTeamTestEnv(t)

	var status struct {
		Running         bool   `json:"running"`
		Phase           string `json:"phase"`
		TrackingTag     string `json:"tracking_tag"`
		TrackingChannel string `json:"tracking_channel"`
		From            string `json:"from"`
	}
	if err := json.Unmarshal(e.admin("GET", "/admin/self-update", nil, 200), &status); err != nil {
		t.Fatal(err)
	}
	if status.TrackingTag != "beta" || status.TrackingChannel != "beta" {
		t.Fatalf("track=%q/%q, want beta/beta", status.TrackingTag, status.TrackingChannel)
	}
	if status.Running || status.Phase != "idle" {
		t.Fatalf("no task is running: %+v", status)
	}
	if status.From != "" {
		t.Fatalf("from=%q, want empty while idle", status.From)
	}
}

// An upgrade runs migrations, and migrations are one-way. The click has to take
// the snapshot itself: asking an operator to copy a volume first is how a
// one-click update turns into a documentation exercise, and the deployments that
// need it most are the ones least able to follow the instructions.
func TestPreUpdateBackupSnapshotsAndVerifiesTheDatabase(t *testing.T) {
	db, err := store.OpenTest(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	dir := t.TempDir()
	h := NewSelfUpdateHandler(nil, nil, backup.NewWithRetention(db, dir, 5), db)

	name, err := h.preUpdateBackup(context.Background())
	if err != nil {
		t.Fatalf("preUpdateBackup: %v", err)
	}
	if name == "" {
		t.Fatal("no snapshot name returned")
	}
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("snapshot %s is not on disk: %v", name, err)
	}
	// The file the operator would restore from must be a complete database, not
	// a truncated copy taken mid-write.
	if err := backup.Verify(path); err != nil {
		t.Fatalf("snapshot does not verify: %v", err)
	}
	// And it must be recorded, because the console lists snapshots from the table.
	records, err := db.BackupRecord.List(5)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Name != name {
		t.Fatalf("records=%+v, want the new snapshot", records)
	}
}

// No directory means no rollback, so the update must not start. The message has
// to name the variable: an operator who has never read the compose file cannot
// act on "backup failed".
func TestPreUpdateBackupRefusesWithoutADirectory(t *testing.T) {
	db, err := store.OpenTest(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	h := NewSelfUpdateHandler(nil, nil, backup.NewWithRetention(db, "", 5), db)
	_, err = h.preUpdateBackup(context.Background())
	if err == nil {
		t.Fatal("an empty backup directory was accepted")
	}
	if !strings.Contains(err.Error(), "BACKUP_DIR") {
		t.Fatalf("error %q does not name BACKUP_DIR", err)
	}
}

// A rejected request must not litter the snapshot directory: the console can
// retry a check freely, and every attempt would otherwise leave a copy of the
// database behind.
func TestRejectedUpdateLeavesNoSnapshot(t *testing.T) {
	e := newTeamTestEnv(t)
	// The running build is "dev" in tests, so this target is not newer.
	e.admin("POST", "/admin/self-update/apply", map[string]string{"target": "v0.0.1"}, 400)
	entries, err := os.ReadDir(e.backupDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("a rejected request left %d file(s) in the backup directory", len(entries))
	}
}

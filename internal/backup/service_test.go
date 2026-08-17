package backup

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

func TestOnlineBackupAndOfflineRestore(t *testing.T) {
	dataDir := t.TempDir()
	backupDir := filepath.Join(t.TempDir(), "backups")
	db, err := store.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	routeID, err := db.Route.Create(&domain.Route{ModelPattern: "manual-model", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	channelID, err := db.Channel.Create(&domain.Channel{Name: "manual", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.RouteMember.Create(&domain.RouteMember{RouteID: routeID, ChannelID: channelID, Enabled: true, Auto: false, ManualOverride: true}); err != nil {
		t.Fatal(err)
	}
	if err := db.AuditEvent.Insert(&store.AuditEvent{ActorKind: "admin", Action: "route.create", Outcome: "success", StatusCode: 201}); err != nil {
		t.Fatal(err)
	}

	service := New(db, backupDir)
	record, err := service.Create(t.Context())
	if err != nil {
		t.Fatalf("create backup: %v", err)
	}
	safeName := regexp.MustCompile(`^meta-gateway-[0-9]{8}T[0-9]{6}Z-[0-9a-f]{12}\.db$`)
	if record.Status != "success" || record.SizeBytes <= 0 || len(record.Checksum) != 64 || !safeName.MatchString(record.Name) {
		t.Fatalf("record=%+v", record)
	}
	if _, err := db.Route.Create(&domain.Route{ModelPattern: "after-backup", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	restoreDir := t.TempDir()
	rollback, err := Restore(restoreDir, backupDir, record.Name)
	if err != nil || rollback != "" {
		t.Fatalf("restore rollback=%q err=%v", rollback, err)
	}
	restored, err := store.Open(restoreDir)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	routes, err := restored.Route.List()
	if err != nil || len(routes) != 1 || routes[0].ModelPattern != "manual-model" {
		t.Fatalf("routes=%+v err=%v", routes, err)
	}
	members, err := restored.RouteMember.ListByRoute(routes[0].ID)
	if err != nil || len(members) != 1 || members[0].Auto || !members[0].ManualOverride {
		t.Fatalf("members=%+v err=%v", members, err)
	}
	events, err := restored.AuditEvent.List(10, 0)
	if err != nil || len(events) != 1 || events[0].Action != "route.create" {
		t.Fatalf("events=%+v err=%v", events, err)
	}
}

func TestRestoreRejectsUnsafeOrCorruptBackup(t *testing.T) {
	backupDir := t.TempDir()
	for _, name := range []string{"../meta-gateway-20260714T120000Z-0123456789ab.db", "arbitrary.db"} {
		if _, err := Restore(t.TempDir(), backupDir, name); err == nil {
			t.Fatalf("accepted unsafe name %q", name)
		}
	}
	name := "meta-gateway-20260714T120000Z-0123456789ab.db"
	if err := os.WriteFile(filepath.Join(backupDir, name), []byte("not sqlite"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(t.TempDir(), backupDir, name); err == nil {
		t.Fatal("accepted corrupt backup")
	}
}

func TestBackupRetentionPrunesFilesAndHistory(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	backupDir := filepath.Join(t.TempDir(), "backups")
	service := NewWithRetention(db, backupDir, 2)
	for i := 0; i < 3; i++ {
		if _, err := service.Create(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatal(err)
	}
	files := 0
	for _, entry := range entries {
		if safeName.MatchString(entry.Name()) {
			files++
		}
	}
	if files != 2 {
		t.Fatalf("retained files=%d, want 2", files)
	}
	records, err := service.List(100)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("retained records=%d, want 2", len(records))
	}
}

func TestBackupRetentionDoesNotEvictRestorableRecordsForNewerFailures(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service := NewWithRetention(db, filepath.Join(t.TempDir(), "backups"), 2)
	for i := 0; i < 2; i++ {
		if _, err := service.Create(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	failed := &store.BackupRecord{Name: "failed-newer-attempt.db", Status: "failed", Category: "snapshot"}
	if err := db.BackupRecord.Insert(failed); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Cleanup(); err != nil {
		t.Fatal(err)
	}
	records, err := service.List(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("retained records=%d, want 2", len(records))
	}
	for _, record := range records {
		if record.Status != "success" {
			t.Fatalf("newer failure evicted a restorable backup record: %+v", records)
		}
		if _, err := os.Stat(filepath.Join(service.dir, record.Name)); err != nil {
			t.Fatalf("retained record has no backup file: %s: %v", record.Name, err)
		}
	}
}

func TestRestoreMovesActiveWALAlongsideRollback(t *testing.T) {
	sourceDir := t.TempDir()
	dataDir := t.TempDir()
	db, err := store.Open(sourceDir)
	if err != nil {
		t.Fatal(err)
	}
	service := New(db, filepath.Join(t.TempDir(), "backups"))
	record, err := service.Create(t.Context())
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	active := filepath.Join(dataDir, "meta-gateway.db")
	if err := copyExclusive(filepath.Join(service.dir, record.Name), active); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(active+"-wal", []byte("sidecar"), 0o600); err != nil {
		t.Fatal(err)
	}
	rollback, err := Restore(dataDir, service.dir, record.Name)
	if err != nil {
		t.Fatal(err)
	}
	if rollback == "" {
		t.Fatal("expected rollback path")
	}
	if _, err := os.Stat(rollback + "-wal"); err != nil {
		t.Fatalf("rollback WAL sidecar not preserved: %v", err)
	}
	if _, err := os.Stat(active + "-wal"); !os.IsNotExist(err) {
		t.Fatalf("active WAL sidecar should not remain, err=%v", err)
	}
}

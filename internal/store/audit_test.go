package store

import (
	"testing"
	"time"
)

func TestAuditEventListAndCleanup(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, action := range []string{"site.create", "channel.update", "route.delete"} {
		if err := db.AuditEvent.Insert(&AuditEvent{ActorKind: "admin", Action: action, Outcome: "success", StatusCode: 200}); err != nil {
			t.Fatal(err)
		}
	}
	events, err := db.AuditEvent.List(2, 0)
	if err != nil || len(events) != 2 || events[0].ID <= events[1].ID {
		t.Fatalf("events=%v err=%v", events, err)
	}
	removed, err := db.AuditEvent.Cleanup(time.Now(), 0, 1)
	if err != nil || removed != 2 {
		t.Fatalf("removed=%d err=%v", removed, err)
	}
	events, _ = db.AuditEvent.List(100, 0)
	if len(events) != 1 || events[0].Action != "route.delete" {
		t.Fatalf("events=%v", events)
	}
}

func TestAuditEventCleanupAppliesAgeThenCount(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	for i, createdAt := range []time.Time{now.AddDate(0, 0, -91), now.Add(-time.Hour), now.Add(-time.Minute)} {
		result, err := db.Exec(`INSERT INTO audit_events
            (actor_kind, action, outcome, status_code, created_at) VALUES ('system', ?, 'success', 200, ?)`,
			"event."+string(rune('a'+i)), createdAt.Format(time.RFC3339Nano))
		if err != nil {
			t.Fatal(err)
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			t.Fatalf("inserted rows=%d", affected)
		}
	}
	removed, err := db.AuditEvent.Cleanup(now, 90, 1)
	if err != nil || removed != 2 {
		t.Fatalf("removed=%d err=%v", removed, err)
	}
	events, err := db.AuditEvent.List(100, 0)
	if err != nil || len(events) != 1 || events[0].Action != "event.c" {
		t.Fatalf("events=%v err=%v", events, err)
	}
}

func TestAuditEventCleanupNormalizesSQLiteDatetimeFormat(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	if _, err := db.Exec(`INSERT INTO audit_events
		(actor_kind, action, outcome, status_code, created_at)
		VALUES ('system', 'old', 'success', 200, '2026-07-12 11:59:59')`); err != nil {
		t.Fatal(err)
	}
	removed, err := db.AuditEvent.Cleanup(now, 1, 0)
	if err != nil || removed != 1 {
		t.Fatalf("removed=%d err=%v", removed, err)
	}
}

func TestP7MigrationCreatesOperationalTables(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, table := range []string{"audit_events", "backup_records"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("table %s count=%d err=%v", table, count, err)
		}
	}
}

func TestBackupRecordList(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.BackupRecord.Insert(&BackupRecord{Name: "backup.db", Status: "success", SizeBytes: 42, Checksum: "abc"}); err != nil {
		t.Fatal(err)
	}
	records, err := db.BackupRecord.List(10)
	if err != nil || len(records) != 1 || records[0].Name != "backup.db" {
		t.Fatalf("records=%v err=%v", records, err)
	}
}

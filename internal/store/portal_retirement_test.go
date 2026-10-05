package store_test

import (
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

func TestRetiredPortalDataSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	keyID, err := db.DownstreamKey.Create(&domain.DownstreamKey{
		Name: "existing-key", TokenHash: "retirement-test-hash",
		Enabled: true, Scopes: "relay", QuotaTotalTokens: 1000, QuotaUsedTokens: 42,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Historical credentials are inert, not erased by removing their UI.
	if _, err := db.Exec(`INSERT INTO portal_credentials
		(key_id, kind, subject, label, created_at) VALUES (?, 'github', '123', 'existing-binding', '2026-10-02T00:00:00Z')`, keyID); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	key, err := reopened.DownstreamKey.GetByID(keyID)
	if err != nil || key == nil {
		t.Fatalf("existing key lost: key=%v err=%v", key, err)
	}
	if key.Name != "existing-key" || !key.Enabled || key.QuotaTotalTokens != 1000 || key.QuotaUsedTokens != 42 {
		t.Fatalf("existing key changed: %+v", key)
	}
	var subject, label string
	if err := reopened.QueryRow(`SELECT subject, label FROM portal_credentials WHERE key_id = ?`, keyID).Scan(&subject, &label); err != nil {
		t.Fatal(err)
	}
	if subject != "123" || label != "existing-binding" {
		t.Fatalf("historical binding changed: subject=%q label=%q", subject, label)
	}
	var count int
	if err := reopened.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE name = '107_portal_credentials.sql'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("historical migration count=%d err=%v", count, err)
	}
}

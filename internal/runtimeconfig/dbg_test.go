package runtimeconfig

import (
	"testing"

	"github.com/lan/meta-gateway/internal/store"
)

func TestDbgBootstrap(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	_ = db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count)
	t.Logf("migrations=%d", count)
	row, err := db.RuntimeSettings.Get()
	if err != nil {
		t.Logf("Get err: %v", err)
	}
	t.Logf("row=%+v", row)
}

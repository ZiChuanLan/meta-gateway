package store_test

import (
	"strings"
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

// TestOpenTestDatabasesAreIsolated proves that the template copy gives each test
// its own database.
//
// This is the test that matters most for OpenTest: a shared-state bug would not
// fail loudly. It would let unrelated tests pass on each other's fixtures, which
// is strictly worse than the slow suite OpenTest replaces. Both directions are
// asserted, because a copy that shared the template file would show up as the
// first database's rows appearing in later ones.
func TestOpenTestDatabasesAreIsolated(t *testing.T) {
	first, err := store.OpenTest(t.TempDir())
	if err != nil {
		t.Fatalf("open first: %v", err)
	}
	defer first.Close()

	if _, err := first.Site.Create(&domain.Site{
		Name: "only-in-first", BaseURL: "https://first.example", Platform: "new-api", Status: domain.StatusEnabled,
	}); err != nil {
		t.Fatalf("create in first: %v", err)
	}

	// A later database must not see it, through the file or through the store's
	// in-process cache.
	second, err := store.OpenTest(t.TempDir())
	if err != nil {
		t.Fatalf("open second: %v", err)
	}
	defer second.Close()

	sites, err := second.Site.List()
	if err != nil {
		t.Fatalf("list second: %v", err)
	}
	if len(sites) != 0 {
		t.Fatalf("second database sees %d site(s) written by the first: %+v", len(sites), sites)
	}

	// And the first database still has exactly its own row: the second open must
	// not have written back into the template.
	sites, err = first.Site.List()
	if err != nil {
		t.Fatalf("list first: %v", err)
	}
	if len(sites) != 1 || sites[0].Name != "only-in-first" {
		t.Fatalf("first database changed: %+v", sites)
	}

	// A third open proves the template itself was never mutated.
	third, err := store.OpenTest(t.TempDir())
	if err != nil {
		t.Fatalf("open third: %v", err)
	}
	defer third.Close()
	if sites, err := third.Site.List(); err != nil || len(sites) != 0 {
		t.Fatalf("template was mutated: sites=%+v err=%v", sites, err)
	}
}

// TestOpenTestRefusesAPopulatedDirectory pins the guard that turns the one
// dangerous mistake — pointing OpenTest at a directory a test has already
// populated, which would overwrite its fixture — into a loud failure.
func TestOpenTestRefusesAPopulatedDirectory(t *testing.T) {
	dir := t.TempDir()

	db, err := store.OpenTest(dir)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	if _, err := db.Site.Create(&domain.Site{
		Name: "fixture", BaseURL: "https://fixture.example", Platform: "new-api", Status: domain.StatusEnabled,
	}); err != nil {
		t.Fatalf("create fixture: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if _, err := store.OpenTest(dir); err == nil {
		t.Fatal("OpenTest overwrote a populated directory; it must refuse")
	} else if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("unexpected error, want the reopen guard: %v", err)
	}

	// The fixture survives, which is the whole point of the guard.
	reopened, err := store.Open(dir)
	if err != nil {
		t.Fatalf("reopen with Open: %v", err)
	}
	defer reopened.Close()
	sites, err := reopened.Site.List()
	if err != nil || len(sites) != 1 || sites[0].Name != "fixture" {
		t.Fatalf("fixture was destroyed: sites=%+v err=%v", sites, err)
	}
}

// TestOpenTestCarriesEveryMigration guards the assumption OpenTest makes when it
// skips Migrate: the template must be fully migrated. If it were not, every
// converted test would fail on a missing table, and this test says so directly.
func TestOpenTestCarriesEveryMigration(t *testing.T) {
	db, err := store.OpenTest(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	var applied int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&applied); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if applied == 0 {
		t.Fatal("template has no recorded migrations; OpenTest's skip-Migrate assumption is broken")
	}

	// Running Migrate on top must be a no-op, which is the other half of the
	// assumption: the copy is already at the current schema version.
	if err := store.Migrate(db.DB); err != nil {
		t.Fatalf("migrate on a template copy: %v", err)
	}
	var after int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&after); err != nil {
		t.Fatalf("count after migrate: %v", err)
	}
	if after != applied {
		t.Fatalf("Migrate applied %d extra migration(s) on a template copy", after-applied)
	}
}

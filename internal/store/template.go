package store

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// databaseFileName is the SQLite file inside a data directory. It is a constant
// because OpenTest has to name the same file sqliteDSN does.
const databaseFileName = "meta-gateway.db"

// template holds the once-per-process migrated database that OpenTest copies.
var (
	templateOnce sync.Once
	templateDir  string
	templateErr  error
)

// OpenTest opens a migrated database for a test by copying a template that is
// migrated once per process, instead of replaying every migration per test.
//
// Why this exists: the race suite's cost is almost entirely migration replay.
// Every test that opens a fresh database replays all of them, which measures
// ~0.14s without -race and ~3.4s with it — modernc.org/sqlite is pure Go, so the
// race detector instruments every SQLite call. With ~300 call sites that is the
// difference between a ~4 minute suite and a ~21 minute one, and the 20-minute
// per-package timeout that the longer one eventually hit.
//
// Migrate is deliberately skipped on the copy: the template is migrated by
// construction, so re-checking 123 rows is pure overhead. If that assumption
// ever breaks it fails loudly (the first query against a missing table) rather
// than silently running an unmigrated database.
//
// dataDir must not already contain a database. That guard is not politeness:
// the dangerous mistake this helper invites is converting a test that *reopens*
// a directory it has already populated, and copying a template over that would
// destroy its fixture and turn a passing test into a confusing one.
//
// This lives in the production package rather than an importable helper because
// internal/store's own tests are split across `package store` and
// `package store_test`, and only a symbol in `store` is reachable from both.
// httpapi.NewTestRouter is the same shape for the same reason.
func OpenTest(dataDir string) (*DB, error) {
	dir, err := testTemplate()
	if err != nil {
		return nil, err
	}
	if err := copyTemplateDatabase(dir, dataDir); err != nil {
		return nil, err
	}
	return openWithMaxConns(dataDir, DefaultMaxOpenConns, false)
}

// testTemplate returns the directory holding the migrated template, building it
// on the first call.
func testTemplate() (string, error) {
	templateOnce.Do(func() {
		dir, err := os.MkdirTemp("", "meta-gateway-store-template-")
		if err != nil {
			templateErr = fmt.Errorf("store: create test template dir: %w", err)
			return
		}
		// Open (not OpenTest) so this one runs the real migrations.
		db, err := Open(dir)
		if err != nil {
			templateErr = fmt.Errorf("store: build test template: %w", err)
			return
		}
		// Checkpoint so the template is one self-contained file where possible.
		// The copy handles a surviving -wal anyway, so a failure here is not
		// fatal; it only means the copy carries two files instead of one.
		_, _ = db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
		if err := db.Close(); err != nil {
			templateErr = fmt.Errorf("store: close test template: %w", err)
			return
		}
		templateDir = dir
	})
	return templateDir, templateErr
}

// copyTemplateDatabase copies the template database into dataDir.
//
// The template is closed and never written again, so a plain file copy is
// consistent. -shm is deliberately not copied: it is shared memory that SQLite
// rebuilds from the -wal, and a stale copy of it is worse than none.
func copyTemplateDatabase(templateDir, dataDir string) error {
	destination := filepath.Join(dataDir, databaseFileName)
	if _, err := os.Stat(destination); err == nil {
		return fmt.Errorf(
			"store: OpenTest: %s already exists — a test that reopens a populated directory must use Open, not OpenTest",
			destination)
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return fmt.Errorf("store: OpenTest: %w", err)
	}
	for _, suffix := range []string{"", "-wal"} {
		if err := copyFile(filepath.Join(templateDir, databaseFileName+suffix), destination+suffix); err != nil {
			return fmt.Errorf("store: OpenTest: copy %s: %w", databaseFileName+suffix, err)
		}
	}
	return nil
}

// copyFile copies src to dst, reporting a missing src as no error so the caller
// can try optional siblings such as a -wal that may not exist.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

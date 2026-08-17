package backup

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/lan/meta-gateway/internal/store"
	"modernc.org/sqlite"
)

var safeName = regexp.MustCompile(`^meta-gateway-[0-9]{8}T[0-9]{6}Z-[0-9a-f]{12}\.db$`)

const DefaultRetentionCount = 30

type Service struct {
	db     *store.DB
	dir    string
	retain int
	mu     sync.Mutex
}

func New(db *store.DB, dir string) *Service {
	return NewWithRetention(db, dir, DefaultRetentionCount)
}

// NewWithRetention allows deployments and tests to choose a bounded number of
// on-disk snapshots. A non-positive value disables automatic pruning.
func NewWithRetention(db *store.DB, dir string, retain int) *Service {
	return &Service{db: db, dir: dir, retain: retain}
}

func (s *Service) Create(ctx context.Context) (*store.BackupRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	started := time.Now()
	name, err := generatedName(started)
	if err != nil {
		return nil, fmt.Errorf("backup generation failed: %w", err)
	}
	record := &store.BackupRecord{Name: name, Status: "failed"}
	if err := ensureDirectory(s.dir); err != nil {
		record.Category = "directory"
		_ = s.record(record, started)
		return nil, fmt.Errorf("backup failed: %w", err)
	}
	temporary := filepath.Join(s.dir, "."+name+".tmp")
	final := filepath.Join(s.dir, name)
	defer os.Remove(temporary)
	if err := onlineCopy(ctx, s.db.DB, temporary); err != nil {
		record.Category = "snapshot"
		_ = s.record(record, started)
		return nil, fmt.Errorf("backup failed: %w", err)
	}
	if err := os.Chmod(temporary, 0o600); err != nil {
		record.Category = "permissions"
		_ = s.record(record, started)
		return nil, fmt.Errorf("backup failed: %w", err)
	}
	if err := Verify(temporary); err != nil {
		record.Category = "integrity"
		_ = s.record(record, started)
		return nil, fmt.Errorf("backup failed: %w", err)
	}
	info, checksum, err := fileMetadata(temporary)
	if err != nil {
		record.Category = "metadata"
		_ = s.record(record, started)
		return nil, fmt.Errorf("backup failed: %w", err)
	}
	if _, err := os.Lstat(final); !errors.Is(err, os.ErrNotExist) {
		record.Category = "collision"
		_ = s.record(record, started)
		return nil, fmt.Errorf("backup failed: %w", err)
	}
	if err := os.Rename(temporary, final); err != nil {
		record.Category = "install"
		_ = s.record(record, started)
		return nil, fmt.Errorf("backup failed: %w", err)
	}
	record.Status, record.SizeBytes, record.Checksum = "success", info.Size(), checksum
	if err := s.record(record, started); err != nil {
		_ = os.Remove(final)
		return nil, fmt.Errorf("backup failed: %w", err)
	}
	if _, cleanupErr := s.cleanupLocked(); cleanupErr != nil {
		// The newly-created verified backup remains usable even if housekeeping
		// encounters a permissions or transient database error. Log and retry on
		// the next backup instead of reporting a false creation failure.
		log.Printf("backup retention cleanup failed: %v", cleanupErr)
	}
	return record, nil
}

func (s *Service) List(limit int) ([]store.BackupRecord, error) { return s.db.BackupRecord.List(limit) }

func (s *Service) record(record *store.BackupRecord, started time.Time) error {
	record.DurationMs = time.Since(started).Milliseconds()
	return s.db.BackupRecord.Insert(record)
}

// Cleanup prunes old verified backup files and their history rows. It is safe
// to call independently (for example from an operator maintenance command).
func (s *Service) Cleanup() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cleanupLocked()
}

func (s *Service) cleanupLocked() (int, error) {
	if s.retain <= 0 {
		return 0, nil
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	files := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if !safeName.MatchString(entry.Name()) {
			continue
		}
		info, infoErr := os.Lstat(filepath.Join(s.dir, entry.Name()))
		if infoErr != nil || !info.Mode().IsRegular() {
			continue
		}
		files[entry.Name()] = struct{}{}
	}
	records, err := s.db.BackupRecord.ListSuccessful()
	if err != nil {
		return 0, err
	}
	keep := make(map[string]struct{}, s.retain)
	for _, record := range records {
		if len(keep) >= s.retain {
			break
		}
		if _, exists := files[record.Name]; exists {
			keep[record.Name] = struct{}{}
		}
	}
	var errs []error
	removed := 0
	for name := range files {
		if _, retained := keep[name]; retained {
			continue
		}
		path := filepath.Join(s.dir, name)
		removeErr := os.Remove(path)
		if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			errs = append(errs, fmt.Errorf("remove %s: %w", name, removeErr))
			continue
		}
		removed++
		if err := s.db.BackupRecord.DeleteByName(name); err != nil {
			errs = append(errs, fmt.Errorf("delete backup record %s: %w", name, err))
		}
	}
	// Drop success rows whose files were already missing. A row for a file that
	// failed deletion above is deliberately preserved so the operator can still
	// see and restore it.
	for _, record := range records {
		if _, existed := files[record.Name]; existed {
			continue
		}
		if err := s.db.BackupRecord.DeleteByName(record.Name); err != nil {
			errs = append(errs, fmt.Errorf("delete missing backup record %s: %w", record.Name, err))
		}
	}
	failedSlots := s.retain - len(keep)
	if _, err := s.db.BackupRecord.PruneNonSuccessful(failedSlots); err != nil {
		errs = append(errs, fmt.Errorf("prune backup failure records: %w", err))
	}
	return removed, errors.Join(errs...)
}

type onlineBackuper interface {
	NewBackup(string) (*sqlite.Backup, error)
}

func onlineCopy(ctx context.Context, source *sql.DB, destination string) error {
	if file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600); err != nil {
		return err
	} else {
		_ = file.Close()
	}
	conn, err := source.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	return conn.Raw(func(driverConn any) error {
		backuper, ok := driverConn.(onlineBackuper)
		if !ok {
			return errors.New("unsupported backup driver")
		}
		operation, err := backuper.NewBackup(destination)
		if err != nil {
			return err
		}
		if _, err := operation.Step(-1); err != nil {
			_ = operation.Finish()
			return err
		}
		return operation.Finish()
	})
}

func Verify(path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("invalid backup file")
	}
	dsn, err := sqliteFileDSN(path, url.Values{"mode": []string{"ro"}})
	if err != nil {
		return errors.New("invalid backup database")
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return errors.New("invalid backup database")
	}
	defer db.Close()
	var result string
	if err := db.QueryRow(`PRAGMA quick_check`).Scan(&result); err != nil || result != "ok" {
		return errors.New("backup integrity check failed")
	}
	foreignKeys, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		return errors.New("backup foreign key check failed")
	}
	defer foreignKeys.Close()
	if foreignKeys.Next() {
		return errors.New("backup foreign key check failed")
	}
	if err := foreignKeys.Err(); err != nil {
		return errors.New("backup foreign key check failed")
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE name = '006_p7_operations.sql'`).Scan(&count); err != nil || count != 1 {
		return errors.New("backup schema check failed")
	}
	return nil
}

func ensureDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("invalid backup directory")
	}
	return os.Chmod(path, 0o700)
}

func generatedName(now time.Time) (string, error) {
	random := make([]byte, 6)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return "meta-gateway-" + now.UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(random) + ".db", nil
}

func fileMetadata(path string) (os.FileInfo, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return nil, "", err
	}
	info, err := file.Stat()
	return info, hex.EncodeToString(hash.Sum(nil)), err
}

func Restore(dataDir, backupDir, name string) (string, error) {
	if !safeName.MatchString(name) || filepath.Base(name) != name {
		return "", errors.New("invalid backup name")
	}
	source := filepath.Join(backupDir, name)
	if err := Verify(source); err != nil {
		return "", errors.New("backup validation failed")
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return "", errors.New("restore preparation failed")
	}
	active := filepath.Join(dataDir, "meta-gateway.db")
	temporary := filepath.Join(dataDir, ".restore.tmp")
	rollback := filepath.Join(dataDir, "meta-gateway.rollback-"+time.Now().UTC().Format("20060102T150405Z")+".db")
	defer os.Remove(temporary)
	if err := copyExclusive(source, temporary); err != nil {
		return "", errors.New("restore preparation failed")
	}
	if err := Verify(temporary); err != nil {
		return "", errors.New("restore validation failed")
	}
	hadActive, err := moveActiveAside(active, rollback)
	if err != nil {
		return "", errors.New("restore install failed")
	}
	if err := os.Rename(temporary, active); err != nil {
		if hadActive {
			_ = restoreActiveAside(active, rollback)
		}
		return "", errors.New("restore install failed")
	}
	if err := Verify(active); err != nil {
		_ = os.Remove(active)
		for _, suffix := range []string{"-wal", "-shm"} {
			_ = os.Remove(active + suffix)
		}
		if hadActive {
			_ = restoreActiveAside(active, rollback)
		}
		return "", errors.New("restore verification failed")
	}
	// A read-only integrity check may create an empty SHM/WAL companion on
	// some SQLite builds. The restored snapshot is self-contained; never leave
	// those transient companions under the active name.
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Remove(active + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			_ = os.Remove(active)
			if hadActive {
				_ = restoreActiveAside(active, rollback)
			}
			return "", errors.New("restore verification cleanup failed")
		}
	}
	if !hadActive {
		rollback = ""
	}
	return rollback, nil
}

// moveActiveAside moves the database and any WAL/SHM sidecars together. A
// database file without its WAL can silently lose committed transactions, and
// leaving old sidecars under the active name can corrupt the newly restored
// file. Keeping the sidecars with the rollback also makes a failed install
// genuinely reversible.
func moveActiveAside(active, rollback string) (bool, error) {
	if _, err := os.Lstat(active); err == nil {
		if err := os.Rename(active, rollback); err != nil {
			return false, err
		}
		for _, suffix := range []string{"-wal", "-shm"} {
			if _, err := os.Lstat(active + suffix); errors.Is(err, os.ErrNotExist) {
				continue
			} else if err != nil {
				_ = restoreActiveAside(active, rollback)
				return false, err
			} else if err := os.Rename(active+suffix, rollback+suffix); err != nil {
				_ = restoreActiveAside(active, rollback)
				return false, err
			}
		}
		return true, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	// There is no active DB; remove stale sidecars so they cannot attach to the
	// restored file.
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Remove(active + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
	}
	return false, nil
}

func restoreActiveAside(active, rollback string) error {
	for _, suffix := range []string{"-wal", "-shm"} {
		_ = os.Remove(active + suffix)
		if err := os.Rename(rollback+suffix, active+suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := os.Rename(rollback, active); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func sqliteFileDSN(path string, values url.Values) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	slash := filepath.ToSlash(abs)
	if filepath.VolumeName(slash) != "" && !strings.HasPrefix(slash, "/") {
		slash = "/" + slash
	}
	u := &url.URL{Scheme: "file", Path: slash}
	u.RawQuery = values.Encode()
	return u.String(), nil
}

func copyExclusive(source, destination string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

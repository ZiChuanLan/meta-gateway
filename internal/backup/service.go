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
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/lan/meta-gateway/internal/store"
	"modernc.org/sqlite"
)

var safeName = regexp.MustCompile(`^meta-gateway-[0-9]{8}T[0-9]{6}Z-[0-9a-f]{12}\.db$`)

type Service struct {
	db  *store.DB
	dir string
	mu  sync.Mutex
}

func New(db *store.DB, dir string) *Service { return &Service{db: db, dir: dir} }

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
	return record, nil
}

func (s *Service) List(limit int) ([]store.BackupRecord, error) { return s.db.BackupRecord.List(limit) }

func (s *Service) record(record *store.BackupRecord, started time.Time) error {
	record.DurationMs = time.Since(started).Milliseconds()
	return s.db.BackupRecord.Insert(record)
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
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		return errors.New("invalid backup database")
	}
	defer db.Close()
	var result string
	if err := db.QueryRow(`PRAGMA quick_check`).Scan(&result); err != nil || result != "ok" {
		return errors.New("backup integrity check failed")
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
	hadActive := false
	if _, err := os.Stat(active); err == nil {
		if err := os.Rename(active, rollback); err != nil {
			return "", errors.New("restore install failed")
		}
		hadActive = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", errors.New("restore install failed")
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		_ = os.Remove(active + suffix)
	}
	if err := os.Rename(temporary, active); err != nil {
		if hadActive {
			_ = os.Rename(rollback, active)
		}
		return "", errors.New("restore install failed")
	}
	if err := Verify(active); err != nil {
		_ = os.Remove(active)
		if hadActive {
			_ = os.Rename(rollback, active)
		}
		return "", errors.New("restore verification failed")
	}
	if !hadActive {
		rollback = ""
	}
	return rollback, nil
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

func SafeName(name string) bool { return safeName.MatchString(name) }

package store

import (
	"database/sql"
	"time"
)

type BackupRecord struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	Status     string    `json:"status"`
	SizeBytes  int64     `json:"size_bytes"`
	Checksum   string    `json:"checksum"`
	DurationMs int64     `json:"duration_ms"`
	Category   string    `json:"category,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

type BackupRecordStore struct{ db *sql.DB }

func (s *BackupRecordStore) Insert(record *BackupRecord) error {
	result, err := s.db.Exec(`INSERT INTO backup_records
        (name, status, size_bytes, checksum, duration_ms, category) VALUES (?, ?, ?, ?, ?, ?)`,
		record.Name, record.Status, record.SizeBytes, record.Checksum, record.DurationMs, record.Category)
	if err != nil {
		return err
	}
	record.ID, err = result.LastInsertId()
	if err != nil {
		return err
	}
	return s.db.QueryRow(`SELECT created_at FROM backup_records WHERE id = ?`, record.ID).Scan(scanTime(&record.CreatedAt))
}

func (s *BackupRecordStore) List(limit int) ([]BackupRecord, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Query(`SELECT id, name, status, size_bytes, checksum, duration_ms, category, created_at
        FROM backup_records ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]BackupRecord, 0)
	for rows.Next() {
		var record BackupRecord
		if err := rows.Scan(&record.ID, &record.Name, &record.Status, &record.SizeBytes,
			&record.Checksum, &record.DurationMs, &record.Category, scanTime(&record.CreatedAt)); err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	return result, rows.Err()
}

// DeleteByName removes a history row after its corresponding file has been
// pruned from disk. Missing rows are treated as success so filesystem cleanup
// can be safely retried.
func (s *BackupRecordStore) DeleteByName(name string) error {
	_, err := s.db.Exec(`DELETE FROM backup_records WHERE name = ?`, name)
	return err
}

// ListSuccessful returns successful snapshots newest first. The backup service
// combines this durable order with actual file existence when choosing what
// to retain, so same-second random filename suffixes cannot reorder history.
func (s *BackupRecordStore) ListSuccessful() ([]BackupRecord, error) {
	rows, err := s.db.Query(`SELECT id, name, status, size_bytes, checksum, duration_ms, category, created_at
		FROM backup_records WHERE status = 'success' ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]BackupRecord, 0)
	for rows.Next() {
		var record BackupRecord
		if err := rows.Scan(&record.ID, &record.Name, &record.Status, &record.SizeBytes,
			&record.Checksum, &record.DurationMs, &record.Category, scanTime(&record.CreatedAt)); err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	return result, rows.Err()
}

// PruneNonSuccessful keeps the newest keep failure/history rows. Successful
// rows are managed together with their files by backup.Service.Cleanup.
func (s *BackupRecordStore) PruneNonSuccessful(keep int) (int64, error) {
	if keep < 0 {
		keep = 0
	}
	result, err := s.db.Exec(`DELETE FROM backup_records
		WHERE status <> 'success' AND id NOT IN
		(SELECT id FROM backup_records WHERE status <> 'success' ORDER BY id DESC LIMIT ?)`, keep)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

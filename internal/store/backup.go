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

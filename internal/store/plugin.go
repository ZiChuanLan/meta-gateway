package store

import (
	"database/sql"
	"time"
)

type PluginRecord struct {
	ID          string     `json:"id"`
	Version     string     `json:"version"`
	Status      string     `json:"status"`
	Enabled     bool       `json:"enabled"`
	Source      string     `json:"source,omitempty"`
	Checksum    string     `json:"checksum,omitempty"`
	InstalledAt *time.Time `json:"installed_at,omitempty"`
	EnabledAt   *time.Time `json:"enabled_at,omitempty"`
	MetaJSON    string     `json:"meta_json,omitempty"`
}

type PluginStore struct{ db *sql.DB }

func (s *PluginStore) List() ([]PluginRecord, error) {
	rows, err := s.db.Query(`SELECT id, version, status, enabled, source, checksum, installed_at, enabled_at, meta_json
		FROM plugins ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]PluginRecord, 0)
	for rows.Next() {
		rec, err := scanPlugin(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (s *PluginStore) Get(id string) (*PluginRecord, error) {
	row := s.db.QueryRow(`SELECT id, version, status, enabled, source, checksum, installed_at, enabled_at, meta_json
		FROM plugins WHERE id = ?`, id)
	rec, err := scanPlugin(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

func (s *PluginStore) Upsert(rec *PluginRecord) error {
	_, err := s.db.Exec(`INSERT INTO plugins
		(id, version, status, enabled, source, checksum, installed_at, enabled_at, meta_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			version = excluded.version,
			status = excluded.status,
			enabled = excluded.enabled,
			source = excluded.source,
			checksum = excluded.checksum,
			installed_at = excluded.installed_at,
			enabled_at = excluded.enabled_at,
			meta_json = excluded.meta_json`,
		rec.ID, rec.Version, rec.Status, boolToInt(rec.Enabled), rec.Source, rec.Checksum,
		formatNullableTime(rec.InstalledAt), formatNullableTime(rec.EnabledAt), rec.MetaJSON)
	return err
}

func (s *PluginStore) Delete(id string) error {
	_, err := s.db.Exec(`DELETE FROM plugins WHERE id = ?`, id)
	return err
}

// UpdateMeta replaces the persisted manifest JSON for a plugin record.
func (s *PluginStore) UpdateMeta(id, metaJSON string) error {
	_, err := s.db.Exec(`UPDATE plugins SET meta_json = ? WHERE id = ?`, metaJSON, id)
	return err
}

// Checkpoint forces the WAL into the main database file so a container
// restart immediately after a plugin write cannot lose the change.
func (s *PluginStore) Checkpoint() error {
	_, err := s.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	return err
}

func (s *PluginStore) EnabledIDs() (map[string]bool, error) {
	rows, err := s.db.Query(`SELECT id FROM plugins WHERE enabled = 1 AND status = 'installed'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]bool)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

type pluginScanner interface {
	Scan(dest ...any) error
}

func scanPlugin(row pluginScanner) (PluginRecord, error) {
	var rec PluginRecord
	var enabled int
	var installed any
	var enabledAt any
	if err := row.Scan(&rec.ID, &rec.Version, &rec.Status, &enabled, &rec.Source, &rec.Checksum, &installed, &enabledAt, &rec.MetaJSON); err != nil {
		return PluginRecord{}, err
	}
	rec.Enabled = enabled != 0
	if t, err := parseSQLiteTime(installed); err == nil && !t.IsZero() {
		rec.InstalledAt = &t
	}
	if t, err := parseSQLiteTime(enabledAt); err == nil && !t.IsZero() {
		rec.EnabledAt = &t
	}
	return rec, nil
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func formatNullableTime(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return t.UTC().Format(time.RFC3339Nano)
}

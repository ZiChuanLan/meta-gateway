package store

import (
	"database/sql"
	"fmt"

	"github.com/lan/meta-gateway/internal/domain"
)

// ChannelStore provides CRUD operations for channels.
type ChannelStore struct {
	db *sql.DB
}

func scanChannel(scanner interface {
	Scan(dest ...any) error
}, r *domain.Channel) error {
	return scanner.Scan(
		&r.ID,
		&r.SiteID,
		&r.CredentialID,
		&r.Name,
		&r.BaseURL,
		&r.ModelsCSV,
		&r.GroupName,
		&r.Priority,
		&r.Weight,
		&r.Status,
		&r.TypeHint,
		scanTime(&r.CreatedAt),
		scanTime(&r.UpdatedAt),
	)
}

func (s *ChannelStore) List() ([]domain.Channel, error) {
	rows, err := s.db.Query(`SELECT id, site_id, credential_id, name, base_url, models_csv, group_name, priority, weight, status, type_hint, created_at, updated_at FROM channels ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("channel list: %w", err)
	}
	defer rows.Close()

	var result []domain.Channel
	for rows.Next() {
		var r domain.Channel
		if err := scanChannel(rows, &r); err != nil {
			return nil, fmt.Errorf("channel scan: %w", err)
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

// ListEnabled returns all enabled channels.
func (s *ChannelStore) ListEnabled() ([]domain.Channel, error) {
	rows, err := s.db.Query(`SELECT id, site_id, credential_id, name, base_url, models_csv, group_name, priority, weight, status, type_hint, created_at, updated_at FROM channels WHERE status = ? ORDER BY priority, id`, domain.StatusEnabled)
	if err != nil {
		return nil, fmt.Errorf("channel list enabled: %w", err)
	}
	defer rows.Close()

	var result []domain.Channel
	for rows.Next() {
		var r domain.Channel
		if err := scanChannel(rows, &r); err != nil {
			return nil, fmt.Errorf("channel scan: %w", err)
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func (s *ChannelStore) GetByID(id int64) (*domain.Channel, error) {
	row := s.db.QueryRow(`SELECT id, site_id, credential_id, name, base_url, models_csv, group_name, priority, weight, status, type_hint, created_at, updated_at FROM channels WHERE id = ?`, id)
	var r domain.Channel
	if err := scanChannel(row, &r); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("channel get: %w", err)
	}
	return &r, nil
}

func (s *ChannelStore) Create(c *domain.Channel) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO channels (site_id, credential_id, name, base_url, models_csv, group_name, priority, weight, status, type_hint) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.SiteID, c.CredentialID, c.Name, c.BaseURL, c.ModelsCSV, c.GroupName, c.Priority, c.Weight, c.Status, c.TypeHint)
	if err != nil {
		return 0, fmt.Errorf("channel create: %w", err)
	}
	return res.LastInsertId()
}

func (s *ChannelStore) Update(c *domain.Channel) error {
	_, err := s.db.Exec(`UPDATE channels SET site_id=?, credential_id=?, name=?, base_url=?, models_csv=?, group_name=?, priority=?, weight=?, status=?, type_hint=?, updated_at=datetime('now') WHERE id=?`,
		c.SiteID, c.CredentialID, c.Name, c.BaseURL, c.ModelsCSV, c.GroupName, c.Priority, c.Weight, c.Status, c.TypeHint, c.ID)
	if err != nil {
		return fmt.Errorf("channel update: %w", err)
	}
	return nil
}

func (s *ChannelStore) Delete(id int64) error {
	_, err := s.db.Exec(`DELETE FROM channels WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("channel delete: %w", err)
	}
	return nil
}

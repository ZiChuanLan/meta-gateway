package store

import (
	"database/sql"
	"fmt"

	"github.com/lan/meta-gateway/internal/domain"
)

// SiteStore provides CRUD operations for sites.
type SiteStore struct {
	db *sql.DB
}

func (s *SiteStore) List() ([]domain.Site, error) {
	rows, err := s.db.Query(`SELECT id, name, base_url, platform, status, created_at, updated_at FROM sites ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("site list: %w", err)
	}
	defer rows.Close()

	var result []domain.Site
	for rows.Next() {
		var r domain.Site
		if err := rows.Scan(&r.ID, &r.Name, &r.BaseURL, &r.Platform, &r.Status, scanTime(&r.CreatedAt), scanTime(&r.UpdatedAt)); err != nil {
			return nil, fmt.Errorf("site list scan: %w", err)
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func (s *SiteStore) GetByID(id int64) (*domain.Site, error) {
	row := s.db.QueryRow(`SELECT id, name, base_url, platform, status, created_at, updated_at FROM sites WHERE id = ?`, id)
	var r domain.Site
	if err := row.Scan(&r.ID, &r.Name, &r.BaseURL, &r.Platform, &r.Status, scanTime(&r.CreatedAt), scanTime(&r.UpdatedAt)); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("site get: %w", err)
	}
	return &r, nil
}

func (s *SiteStore) Create(site *domain.Site) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO sites (name, base_url, platform, status) VALUES (?, ?, ?, ?)`,
		site.Name, site.BaseURL, site.Platform, site.Status)
	if err != nil {
		return 0, fmt.Errorf("site create: %w", err)
	}
	return res.LastInsertId()
}

func (s *SiteStore) Update(site *domain.Site) error {
	_, err := s.db.Exec(`UPDATE sites SET name=?, base_url=?, platform=?, status=?, updated_at=datetime('now') WHERE id=?`,
		site.Name, site.BaseURL, site.Platform, site.Status, site.ID)
	if err != nil {
		return fmt.Errorf("site update: %w", err)
	}
	return nil
}

func (s *SiteStore) Delete(id int64) error {
	_, err := s.db.Exec(`DELETE FROM sites WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("site delete: %w", err)
	}
	return nil
}

package store

import (
	"database/sql"
	"fmt"

	"github.com/lan/meta-gateway/internal/domain"
)

// CredentialStore provides CRUD operations for credentials.
type CredentialStore struct {
	db *sql.DB
}

func (s *CredentialStore) ListBySite(siteID int64) ([]domain.Credential, error) {
	rows, err := s.db.Query(`SELECT id, site_id, kind, secret_enc, meta_json, status, checkin_enabled, created_at, updated_at FROM credentials WHERE site_id = ? ORDER BY id`, siteID)
	if err != nil {
		return nil, fmt.Errorf("credential list: %w", err)
	}
	defer rows.Close()

	var result []domain.Credential
	for rows.Next() {
		var r domain.Credential
		var secret string
		if err := rows.Scan(&r.ID, &r.SiteID, &r.Kind, &secret, &r.MetaJSON, &r.Status, &r.CheckinEnabled, scanTime(&r.CreatedAt), scanTime(&r.UpdatedAt)); err != nil {
			return nil, fmt.Errorf("credential scan: %w", err)
		}
		r.SecretEnc = []byte(secret)
		result = append(result, r)
	}
	return result, rows.Err()
}

func (s *CredentialStore) GetByID(id int64) (*domain.Credential, error) {
	row := s.db.QueryRow(`SELECT id, site_id, kind, secret_enc, meta_json, status, checkin_enabled, created_at, updated_at FROM credentials WHERE id = ?`, id)
	var r domain.Credential
	var secret string
	if err := row.Scan(&r.ID, &r.SiteID, &r.Kind, &secret, &r.MetaJSON, &r.Status, &r.CheckinEnabled, scanTime(&r.CreatedAt), scanTime(&r.UpdatedAt)); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("credential get: %w", err)
	}
	r.SecretEnc = []byte(secret)
	return &r, nil
}

func (s *CredentialStore) Create(c *domain.Credential) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO credentials (site_id, kind, secret_enc, meta_json, status, checkin_enabled) VALUES (?, ?, ?, ?, ?, ?)`,
		c.SiteID, c.Kind, string(c.SecretEnc), c.MetaJSON, c.Status, c.CheckinEnabled)
	if err != nil {
		return 0, fmt.Errorf("credential create: %w", err)
	}
	return res.LastInsertId()
}

func (s *CredentialStore) SetCheckinEnabled(id int64, enabled bool) error {
	result, err := s.db.Exec(`UPDATE credentials SET checkin_enabled=?, updated_at=datetime('now') WHERE id=?`, enabled, id)
	if err != nil {
		return fmt.Errorf("credential checkin update: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("credential checkin rows: %w", err)
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *CredentialStore) ListCheckinEnabled() ([]domain.Credential, error) {
	rows, err := s.db.Query(`SELECT id, site_id, kind, secret_enc, meta_json, status, checkin_enabled, created_at, updated_at FROM credentials WHERE checkin_enabled = 1 ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("credential checkin list: %w", err)
	}
	defer rows.Close()
	var result []domain.Credential
	for rows.Next() {
		var r domain.Credential
		var secret string
		if err := rows.Scan(&r.ID, &r.SiteID, &r.Kind, &secret, &r.MetaJSON, &r.Status, &r.CheckinEnabled, scanTime(&r.CreatedAt), scanTime(&r.UpdatedAt)); err != nil {
			return nil, fmt.Errorf("credential checkin scan: %w", err)
		}
		r.SecretEnc = []byte(secret)
		result = append(result, r)
	}
	return result, rows.Err()
}

func (s *CredentialStore) Update(c *domain.Credential) error {
	_, err := s.db.Exec(`UPDATE credentials SET kind=?, secret_enc=?, meta_json=?, status=?, updated_at=datetime('now') WHERE id=?`,
		c.Kind, string(c.SecretEnc), c.MetaJSON, c.Status, c.ID)
	if err != nil {
		return fmt.Errorf("credential update: %w", err)
	}
	return nil
}

func (s *CredentialStore) Delete(id int64) error {
	_, err := s.db.Exec(`DELETE FROM credentials WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("credential delete: %w", err)
	}
	return nil
}

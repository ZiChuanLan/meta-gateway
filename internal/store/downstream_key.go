package store

import (
	"database/sql"
	"fmt"

	"github.com/lan/meta-gateway/internal/domain"
)

// DownstreamKeyStore provides CRUD operations for downstream keys.
type DownstreamKeyStore struct {
	db *sql.DB
}

func scanDownstreamKey(scanner interface {
	Scan(dest ...any) error
}, r *domain.DownstreamKey) error {
	var enabled int
	if err := scanner.Scan(&r.ID, &r.TokenHash, &r.Name, &enabled, &r.Scopes, scanTime(&r.CreatedAt)); err != nil {
		return err
	}
	r.Enabled = enabled != 0
	return nil
}

func (s *DownstreamKeyStore) List() ([]domain.DownstreamKey, error) {
	rows, err := s.db.Query(`SELECT id, token_hash, name, enabled, scopes, created_at FROM downstream_keys ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("downstream key list: %w", err)
	}
	defer rows.Close()

	var result []domain.DownstreamKey
	for rows.Next() {
		var r domain.DownstreamKey
		if err := scanDownstreamKey(rows, &r); err != nil {
			return nil, fmt.Errorf("downstream key scan: %w", err)
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func (s *DownstreamKeyStore) GetByID(id int64) (*domain.DownstreamKey, error) {
	row := s.db.QueryRow(`SELECT id, token_hash, name, enabled, scopes, created_at FROM downstream_keys WHERE id = ?`, id)
	var r domain.DownstreamKey
	if err := scanDownstreamKey(row, &r); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("downstream key get: %w", err)
	}
	return &r, nil
}

// GetByHash retrieves a downstream key by its hashed token value.
func (s *DownstreamKeyStore) GetByHash(hash string) (*domain.DownstreamKey, error) {
	row := s.db.QueryRow(`SELECT id, token_hash, name, enabled, scopes, created_at FROM downstream_keys WHERE token_hash = ?`, hash)
	var r domain.DownstreamKey
	if err := scanDownstreamKey(row, &r); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("downstream key by hash: %w", err)
	}
	return &r, nil
}

func (s *DownstreamKeyStore) Create(k *domain.DownstreamKey) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO downstream_keys (token_hash, name, enabled, scopes) VALUES (?, ?, ?, ?)`,
		k.TokenHash, k.Name, boolInt(k.Enabled), k.Scopes)
	if err != nil {
		return 0, fmt.Errorf("downstream key create: %w", err)
	}
	return res.LastInsertId()
}

func (s *DownstreamKeyStore) Delete(id int64) error {
	_, err := s.db.Exec(`DELETE FROM downstream_keys WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("downstream key delete: %w", err)
	}
	return nil
}

func (s *DownstreamKeyStore) Update(k *domain.DownstreamKey) error {
	_, err := s.db.Exec(`UPDATE downstream_keys SET name=?, enabled=?, scopes=? WHERE id=?`,
		k.Name, boolInt(k.Enabled), k.Scopes, k.ID)
	if err != nil {
		return fmt.Errorf("downstream key update: %w", err)
	}
	return nil
}

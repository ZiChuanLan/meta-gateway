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
	if err := scanner.Scan(
		&r.ID,
		&r.TokenHash,
		&r.Name,
		&enabled,
		&r.Scopes,
		&r.QuotaTotalTokens,
		&r.QuotaUsedTokens,
		&r.PricePromptPer1k,
		&r.PriceCompletionPer1k,
		scanTime(&r.CreatedAt),
	); err != nil {
		return err
	}
	r.Enabled = enabled != 0
	return nil
}

const downstreamKeySelect = `SELECT id, token_hash, name, enabled, scopes, quota_total_tokens, quota_used_tokens, price_prompt_per_1k, price_completion_per_1k, created_at FROM downstream_keys`

func (s *DownstreamKeyStore) List() ([]domain.DownstreamKey, error) {
	rows, err := s.db.Query(downstreamKeySelect + ` ORDER BY id`)
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
	row := s.db.QueryRow(downstreamKeySelect+` WHERE id = ?`, id)
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
	row := s.db.QueryRow(downstreamKeySelect+` WHERE token_hash = ?`, hash)
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
	res, err := s.db.Exec(
		`INSERT INTO downstream_keys (token_hash, name, enabled, scopes, quota_total_tokens, quota_used_tokens, price_prompt_per_1k, price_completion_per_1k) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		k.TokenHash, k.Name, boolInt(k.Enabled), k.Scopes, k.QuotaTotalTokens, k.QuotaUsedTokens, k.PricePromptPer1k, k.PriceCompletionPer1k,
	)
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
	_, err := s.db.Exec(
		`UPDATE downstream_keys SET name=?, enabled=?, scopes=?, quota_total_tokens=?, price_prompt_per_1k=?, price_completion_per_1k=? WHERE id=?`,
		k.Name, boolInt(k.Enabled), k.Scopes, k.QuotaTotalTokens, k.PricePromptPer1k, k.PriceCompletionPer1k, k.ID,
	)
	if err != nil {
		return fmt.Errorf("downstream key update: %w", err)
	}
	return nil
}

// AddUsage increments quota_used_tokens for a key.
func (s *DownstreamKeyStore) AddUsage(id int64, totalTokens int) error {
	if id <= 0 || totalTokens <= 0 {
		return nil
	}
	_, err := s.db.Exec(`UPDATE downstream_keys SET quota_used_tokens = quota_used_tokens + ? WHERE id = ?`, totalTokens, id)
	if err != nil {
		return fmt.Errorf("downstream key add usage: %w", err)
	}
	return nil
}

// QuotaExceeded reports whether the key has exhausted a finite quota.
func QuotaExceeded(key *domain.DownstreamKey) bool {
	if key == nil {
		return true
	}
	if key.QuotaTotalTokens <= 0 {
		return false
	}
	return key.QuotaUsedTokens >= key.QuotaTotalTokens
}

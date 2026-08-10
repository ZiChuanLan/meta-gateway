package store

import (
	"database/sql"
	"fmt"
	"sync"

	"github.com/lan/meta-gateway/internal/domain"
)

// DownstreamKeyStore provides CRUD operations for downstream keys.
//
// The relay hot path authenticates every /v1 request through this store, so
// reads are served from an in-process cache (by id and by token hash). The
// cache never shares mutable state with callers: reads return deep copies and
// writes replace (never mutate in place) cached objects, so concurrent relay
// reads and admin writes cannot race. Usage/quota writes only invalidate the
// cache and let the next read reload from the database.
type DownstreamKeyStore struct {
	db *sql.DB

	mu     sync.RWMutex
	byID   map[int64]*domain.DownstreamKey
	byHash map[string]*domain.DownstreamKey
}

func newDownstreamKeyStore(db *sql.DB) *DownstreamKeyStore {
	return &DownstreamKeyStore{
		db:     db,
		byID:   make(map[int64]*domain.DownstreamKey),
		byHash: make(map[string]*domain.DownstreamKey),
	}
}

// cloneKey returns a deep copy so callers can never mutate the cached object
// (or observe in-flight admin edits) through a shared pointer. All fields are
// value types (string/int64), so a struct copy is sufficient.
func cloneKey(key *domain.DownstreamKey) *domain.DownstreamKey {
	if key == nil {
		return nil
	}
	copy := *key
	return &copy
}

// invalidate drops a key from both cache indexes. Callers must hold no lock.
func (s *DownstreamKeyStore) invalidate(id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.byID[id]; ok {
		if old.TokenHash != "" {
			delete(s.byHash, old.TokenHash)
		}
		delete(s.byID, id)
	}
}

// cachePut stores a clone of the key in both indexes. Callers must hold no lock.
func (s *DownstreamKeyStore) cachePut(key *domain.DownstreamKey) {
	if key == nil || key.ID <= 0 {
		return
	}
	cloned := cloneKey(key)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byID[cloned.ID] = cloned
	if cloned.TokenHash != "" {
		s.byHash[cloned.TokenHash] = cloned
	}
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
		&r.ModelAllowlist,
		&r.ModelDenylist,
		&r.ExpiresAt,
		&r.AllowedIPs,
		&r.GroupName,
		scanTime(&r.CreatedAt),
	); err != nil {
		return err
	}
	r.Enabled = enabled != 0
	return nil
}

const downstreamKeySelect = `SELECT id, token_hash, name, enabled, scopes, quota_total_tokens, quota_used_tokens, price_prompt_per_1k, price_completion_per_1k, model_allowlist, model_denylist, expires_at, allowed_ips, group_name, created_at FROM downstream_keys`

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
	if id > 0 {
		s.mu.RLock()
		cached, ok := s.byID[id]
		s.mu.RUnlock()
		if ok {
			return cloneKey(cached), nil
		}
	}
	row := s.db.QueryRow(downstreamKeySelect+` WHERE id = ?`, id)
	var r domain.DownstreamKey
	if err := scanDownstreamKey(row, &r); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("downstream key get: %w", err)
	}
	s.cachePut(&r)
	return &r, nil
}

// GetByHash retrieves a downstream key by its hashed token value, using the
// in-process cache on the hot path. The returned key is a copy.
func (s *DownstreamKeyStore) GetByHash(hash string) (*domain.DownstreamKey, error) {
	if hash != "" {
		s.mu.RLock()
		cached, ok := s.byHash[hash]
		s.mu.RUnlock()
		if ok {
			return cloneKey(cached), nil
		}
	}
	row := s.db.QueryRow(downstreamKeySelect+` WHERE token_hash = ?`, hash)
	var r domain.DownstreamKey
	if err := scanDownstreamKey(row, &r); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("downstream key by hash: %w", err)
	}
	s.cachePut(&r)
	return &r, nil
}

func (s *DownstreamKeyStore) Create(k *domain.DownstreamKey) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO downstream_keys (token_hash, name, enabled, scopes, quota_total_tokens, quota_used_tokens, price_prompt_per_1k, price_completion_per_1k, model_allowlist, model_denylist, expires_at, allowed_ips, group_name) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		k.TokenHash, k.Name, boolInt(k.Enabled), k.Scopes, k.QuotaTotalTokens, k.QuotaUsedTokens, k.PricePromptPer1k, k.PriceCompletionPer1k, k.ModelAllowlist, k.ModelDenylist, k.ExpiresAt, k.AllowedIPs, normalizeGroupName(k.GroupName),
	)
	if err != nil {
		return 0, fmt.Errorf("downstream key create: %w", err)
	}
	id, err := res.LastInsertId()
	if err == nil {
		k.ID = id
		s.cachePut(k)
	}
	return id, err
}

func (s *DownstreamKeyStore) Delete(id int64) error {
	_, err := s.db.Exec(`DELETE FROM downstream_keys WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("downstream key delete: %w", err)
	}
	s.invalidate(id)
	return nil
}

func (s *DownstreamKeyStore) Update(k *domain.DownstreamKey) error {
	_, err := s.db.Exec(
		`UPDATE downstream_keys SET name=?, enabled=?, scopes=?, quota_total_tokens=?, price_prompt_per_1k=?, price_completion_per_1k=?, model_allowlist=?, model_denylist=?, expires_at=?, allowed_ips=?, group_name=? WHERE id=?`,
		k.Name, boolInt(k.Enabled), k.Scopes, k.QuotaTotalTokens, k.PricePromptPer1k, k.PriceCompletionPer1k, k.ModelAllowlist, k.ModelDenylist, k.ExpiresAt, k.AllowedIPs, normalizeGroupName(k.GroupName), k.ID,
	)
	if err != nil {
		return fmt.Errorf("downstream key update: %w", err)
	}
	s.invalidate(k.ID)
	return nil
}

// ResetUsage zeroes the key's used quota in the database and drops the cached
// entry so the next read observes the reset.
func (s *DownstreamKeyStore) ResetUsage(id int64) error {
	_, err := s.db.Exec(`UPDATE downstream_keys SET quota_used_tokens = 0 WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("downstream key reset usage: %w", err)
	}
	s.invalidate(id)
	return nil
}

// bumpCachedUsage applies an already-persisted usage increment to the cached
// entry in place (the caller owns the write, e.g. inside the RecordRelayUsage
// transaction), so the next auth read still hits the cache instead of
// reloading from SQLite. The database remains authoritative; the cache is
// only an accelerator.
func (s *DownstreamKeyStore) bumpCachedUsage(id int64, totalTokens int) {
	if id <= 0 || totalTokens <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if cached, ok := s.byID[id]; ok {
		cached.QuotaUsedTokens += int64(totalTokens)
	}
}

// AddUsage increments quota_used_tokens for a key and applies the same
// increment to the cached entry in place; the next read still hits the cache.
func (s *DownstreamKeyStore) AddUsage(id int64, totalTokens int) error {
	if id <= 0 || totalTokens <= 0 {
		return nil
	}
	_, err := s.db.Exec(`UPDATE downstream_keys SET quota_used_tokens = quota_used_tokens + ? WHERE id = ?`, totalTokens, id)
	if err != nil {
		return fmt.Errorf("downstream key add usage: %w", err)
	}
	s.bumpCachedUsage(id, totalTokens)
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

package store

import (
	"database/sql"
	"errors"
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
// reads and admin writes cannot race. Usage writes synchronize an absolute
// committed quota value; administrative writes invalidate the entry.
type DownstreamKeyStore struct {
	db *sql.DB

	mu         sync.RWMutex
	byID       map[int64]*domain.DownstreamKey
	byHash     map[string]*domain.DownstreamKey
	generation uint64
	// mutationEpoch changes on non-monotonic/admin writes. Usage callbacks
	// captured before a reset/delete/update must not repopulate newer state.
	mutationEpoch uint64
}

func newDownstreamKeyStore(db *sql.DB) *DownstreamKeyStore {
	return &DownstreamKeyStore{
		db:     db,
		byID:   make(map[int64]*domain.DownstreamKey),
		byHash: make(map[string]*domain.DownstreamKey),
	}
}

// ClearCache drops both authentication indexes. It is used after bulk SQL
// operations such as FactoryReset where individual invalidation is not enough.
func (s *DownstreamKeyStore) ClearCache() {
	s.mu.Lock()
	s.byID = make(map[int64]*domain.DownstreamKey)
	s.byHash = make(map[string]*domain.DownstreamKey)
	s.generation++
	s.mutationEpoch++
	s.mu.Unlock()
}

// cloneKey returns a deep copy so callers can never mutate the cached object
// (or observe in-flight admin edits) through a shared pointer. All fields are
// value types (string/int64), so a struct copy is sufficient; TokenEnc is
// nilled separately by cachePutIfGeneration and never shared from the cache.
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
	s.generation++
	s.mutationEpoch++
	if old, ok := s.byID[id]; ok {
		if old.TokenHash != "" {
			delete(s.byHash, old.TokenHash)
		}
		delete(s.byID, id)
	}
}

// cachePutIfGeneration stores a clone of the key in both indexes when the
// miss query was not overtaken by a write. Callers must hold no lock.
func (s *DownstreamKeyStore) cachePutIfGeneration(key *domain.DownstreamKey, generation uint64) {
	if key == nil || key.ID <= 0 {
		return
	}
	cloned := cloneKey(key)
	// Never keep the encrypted plaintext token in the hot-path cache; it is
	// only fetched on demand by the admin reveal endpoint.
	cloned.TokenEnc = nil
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.generation != generation {
		return
	}
	s.byID[cloned.ID] = cloned
	if cloned.TokenHash != "" {
		s.byHash[cloned.TokenHash] = cloned
	}
}

func scanDownstreamKey(scanner interface {
	Scan(dest ...any) error
}, r *domain.DownstreamKey) error {
	var enabled int
	var tokenEnc string
	if err := scanner.Scan(
		&r.ID,
		&r.TokenHash,
		&tokenEnc,
		&r.Name,
		&enabled,
		&r.Scopes,
		&r.QuotaTotalTokens,
		&r.QuotaUsedTokens,
		&r.PricePromptPer1k,
		&r.PriceCompletionPer1k,
		&r.PriceCachePer1k,
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
	r.TokenEnc = []byte(tokenEnc)
	return nil
}

const downstreamKeySelect = `SELECT id, token_hash, token_enc, name, enabled, scopes, quota_total_tokens, quota_used_tokens, price_prompt_per_1k, price_completion_per_1k, price_cache_per_1k, model_allowlist, model_denylist, expires_at, allowed_ips, group_name, created_at FROM downstream_keys`

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
	var generation uint64
	if id > 0 {
		s.mu.RLock()
		cached, ok := s.byID[id]
		generation = s.generation
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
	s.cachePutIfGeneration(&r, generation)
	return &r, nil
}

// GetByHash retrieves a downstream key by its hashed token value, using the
// in-process cache on the hot path. The returned key is a copy.
func (s *DownstreamKeyStore) GetByHash(hash string) (*domain.DownstreamKey, error) {
	var generation uint64
	if hash != "" {
		s.mu.RLock()
		cached, ok := s.byHash[hash]
		generation = s.generation
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
	s.cachePutIfGeneration(&r, generation)
	return &r, nil
}

func (s *DownstreamKeyStore) Create(k *domain.DownstreamKey) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO downstream_keys (token_hash, token_enc, name, enabled, scopes, quota_total_tokens, quota_used_tokens, price_prompt_per_1k, price_completion_per_1k, price_cache_per_1k, model_allowlist, model_denylist, expires_at, allowed_ips, group_name) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		k.TokenHash, string(k.TokenEnc), k.Name, boolInt(k.Enabled), k.Scopes, k.QuotaTotalTokens, k.QuotaUsedTokens, k.PricePromptPer1k, k.PriceCompletionPer1k, k.PriceCachePer1k, k.ModelAllowlist, k.ModelDenylist, k.ExpiresAt, k.AllowedIPs, normalizeGroupName(k.GroupName),
	)
	if err != nil {
		return 0, fmt.Errorf("downstream key create: %w", err)
	}
	id, err := res.LastInsertId()
	if err == nil {
		k.ID = id
		s.invalidate(id)
		_, _ = s.GetByID(id)
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
		`UPDATE downstream_keys SET name=?, enabled=?, scopes=?, quota_total_tokens=?, price_prompt_per_1k=?, price_completion_per_1k=?, price_cache_per_1k=?, model_allowlist=?, model_denylist=?, expires_at=?, allowed_ips=?, group_name=? WHERE id=?`,
		k.Name, boolInt(k.Enabled), k.Scopes, k.QuotaTotalTokens, k.PricePromptPer1k, k.PriceCompletionPer1k, k.PriceCachePer1k, k.ModelAllowlist, k.ModelDenylist, k.ExpiresAt, k.AllowedIPs, normalizeGroupName(k.GroupName), k.ID,
	)
	if err != nil {
		return fmt.Errorf("downstream key update: %w", err)
	}
	s.invalidate(k.ID)
	return nil
}

// Invalidate drops a key from the in-process cache so the next read observes
// external quota changes (e.g. a redemption top-up).
func (s *DownstreamKeyStore) Invalidate(id int64) {
	s.invalidate(id)
}

// GetTokenEnc returns the MASTER_KEY-encrypted plaintext token for a key
// (empty string when the key predates plaintext storage). Admin-only; never
// cached.
func (s *DownstreamKeyStore) GetTokenEnc(id int64) (string, error) {
	var enc string
	if err := s.db.QueryRow(`SELECT COALESCE(token_enc, '') FROM downstream_keys WHERE id = ?`, id).Scan(&enc); err != nil {
		return "", fmt.Errorf("downstream key token enc: %w", err)
	}
	return enc, nil
}

// RotateToken atomically replaces the token hash and its encrypted plaintext,
// invalidating the old token immediately.
func (s *DownstreamKeyStore) RotateToken(id int64, hash string, tokenEnc string) error {
	_, err := s.db.Exec(`UPDATE downstream_keys SET token_hash = ?, token_enc = ? WHERE id = ?`, hash, tokenEnc, id)
	if err != nil {
		return fmt.Errorf("downstream key rotate token: %w", err)
	}
	s.invalidate(id)
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

func (s *DownstreamKeyStore) mutationEpochSnapshot() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.mutationEpoch
}

// setCachedUsageIfEpoch applies the absolute value returned by the committed
// SQL update. Absolute/max assignment avoids double-counting when a cache miss
// reloads the new database value before this callback runs; the epoch blocks
// callbacks that predate a reset or administrative mutation.
func (s *DownstreamKeyStore) setCachedUsageIfEpoch(id, used int64, epoch uint64) {
	if id <= 0 || used < 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mutationEpoch != epoch {
		return
	}
	s.generation++
	if cached, ok := s.byID[id]; ok && used > cached.QuotaUsedTokens {
		cached.QuotaUsedTokens = used
	}
}

// AddUsage increments quota_used_tokens for a key and synchronizes the
// committed absolute value into the cache; the next read still hits the cache.
func (s *DownstreamKeyStore) AddUsage(id int64, totalTokens int) error {
	if id <= 0 || totalTokens <= 0 {
		return nil
	}
	epoch := s.mutationEpochSnapshot()
	var used int64
	err := s.db.QueryRow(`UPDATE downstream_keys SET quota_used_tokens = quota_used_tokens + ? WHERE id = ? RETURNING quota_used_tokens`, totalTokens, id).Scan(&used)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("downstream key add usage: %w", err)
	}
	s.setCachedUsageIfEpoch(id, used, epoch)
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

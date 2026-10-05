package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
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
		&r.QuotaTotalCost,
		&r.QuotaUsedCost,
		&r.ModelAllowlist,
		&r.ModelDenylist,
		&r.ExpiresAt,
		&r.AllowedIPs,
		&r.GroupName,
		&r.RouteGroupName,
		scanTime(&r.CreatedAt),
		&r.UserID,
		&r.TeamPlanID,
		&r.TeamHint,
		&r.TeamDeletedAt,
	); err != nil {
		return err
	}
	r.Enabled = enabled != 0
	r.TokenEnc = []byte(tokenEnc)
	return nil
}

const downstreamKeySelect = `SELECT id, token_hash, token_enc, name, enabled, scopes, quota_total_tokens, quota_used_tokens, quota_total_cost, quota_used_cost, model_allowlist, model_denylist, expires_at, allowed_ips, group_name, route_group_name, created_at, COALESCE(user_id,0), team_plan_id, team_hint, team_deleted_at FROM downstream_keys`

func (s *DownstreamKeyStore) List() ([]domain.DownstreamKey, error) {
	rows, err := s.db.Query(downstreamKeySelect + ` WHERE team_deleted_at = '' ORDER BY id`)
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
		`INSERT INTO downstream_keys (token_hash, token_enc, name, enabled, scopes, quota_total_tokens, quota_used_tokens, quota_total_cost, quota_used_cost, model_allowlist, model_denylist, expires_at, allowed_ips, group_name, route_group_name) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		k.TokenHash, string(k.TokenEnc), k.Name, boolInt(k.Enabled), k.Scopes, k.QuotaTotalTokens, k.QuotaUsedTokens, k.QuotaTotalCost, k.QuotaUsedCost, k.ModelAllowlist, k.ModelDenylist, k.ExpiresAt, k.AllowedIPs, normalizeGroupName(k.GroupName), strings.TrimSpace(k.RouteGroupName),
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
		`UPDATE downstream_keys SET name=?, enabled=?, scopes=?, quota_total_tokens=?, quota_total_cost=?, model_allowlist=?, model_denylist=?, expires_at=?, allowed_ips=?, group_name=?, route_group_name=? WHERE id=?`,
		k.Name, boolInt(k.Enabled), k.Scopes, k.QuotaTotalTokens, k.QuotaTotalCost, k.ModelAllowlist, k.ModelDenylist, k.ExpiresAt, k.AllowedIPs, normalizeGroupName(k.GroupName), strings.TrimSpace(k.RouteGroupName), k.ID,
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
// ResetUsage clears BOTH counters: "reset the usage" means the key is fresh
// again, and clearing only tokens would leave a spent spend-budget behind.
func (s *DownstreamKeyStore) ResetUsage(id int64) error {
	_, err := s.db.Exec(`UPDATE downstream_keys SET quota_used_tokens = 0, quota_used_cost = 0 WHERE id = ?`, id)
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
// setCachedUsageIfEpoch syncs the committed absolute counters into the cache.
// Money counts here exactly like tokens: the relay reads this cached object for
// its quota check, so syncing only the token counter would leave a key's spend
// budget frozen at whatever it was when the row was first cached — a capped key
// would keep being served.
func (s *DownstreamKeyStore) setCachedUsageIfEpoch(id, used int64, usedCost float64, epoch uint64) {
	if id <= 0 || used < 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mutationEpoch != epoch {
		return
	}
	s.generation++
	if cached, ok := s.byID[id]; ok {
		if used > cached.QuotaUsedTokens {
			cached.QuotaUsedTokens = used
		}
		if usedCost > cached.QuotaUsedCost {
			cached.QuotaUsedCost = usedCost
		}
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
	var spend float64
	// Returning the spend beside the count keeps the cached key consistent: this
	// path only ever adds tokens, so the money counter is carried through
	// unchanged rather than being reset to zero in the cache.
	err := s.db.QueryRow(`UPDATE downstream_keys SET quota_used_tokens = quota_used_tokens + ? WHERE id = ? RETURNING quota_used_tokens, quota_used_cost`, totalTokens, id).Scan(&used, &spend)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("downstream key add usage: %w", err)
	}
	s.setCachedUsageIfEpoch(id, used, spend, epoch)
	return nil
}

// QuotaExceeded reports whether the key has exhausted a finite quota. Both
// units count: a key with a spend budget and a key with a token budget are the
// same thing to the relay, and whichever runs out first refuses the request.
func QuotaExceeded(key *domain.DownstreamKey) bool {
	if key == nil {
		return true
	}
	if key.QuotaTotalTokens > 0 && key.QuotaUsedTokens >= key.QuotaTotalTokens {
		return true
	}
	return key.QuotaTotalCost > 0 && key.QuotaUsedCost >= key.QuotaTotalCost
}

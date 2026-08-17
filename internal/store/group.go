package store

import (
	"database/sql"
	"fmt"
	"strings"
	"sync"

	"github.com/lan/meta-gateway/internal/domain"
)

// GroupStore persists multi-tenant key groups (quota + rate limits).
// Reads on the relay hot path are cache-backed; writes invalidate.
type GroupStore struct {
	db *sql.DB

	mu            sync.RWMutex
	cache         map[string]*domain.KeyGroup
	generation    uint64
	mutationEpoch uint64
}

func newGroupStore(db *sql.DB) *GroupStore {
	return &GroupStore{db: db, cache: make(map[string]*domain.KeyGroup)}
}

// ClearCache drops all cached groups after a bulk SQL operation.
func (s *GroupStore) ClearCache() {
	s.mu.Lock()
	s.cache = make(map[string]*domain.KeyGroup)
	s.generation++
	s.mutationEpoch++
	s.mu.Unlock()
}

// Get returns a group by name; nil when absent. "default" always resolves to
// an unlimited group even before any row exists. The returned group is a copy:
// callers can never mutate the cached object through a shared pointer.
func (s *GroupStore) Get(name string) (*domain.KeyGroup, error) {
	name = normalizeGroupName(name)
	if name == "" {
		name = "default"
	}
	s.mu.RLock()
	cached, ok := s.cache[name]
	generation := s.generation
	s.mu.RUnlock()
	if ok {
		clone := *cached
		return &clone, nil
	}
	row := s.db.QueryRow(`SELECT name, quota_total_tokens, quota_used_tokens, rate_per_minute, rate_burst, created_at, updated_at FROM key_groups WHERE name = ?`, name)
	var g domain.KeyGroup
	if err := row.Scan(&g.Name, &g.QuotaTotalTokens, &g.QuotaUsedTokens, &g.RatePerMinute, &g.RateBurst, scanTime(&g.CreatedAt), scanTime(&g.UpdatedAt)); err != nil {
		if err == sql.ErrNoRows {
			// Absent group = unlimited, no rate limit.
			def := &domain.KeyGroup{Name: name}
			s.cachePutIfGeneration(def, generation)
			return def, nil
		}
		return nil, fmt.Errorf("group get: %w", err)
	}
	s.cachePutIfGeneration(&g, generation)
	return &g, nil
}

// List returns every group ordered by name.
func (s *GroupStore) List() ([]domain.KeyGroup, error) {
	rows, err := s.db.Query(`SELECT name, quota_total_tokens, quota_used_tokens, rate_per_minute, rate_burst, created_at, updated_at FROM key_groups ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("group list: %w", err)
	}
	defer rows.Close()
	var result []domain.KeyGroup
	for rows.Next() {
		var g domain.KeyGroup
		if err := rows.Scan(&g.Name, &g.QuotaTotalTokens, &g.QuotaUsedTokens, &g.RatePerMinute, &g.RateBurst, scanTime(&g.CreatedAt), scanTime(&g.UpdatedAt)); err != nil {
			return nil, fmt.Errorf("group scan: %w", err)
		}
		result = append(result, g)
	}
	return result, rows.Err()
}

// Upsert creates or updates a group's quota/rate limits.
func (s *GroupStore) Upsert(name string, quotaTotal int64, ratePerMinute, rateBurst int) error {
	name = normalizeGroupName(name)
	if name == "" {
		return fmt.Errorf("group upsert: empty name")
	}
	if _, err := s.db.Exec(
		`INSERT INTO key_groups (name, quota_total_tokens, quota_used_tokens, rate_per_minute, rate_burst, created_at, updated_at)
		 VALUES (?, ?, 0, ?, ?, datetime('now'), datetime('now'))
		 ON CONFLICT(name) DO UPDATE SET
		   quota_total_tokens = excluded.quota_total_tokens,
		   rate_per_minute = excluded.rate_per_minute,
		   rate_burst = excluded.rate_burst,
		   updated_at = datetime('now')`,
		name, quotaTotal, ratePerMinute, rateBurst,
	); err != nil {
		return fmt.Errorf("group upsert: %w", err)
	}
	s.mu.Lock()
	s.generation++
	s.mutationEpoch++
	delete(s.cache, name)
	s.mu.Unlock()
	return nil
}

// Delete removes a group; its keys fall back to the default group on the next
// read (Get returns an unlimited default for absent rows).
func (s *GroupStore) Delete(name string) error {
	name = normalizeGroupName(name)
	if name == "" || name == "default" {
		return fmt.Errorf("group delete: cannot delete default")
	}
	if _, err := s.db.Exec(`DELETE FROM key_groups WHERE name = ?`, name); err != nil {
		return fmt.Errorf("group delete: %w", err)
	}
	s.mu.Lock()
	s.generation++
	s.mutationEpoch++
	delete(s.cache, name)
	s.mu.Unlock()
	return nil
}

func (s *GroupStore) mutationEpochSnapshot() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.mutationEpoch
}

func (s *GroupStore) setCachedUsageIfEpoch(name string, used int64, epoch uint64) {
	name = normalizeGroupName(name)
	if name == "" || used < 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mutationEpoch != epoch {
		return
	}
	s.generation++
	if cached, ok := s.cache[name]; ok && used > cached.QuotaUsedTokens {
		cached.QuotaUsedTokens = used
	}
}

func (s *GroupStore) cachePutIfGeneration(g *domain.KeyGroup, generation uint64) {
	if g == nil || g.Name == "" {
		return
	}
	clone := *g
	s.mu.Lock()
	if s.generation == generation {
		s.cache[clone.Name] = &clone
	}
	s.mu.Unlock()
}

func normalizeGroupName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

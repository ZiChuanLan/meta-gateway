package httpapi

import (
	"strings"
	"sync"
	"time"
)

// modelsCache caches the raw model id list served by /v1/models. The list is
// derived from enabled routes (plus the channel models fallback) and only
// changes on admin writes, so a short TTL plus explicit invalidation removes
// the per-request DB queries without ever serving stale models for long.
type modelsCache struct {
	mu         sync.Mutex
	refreshMu  sync.Mutex
	raw        []string
	cachedAt   time.Time
	ttl        time.Duration
	now        func() time.Time
	generation uint64
}

func newModelsCache(ttl time.Duration) *modelsCache {
	if ttl <= 0 {
		ttl = 5 * time.Second
	}
	return &modelsCache{ttl: ttl, now: time.Now}
}

// Get returns the cached raw model ids, or false when the cache is cold/expired.
func (c *modelsCache) Get() ([]string, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	age := c.now().Sub(c.cachedAt)
	if c.raw == nil || age < 0 || age >= c.ttl {
		return nil, false
	}
	return append([]string(nil), c.raw...), true
}

// GetOrCompute serializes cold-cache refreshes and rechecks the cache after
// waiting, so concurrent /v1/models requests share one database scan.
func (c *modelsCache) GetOrCompute(compute func() []string) []string {
	if c == nil {
		if compute == nil {
			return nil
		}
		return compute()
	}
	if raw, ok := c.Get(); ok {
		return raw
	}
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()
	if raw, ok := c.Get(); ok {
		return raw
	}
	if compute == nil {
		return nil
	}
	generation := c.currentGeneration()
	raw := compute()
	c.putIfGeneration(raw, generation)
	return append([]string(nil), raw...)
}

func (c *modelsCache) currentGeneration() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.generation
}

func (c *modelsCache) putIfGeneration(raw []string, generation uint64) {
	seen := make(map[string]struct{}, len(raw))
	ids := make([]string, 0, len(raw))
	for _, id := range raw {
		if id = strings.TrimSpace(id); id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.generation != generation {
		return
	}
	c.raw = ids
	c.cachedAt = c.now()
}

// Invalidate drops the cached list so the next request recomputes it
// (called after route/channel writes).
func (c *modelsCache) Invalidate() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.raw = nil
	c.cachedAt = time.Time{}
	c.generation++
	c.mu.Unlock()
}

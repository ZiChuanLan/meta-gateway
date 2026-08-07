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
	mu       sync.Mutex
	raw      []string
	cachedAt time.Time
	ttl      time.Duration
	now      func() time.Time
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
	if c.raw == nil || c.now().Sub(c.cachedAt) > c.ttl {
		return nil, false
	}
	return c.raw, true
}

// Put stores a freshly computed model id list (trimmed and de-duplicated).
func (c *modelsCache) Put(raw []string) {
	if c == nil {
		return
	}
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
	c.raw = ids
	c.cachedAt = c.now()
	c.mu.Unlock()
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
	c.mu.Unlock()
}

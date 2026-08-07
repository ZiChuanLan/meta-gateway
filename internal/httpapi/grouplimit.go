package httpapi

import (
	"hash/fnv"
	"sync"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/ratelimit"
)

// groupLimiterEntry pairs a token bucket with the configuration it was built
// from, so a config change rebuilds the bucket lazily.
type groupLimiterEntry struct {
	limiter *ratelimit.Limiter
	rpm     int
	burst   int
}

// groupRateLimiter enforces per-tenant-group rate limits on top of the
// per-key relay limiter. Groups with RatePerMinute <= 0 are unlimited.
type groupRateLimiter struct {
	mu       sync.Mutex
	limiters map[string]*groupLimiterEntry
}

func newGroupRateLimiter() *groupRateLimiter {
	return &groupRateLimiter{limiters: make(map[string]*groupLimiterEntry)}
}

// Allow checks one request against the group's bucket. Returns false with a
// wait duration when the group is exhausted.
func (g *groupRateLimiter) Allow(group *domain.KeyGroup) (bool, time.Duration) {
	if g == nil || group == nil || group.RatePerMinute <= 0 {
		return true, 0
	}
	g.mu.Lock()
	entry := g.limiters[group.Name]
	if entry == nil || entry.rpm != group.RatePerMinute || entry.burst != group.RateBurst {
		entry = &groupLimiterEntry{
			limiter: ratelimit.New(group.RatePerMinute, group.RateBurst),
			rpm:     group.RatePerMinute,
			burst:   group.RateBurst,
		}
		g.limiters[group.Name] = entry
	}
	g.mu.Unlock()
	// fnv64 of the group name as the bucket key (collisions only share a
	// bucket, mirroring the per-model limiter).
	h := fnv.New64a()
	_, _ = h.Write([]byte(group.Name))
	return entry.limiter.Allow(int64(h.Sum64()))
}

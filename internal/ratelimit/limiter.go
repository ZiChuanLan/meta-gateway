// Package ratelimit implements bounded process-local token buckets.
package ratelimit

import (
	"math"
	"sync"
	"time"
)

type bucket struct {
	tokens   float64
	updated  time.Time
	lastSeen time.Time
}

type Limiter struct {
	mu              sync.Mutex
	perSec          float64
	burst           float64
	buckets         map[int64]*bucket
	now             func() time.Time
	nextCleanup     time.Time
	cleanupInterval time.Duration
	maxIdle         time.Duration
}

const maxBuckets = 10000

func New(requestsPerMinute, burst int) *Limiter {
	return &Limiter{
		perSec:          float64(requestsPerMinute) / 60,
		burst:           float64(burst),
		buckets:         make(map[int64]*bucket),
		now:             time.Now,
		cleanupInterval: time.Hour,
		maxIdle:         time.Hour,
	}
}

// SetLimits hot-updates rate and burst. Existing buckets keep remaining tokens
// but are capped to the new burst on the next refill.
func (l *Limiter) SetLimits(requestsPerMinute, burst int) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.perSec = float64(requestsPerMinute) / 60
	l.burst = float64(burst)
	for _, bucketState := range l.buckets {
		if bucketState.tokens > l.burst {
			bucketState.tokens = l.burst
		}
	}
}

// Allow consumes one token and returns the wait duration when denied.
func (l *Limiter) Allow(key int64) (bool, time.Duration) {
	if l == nil {
		return true, 0
	}
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.perSec <= 0 || l.burst <= 0 {
		return true, 0
	}
	l.cleanupIfDue(now)
	b := l.buckets[key]
	if b == nil {
		if len(l.buckets) >= maxBuckets {
			// Keys may contain attacker-controlled dimensions (for example model
			// names). Evict one existing bucket before admitting a new one so the
			// limiter itself cannot become an unbounded map. Map eviction is
			// intentionally O(1); token buckets are soft protection, not a cache.
			for candidate := range l.buckets {
				delete(l.buckets, candidate)
				break
			}
		}
		b = &bucket{tokens: l.burst, updated: now}
		l.buckets[key] = b
	}
	elapsed := now.Sub(b.updated).Seconds()
	if elapsed > 0 {
		b.tokens = math.Min(l.burst, b.tokens+elapsed*l.perSec)
		b.updated = now
	}
	b.lastSeen = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	wait := time.Duration(math.Ceil((1 - b.tokens) / l.perSec * float64(time.Second)))
	if wait < time.Second {
		wait = time.Second
	}
	return false, wait
}

func (l *Limiter) Cleanup(maxIdle time.Duration) int {
	if l == nil || maxIdle <= 0 {
		return 0
	}
	cutoff := l.now().Add(-maxIdle)
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.cleanupBefore(cutoff)
}

func (l *Limiter) cleanupIfDue(now time.Time) {
	if l.cleanupInterval <= 0 || (!l.nextCleanup.IsZero() && now.Before(l.nextCleanup)) {
		return
	}
	l.cleanupBefore(now.Add(-l.maxIdle))
	l.nextCleanup = now.Add(l.cleanupInterval)
}

func (l *Limiter) cleanupBefore(cutoff time.Time) int {
	removed := 0
	for key, b := range l.buckets {
		if b.lastSeen.Before(cutoff) {
			delete(l.buckets, key)
			removed++
		}
	}
	return removed
}

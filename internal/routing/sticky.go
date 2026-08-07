package routing

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// StickyStore keeps a TTL-bounded in-process map of session key -> preferred
// channel. It is the affinity source for sticky routing and is safe for
// concurrent use from the relay path. Entries expire after the configured TTL,
// so a stale binding never pins a channel forever.
type StickyStore struct {
	mu      sync.Mutex
	entries map[string]stickyEntry
	ttl     time.Duration
	clock   Clock
	hits    atomic.Int64
	binds   atomic.Int64
	escapes atomic.Int64
}

type stickyEntry struct {
	ChannelID int64
	ExpiresAt time.Time
}

// StickyStats is the admin-facing summary of sticky routing activity.
type StickyStats struct {
	BoundSessions int   `json:"bound_sessions"`
	Hits          int64 `json:"hits"`
	Binds         int64 `json:"binds"`
	Escapes       int64 `json:"escapes"`
}

// StickyEntrySnapshot is one live affinity binding for the admin UI.
type StickyEntrySnapshot struct {
	Key       string    `json:"key"`
	ChannelID int64     `json:"channel_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

// NewStickyStore creates a sticky store with the given TTL. A nil clock falls
// back to the real system clock.
func NewStickyStore(ttl time.Duration, clock Clock) *StickyStore {
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	if clock == nil {
		clock = systemClock{}
	}
	return &StickyStore{entries: make(map[string]stickyEntry), ttl: ttl, clock: clock}
}

// TTL returns the configured entry lifetime.
func (s *StickyStore) TTL() time.Duration { return s.ttl }

// Lookup returns the bound channel for a session key, expiring stale entries
// on access.
func (s *StickyStore) Lookup(key string, now time.Time) (int64, bool) {
	if key == "" {
		return 0, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[key]
	if !ok {
		return 0, false
	}
	if !entry.ExpiresAt.After(now) {
		delete(s.entries, key)
		return 0, false
	}
	return entry.ChannelID, true
}

// Bind records (or refreshes) the affinity between a session key and a
// channel. Refreshing on every successful relay keeps long conversations
// sticky without ever exceeding the TTL window of inactivity.
func (s *StickyStore) Bind(key string, channelID int64, now time.Time) {
	if key == "" || channelID <= 0 {
		return
	}
	s.mu.Lock()
	s.entries[key] = stickyEntry{ChannelID: channelID, ExpiresAt: now.Add(s.ttl)}
	s.mu.Unlock()
	s.binds.Add(1)
}

// RecordHit counts a sticky hit; RecordEscape counts a bound session that had
// to fail over (the bound channel was unavailable).
func (s *StickyStore) RecordHit()    { s.hits.Add(1) }
func (s *StickyStore) RecordEscape() { s.escapes.Add(1) }

// Stats returns the current counters and the live bound-session count.
func (s *StickyStore) Stats() StickyStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(s.clock.Now())
	return StickyStats{
		BoundSessions: len(s.entries),
		Hits:          s.hits.Load(),
		Binds:         s.binds.Load(),
		Escapes:       s.escapes.Load(),
	}
}

// Snapshot returns live bindings ordered by expiry (bounded) for the admin UI.
func (s *StickyStore) Snapshot(limit int) []StickyEntrySnapshot {
	if limit <= 0 {
		limit = 100
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(s.clock.Now())
	out := make([]StickyEntrySnapshot, 0, len(s.entries))
	for key, entry := range s.entries {
		out = append(out, StickyEntrySnapshot{Key: key, ChannelID: entry.ChannelID, ExpiresAt: entry.ExpiresAt})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ExpiresAt.Before(out[j].ExpiresAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (s *StickyStore) pruneLocked(now time.Time) {
	for key, entry := range s.entries {
		if !entry.ExpiresAt.After(now) {
			delete(s.entries, key)
		}
	}
}

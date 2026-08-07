package proxy

import (
	"math"
	"sync"
	"sync/atomic"
	"time"
)

// Model-level circuit breaker, ported from AxonHub's model_circuit_breaker.go
// (source-verified): closed → half-open (weight × 0.3) → open (weight 0),
// with lazy probe release, exponential backoff (cap 8×), 30m failure TTL and
// full reset on a single success.

const (
	breakerHalfOpenThreshold = 3  // consecutive failures → half-open
	breakerOpenThreshold     = 5  // consecutive failures → open
	breakerFailureStatsTTL   = 30 * time.Minute
	breakerProbeInterval     = 5 * time.Minute
	breakerHalfOpenWeight    = 0.3
	breakerMaxBackoff        = 8 // probe-interval multiplier cap
)

type breakerState string

const (
	breakerClosed   breakerState = "closed"
	breakerHalfOpen breakerState = "half_open"
	breakerOpen     breakerState = "open"
)

type channelModelKey struct {
	channelID int64
	model     string
}

type breakerStats struct {
	state               breakerState
	consecutiveFailures int
	lastFailureAt       time.Time
	nextProbeAt         time.Time
	probingInProgress   int32
	probeAttempts       int
}

// ModelCircuitBreaker tracks per channel × model failure state.
type ModelCircuitBreaker struct {
	mu    sync.Mutex
	stats map[channelModelKey]*breakerStats
	now   func() time.Time
}

func NewModelCircuitBreaker() *ModelCircuitBreaker {
	return &ModelCircuitBreaker{
		stats: make(map[channelModelKey]*breakerStats),
		now:   time.Now,
	}
}

func (b *ModelCircuitBreaker) get(key channelModelKey) *breakerStats {
	st, ok := b.stats[key]
	if !ok {
		st = &breakerStats{state: breakerClosed}
		b.stats[key] = st
	}
	return st
}

// RecordError advances the failure state. wasProbe=true only for failures of
// an actual probe request — only those push the backoff.
func (b *ModelCircuitBreaker) RecordError(channelID int64, model string, wasProbe bool) {
	if channelID <= 0 {
		return
	}
	key := channelModelKey{channelID: channelID, model: model}
	now := b.now()
	b.mu.Lock()
	defer b.mu.Unlock()
	st := b.get(key)
	// TTL check: stale failures (older than 30m) must not keep the breaker open.
	if st.consecutiveFailures > 0 && now.Sub(st.lastFailureAt) > breakerFailureStatsTTL {
		st.consecutiveFailures = 0
		if st.state == breakerHalfOpen {
			st.state = breakerClosed
		}
	}
	st.consecutiveFailures++
	st.lastFailureAt = now
	switch {
	case st.consecutiveFailures >= breakerOpenThreshold:
		if st.state != breakerOpen {
			st.state = breakerOpen
			st.nextProbeAt = now.Add(breakerProbeInterval)
			st.probeAttempts = 0
		} else if wasProbe {
			// Only a failed probe advances the backoff; ordinary requests
			// rejected by the breaker do not, otherwise recovery is pushed
			// back forever while the upstream stays healthy.
			multiplier := math.Pow(2, float64(st.probeAttempts))
			if multiplier > breakerMaxBackoff {
				multiplier = breakerMaxBackoff
			}
			st.nextProbeAt = now.Add(time.Duration(float64(breakerProbeInterval) * multiplier))
			st.probeAttempts++
		}
	case st.consecutiveFailures >= breakerHalfOpenThreshold:
		if st.state != breakerHalfOpen {
			st.state = breakerHalfOpen
		}
	}
}

// RecordSuccess fully resets the breaker (single success heals).
func (b *ModelCircuitBreaker) RecordSuccess(channelID int64, model string) {
	if channelID <= 0 {
		return
	}
	key := channelModelKey{channelID: channelID, model: model}
	b.mu.Lock()
	defer b.mu.Unlock()
	st := b.get(key)
	st.state = breakerClosed
	st.consecutiveFailures = 0
	st.nextProbeAt = time.Time{}
	atomic.StoreInt32(&st.probingInProgress, 0)
	st.probeAttempts = 0
}

// EffectiveWeight returns the base weight adjusted by the breaker state:
// closed → base; half-open → base × 0.3; open → 0, unless the probe window is
// due AND a probe slot was acquired (lazy release) → base × 0.3.
func (b *ModelCircuitBreaker) EffectiveWeight(channelID int64, model string, base float64) float64 {
	if channelID <= 0 {
		return base
	}
	key := channelModelKey{channelID: channelID, model: model}
	now := b.now()
	b.mu.Lock()
	st := b.get(key)
	// Lazy self-heal: state is stale and no failure in TTL → back to closed.
	if st.state != breakerClosed && st.consecutiveFailures > 0 && now.Sub(st.lastFailureAt) > breakerFailureStatsTTL {
		st.state = breakerClosed
		st.consecutiveFailures = 0
		st.nextProbeAt = time.Time{}
		atomic.StoreInt32(&st.probingInProgress, 0)
		st.probeAttempts = 0
	}
	switch st.state {
	case breakerClosed:
		b.mu.Unlock()
		return base
	case breakerHalfOpen:
		b.mu.Unlock()
		return base * breakerHalfOpenWeight
	default: // open
		if now.After(st.nextProbeAt) && atomic.LoadInt32(&st.probingInProgress) == 0 {
			b.mu.Unlock()
			return base * breakerHalfOpenWeight
		}
		b.mu.Unlock()
		return 0
	}
}

// TryBeginProbe acquires the single probe slot for an open breaker whose
// probe window is due. Returns false when the channel is not probeable.
func (b *ModelCircuitBreaker) TryBeginProbe(channelID int64, model string) bool {
	if channelID <= 0 {
		return false
	}
	key := channelModelKey{channelID: channelID, model: model}
	b.mu.Lock()
	st := b.get(key)
	if st.state != breakerOpen || !b.now().After(st.nextProbeAt) {
		b.mu.Unlock()
		return false
	}
	b.mu.Unlock()
	return atomic.CompareAndSwapInt32(&st.probingInProgress, 0, 1)
}

// EndProbe releases the probe slot.
func (b *ModelCircuitBreaker) EndProbe(channelID int64, model string) {
	if channelID <= 0 {
		return
	}
	key := channelModelKey{channelID: channelID, model: model}
	b.mu.Lock()
	defer b.mu.Unlock()
	if st, ok := b.stats[key]; ok {
		atomic.StoreInt32(&st.probingInProgress, 0)
	}
}

// IsOpen reports whether the channel × model breaker is currently open
// (weight zero) — used for diagnostics/tests.
func (b *ModelCircuitBreaker) IsOpen(channelID int64, model string) bool {
	if channelID <= 0 {
		return false
	}
	key := channelModelKey{channelID: channelID, model: model}
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.stats[key]
	return ok && st.state == breakerOpen
}

// ResetForTesting clears all state (tests only).
func (b *ModelCircuitBreaker) ResetForTesting() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stats = make(map[channelModelKey]*breakerStats)
}

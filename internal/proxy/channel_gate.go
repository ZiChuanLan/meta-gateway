package proxy

import (
	"context"
	"io"
	"sync"
)

// ChannelGate is the hard per-channel concurrency limiter: a bounded semaphore
// per channel. Requests beyond the channel's MaxConcurrent limit block on a
// FIFO wait queue instead of being dropped or failed over (the upstream's
// own concurrency ceiling is respected, and a client burst never trips rate
// limiters upstream).
//
// FIFO guarantee: the semaphore is a buffered channel of capacity = limit, so
// blocked acquirers are woken in arrival order (Go channels wake waiting
// senders FIFO). No locks are held while waiting, so concurrent acquires on
// different channels never contend.
type ChannelGate struct {
	mu    sync.Mutex
	gates map[int64]*gate
	gen   uint64
}

type gate struct {
	gen   uint64
	limit int
	slots chan struct{}
}

// NewChannelGate creates an empty gate registry.
func NewChannelGate() *ChannelGate {
	return &ChannelGate{gates: make(map[int64]*gate)}
}

// Acquire blocks until a slot for the channel is free, the limit is 0
// (unlimited → immediate), or ctx is done. The returned generation must be
// passed to Release so a stale token from before a limit change is dropped
// instead of unblocking the wrong gate.
func (g *ChannelGate) Acquire(ctx context.Context, channelID int64, limit int) (uint64, error) {
	if limit <= 0 {
		return 0, nil
	}
	g.mu.Lock()
	gt, ok := g.gates[channelID]
	if !ok || gt.limit != limit {
		// First touch or a limit change: (re)create the gate. In-flight
		// tokens on the old gate are abandoned; their releases become no-ops
		// through the generation check. This keeps the window tiny and the
		// code lock-free while waiting.
		g.gen++
		gt = &gate{gen: g.gen, limit: limit, slots: make(chan struct{}, limit)}
		g.gates[channelID] = gt
	}
	g.mu.Unlock()
	select {
	case gt.slots <- struct{}{}:
		return gt.gen, nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

// Release returns a slot to the channel's gate. Releases from a previous
// generation (the gate was recreated after a limit change) are no-ops.
func (g *ChannelGate) Release(channelID int64, gen uint64) {
	if gen == 0 {
		return
	}
	g.mu.Lock()
	gt, ok := g.gates[channelID]
	g.mu.Unlock()
	if !ok || gt.gen != gen {
		return
	}
	<-gt.slots
}

// InFlight reports the number of currently held slots for a channel (used by
// tests and the concurrency-aware routing score provider).
func (g *ChannelGate) InFlight(channelID int64) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	gt, ok := g.gates[channelID]
	if !ok {
		return 0
	}
	return len(gt.slots)
}

// gateBoundBody releases the channel gate slot when the response body is
// closed, so the hard concurrency ceiling covers the full stream lifetime
// (not just the header phase). The release fires exactly once; closing the
// body repeatedly stays idempotent.
type gateBoundBody struct {
	io.ReadCloser
	once    sync.Once
	release func()
}

func (b *gateBoundBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(b.release)
	return err
}

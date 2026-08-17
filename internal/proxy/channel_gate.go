package proxy

import (
	"context"
	"io"
	"sync"
)

// ChannelGate is the hard per-channel concurrency limiter. A channel keeps a
// stable gate for its entire lifetime so changing the configured limit never
// abandons holders or waiters from the previous limit.
type ChannelGate struct {
	mu    sync.Mutex
	gates map[int64]*gate
	gen   uint64
}

type gate struct {
	mu       sync.Mutex
	gen      uint64
	limit    int
	inFlight int
	waiters  []*gateWaiter
}

type gateWaiter struct {
	ready   chan struct{}
	granted bool
}

// NewChannelGate creates an empty gate registry.
func NewChannelGate() *ChannelGate {
	return &ChannelGate{gates: make(map[int64]*gate)}
}

func (g *ChannelGate) channel(channelID int64) *gate {
	g.mu.Lock()
	defer g.mu.Unlock()
	if gt := g.gates[channelID]; gt != nil {
		return gt
	}
	g.gen++
	gt := &gate{gen: g.gen}
	g.gates[channelID] = gt
	return gt
}

// Acquire blocks until a slot for the channel is free or ctx is done. A limit
// <= 0 is unlimited, but the request is still counted so enabling a limit at
// runtime accounts for requests that were already in flight.
func (g *ChannelGate) Acquire(ctx context.Context, channelID int64, limit int) (uint64, error) {
	gt := g.channel(channelID)
	waiter := &gateWaiter{ready: make(chan struct{})}

	gt.mu.Lock()
	if gt.limit != limit {
		gt.limit = limit
		gt.dispatchLocked()
	}
	if len(gt.waiters) == 0 && gt.hasCapacityLocked() {
		gt.inFlight++
		gen := gt.gen
		gt.mu.Unlock()
		return gen, nil
	}
	gt.waiters = append(gt.waiters, waiter)
	gt.dispatchLocked()
	gt.mu.Unlock()

	select {
	case <-waiter.ready:
		return gt.gen, nil
	case <-ctx.Done():
		gt.mu.Lock()
		if waiter.granted {
			gt.mu.Unlock()
			return gt.gen, nil
		}
		for i, queued := range gt.waiters {
			if queued == waiter {
				gt.waiters = append(gt.waiters[:i], gt.waiters[i+1:]...)
				break
			}
		}
		gt.mu.Unlock()
		return 0, ctx.Err()
	}
}

func (gt *gate) hasCapacityLocked() bool {
	return gt.limit <= 0 || gt.inFlight < gt.limit
}

func (gt *gate) dispatchLocked() {
	for len(gt.waiters) > 0 && gt.hasCapacityLocked() {
		waiter := gt.waiters[0]
		gt.waiters = gt.waiters[1:]
		gt.inFlight++
		waiter.granted = true
		close(waiter.ready)
	}
}

// Release returns a slot to the channel's stable gate.
func (g *ChannelGate) Release(channelID int64, gen uint64) {
	if gen == 0 {
		return
	}
	g.mu.Lock()
	gt := g.gates[channelID]
	g.mu.Unlock()
	if gt == nil || gt.gen != gen {
		return
	}
	gt.mu.Lock()
	if gt.inFlight > 0 {
		gt.inFlight--
	}
	gt.dispatchLocked()
	gt.mu.Unlock()
}

// InFlight reports the number of currently held slots for a channel.
func (g *ChannelGate) InFlight(channelID int64) int {
	g.mu.Lock()
	gt := g.gates[channelID]
	g.mu.Unlock()
	if gt == nil {
		return 0
	}
	gt.mu.Lock()
	defer gt.mu.Unlock()
	return gt.inFlight
}

// gateBoundBody releases the channel gate slot when the response body is
// closed. Closing the body repeatedly remains idempotent.
type gateBoundBody struct {
	io.ReadCloser
	once     sync.Once
	release  func()
	closeErr error
}

func (b *gateBoundBody) Close() error {
	b.once.Do(func() {
		b.closeErr = b.ReadCloser.Close()
		if b.release != nil {
			b.release()
		}
	})
	return b.closeErr
}

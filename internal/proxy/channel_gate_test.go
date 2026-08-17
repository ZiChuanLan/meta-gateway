package proxy

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestChannelGateQueuesFIFO(t *testing.T) {
	g := NewChannelGate()
	first, err := g.Acquire(context.Background(), 7, 1)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var maxInFlight atomic.Int64
	var current atomic.Int64
	var served atomic.Int64
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			gen, acquireErr := g.Acquire(context.Background(), 7, 1)
			if acquireErr != nil {
				return
			}
			value := current.Add(1)
			for {
				maximum := maxInFlight.Load()
				if value <= maximum || maxInFlight.CompareAndSwap(maximum, value) {
					break
				}
			}
			served.Add(1)
			current.Add(-1)
			g.Release(7, gen)
		}()
	}
	time.Sleep(100 * time.Millisecond)
	g.Release(7, first)
	wg.Wait()
	if served.Load() != 3 {
		t.Fatalf("served = %d, want 3", served.Load())
	}
	if maxInFlight.Load() > 1 {
		t.Fatalf("max in-flight = %d, want 1", maxInFlight.Load())
	}
	if got := g.InFlight(7); got != 0 {
		t.Fatalf("in-flight after release = %d, want 0", got)
	}
}

func TestChannelGateUnlimitedIsTracked(t *testing.T) {
	g := NewChannelGate()
	gen, err := g.Acquire(context.Background(), 1, 0)
	if err != nil || gen == 0 {
		t.Fatalf("unlimited acquire failed (gen=%d err=%v)", gen, err)
	}
	if got := g.InFlight(1); got != 1 {
		t.Fatalf("in-flight = %d, want 1", got)
	}
	g.Release(1, gen)
	if got := g.InFlight(1); got != 0 {
		t.Fatalf("in-flight after release = %d, want 0", got)
	}
}

func TestChannelGateContextCancel(t *testing.T) {
	g := NewChannelGate()
	gen, _ := g.Acquire(context.Background(), 5, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := g.Acquire(ctx, 5, 1); err == nil {
		t.Fatal("blocked acquire must fail on context timeout")
	}
	if time.Since(start) < 60*time.Millisecond {
		t.Fatal("acquire returned before context deadline")
	}
	g.Release(5, gen)
	next, err := g.Acquire(context.Background(), 5, 1)
	if err != nil {
		t.Fatal(err)
	}
	g.Release(5, next)
}

func TestChannelGateLimitIncreaseWakesExistingWaiter(t *testing.T) {
	g := NewChannelGate()
	first, _ := g.Acquire(context.Background(), 3, 1)
	waiterDone := make(chan uint64, 1)
	go func() {
		gen, err := g.Acquire(context.Background(), 3, 1)
		if err != nil {
			waiterDone <- 0
			return
		}
		waiterDone <- gen
	}()
	time.Sleep(50 * time.Millisecond)
	third, err := g.Acquire(context.Background(), 3, 3)
	if err != nil {
		t.Fatal(err)
	}
	var second uint64
	select {
	case second = <-waiterDone:
	case <-time.After(time.Second):
		t.Fatal("old waiter was not woken after raising limit")
	}
	if second == 0 {
		t.Fatal("old waiter failed to acquire")
	}
	if got := g.InFlight(3); got != 3 {
		t.Fatalf("in-flight after live raise = %d, want 3", got)
	}
	g.Release(3, first)
	g.Release(3, second)
	g.Release(3, third)
}

func TestChannelGateLimitDecreaseWaitsForExistingHolders(t *testing.T) {
	g := NewChannelGate()
	first, _ := g.Acquire(context.Background(), 8, 2)
	second, _ := g.Acquire(context.Background(), 8, 2)
	acquired := make(chan uint64, 1)
	go func() {
		gen, err := g.Acquire(context.Background(), 8, 1)
		if err == nil {
			acquired <- gen
		}
	}()
	time.Sleep(50 * time.Millisecond)
	g.Release(8, first)
	select {
	case <-acquired:
		t.Fatal("decreased limit granted while an old holder remained")
	case <-time.After(50 * time.Millisecond):
	}
	g.Release(8, second)
	select {
	case gen := <-acquired:
		g.Release(8, gen)
	case <-time.After(time.Second):
		t.Fatal("waiter was not granted after old holders drained")
	}
}

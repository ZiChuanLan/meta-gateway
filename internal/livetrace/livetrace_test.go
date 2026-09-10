package livetrace

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestBeginFinishLifecycle(t *testing.T) {
	registry := New()
	ctx, release, ok := registry.Begin(context.Background(), "req-1", "openai", "model-a")
	if !ok {
		t.Fatal("Begin must register a fresh request ID")
	}
	// Round update flows into the snapshot.
	registry.Attempt("req-1", 1, "channel-x", "openai", "")
	snapshot := registry.Snapshot()
	if len(snapshot) != 1 || snapshot[0].Status != StatusRunning || snapshot[0].TargetChannel != "channel-x" {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	release()
	registry.Finish("req-1", StatusSuccess, "")
	ctxCanceled := false
	select {
	case <-ctx.Done():
		ctxCanceled = true
	default:
	}
	if ctxCanceled {
		t.Fatal("request context must not cancel on normal finish")
	}
	final := registry.Snapshot()
	if len(final) != 1 || final[0].Status != StatusSuccess {
		t.Fatalf("final=%+v", final)
	}
}

func TestInterruptCancelsAndMarks(t *testing.T) {
	registry := New()
	ctx, release, ok := registry.Begin(context.Background(), "req-i", "anthropic", "m")
	if !ok {
		t.Fatal("Begin failed")
	}
	defer release()
	done := make(chan struct{})
	go func() {
		<-ctx.Done()
		close(done)
	}()
	if !registry.Interrupt("req-i") {
		t.Fatal("Interrupt must find an in-flight request")
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Interrupt did not cancel the watch context")
	}
	snapshot := registry.Snapshot()
	if len(snapshot) != 1 || snapshot[0].Status != StatusInterrupted {
		t.Fatalf("interrupt state=%+v", snapshot)
	}
	// A second interrupt is a no-op (request already terminal).
	if registry.Interrupt("req-i") {
		t.Fatal("second Interrupt must not succeed")
	}
}

func TestSubscribeReceivesUpdatesAndCancellation(t *testing.T) {
	registry := New()
	ctxA, releaseA, _ := registry.Begin(context.Background(), "a", "openai", "m")
	stream := registry.Subscribe()
	defer registry.Unsubscribe(stream)

	// Snapshot replay: both live states arrive.
	received := map[string]Status{}
	var mu sync.Mutex
	go func() {
		for update := range stream {
			mu.Lock()
			received[update.RequestID] = update.Status
			mu.Unlock()
		}
	}()
	time.Sleep(50 * time.Millisecond)
	registry.Finish("a", StatusSuccess, "")
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if received["a"] != StatusSuccess {
		t.Fatalf("subscriber missed terminal state: %v", received)
	}
	releaseA()
	_ = ctxA
}

func TestClientDisconnectMarksCanceled(t *testing.T) {
	registry := New()
	parent, parentCancel := context.WithCancel(context.Background())
	_, release, ok := registry.Begin(parent, "req-c", "openai", "m")
	if !ok {
		t.Fatal("Begin failed")
	}
	defer release()
	parentCancel()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snapshot := registry.Snapshot()
		if len(snapshot) == 1 && snapshot[0].Status == StatusCanceled {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	snapshot := registry.Snapshot()
	t.Fatalf("client disconnect did not mark canceled: %+v", snapshot)
}

func TestDuplicateBeginIsRejected(t *testing.T) {
	registry := New()
	_, release1, ok1 := registry.Begin(context.Background(), "dup", "openai", "m")
	if !ok1 {
		t.Fatal("first Begin must succeed")
	}
	defer release1()
	_, release2, ok2 := registry.Begin(context.Background(), "dup", "openai", "m")
	if ok2 {
		release2()
		t.Fatal("duplicate Begin must be rejected")
	}
}

func TestFinishedRequestsArePruned(t *testing.T) {
	registry := New()
	for i := 0; i < maxFinished+10; i++ {
		id := "req-" + string(rune('a'+i%26)) + string(rune('0'+i/26))
		_, release, ok := registry.Begin(context.Background(), id, "openai", "m")
		if ok {
			registry.Finish(id, StatusSuccess, "")
			release()
		}
	}
	snapshot := registry.Snapshot()
	finished := 0
	for _, req := range snapshot {
		if req.Status.Terminal() {
			finished++
		}
	}
	if finished > maxFinished {
		t.Fatalf("finished retained %d > cap %d", finished, maxFinished)
	}
}

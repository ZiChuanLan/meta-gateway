package livetrace

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestBeginFinishLifecycle(t *testing.T) {
	registry := New()
	ctx, release, ok := registry.Begin(context.Background(), "req-1", "openai", "model-a", BeginMeta{})
	if !ok {
		t.Fatal("Begin must register a fresh request ID")
	}
	// Round update flows into the snapshot.
	registry.Attempt("req-1", 1, "channel-x", "openai", "")
	snapshot := registry.Snapshot()
	if len(snapshot) != 1 || snapshot[0].Status != StatusRunning || snapshot[0].TargetChannel != "channel-x" {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	// Production order: the transfer path finalizes the request first, the
	// deferred release runs after — release settles anything still running.
	registry.Finish("req-1", StatusSuccess, "")
	release()
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

func TestBeginPublishesImmediately(t *testing.T) {
	registry := New()
	stream := registry.Subscribe()
	defer registry.Unsubscribe(stream)

	// A fresh request must appear for subscribers the moment it begins,
	// before the first routing attempt.
	registry.Begin(context.Background(), "req-live", "openai", "m", BeginMeta{Stream: true, ClientKey: "ops-key"})
	select {
	case update := <-stream:
		if update.RequestID != "req-live" || update.Status != StatusRunning || !update.Stream || update.ClientKey != "ops-key" {
			t.Fatalf("begin frame=%+v", update)
		}
	case <-time.After(time.Second):
		t.Fatal("Begin did not publish the new request")
	}
}

func TestRetryCorrelation(t *testing.T) {
	registry := New()
	_, release, _ := registry.Begin(context.Background(), "victim", "openai", "gpt-x", BeginMeta{ClientKey: "app-key"})
	registry.Interrupt("victim")
	release()

	// Same client key + model right after the interrupt → linked retry.
	_, releaseRetry, _ := registry.Begin(context.Background(), "retry-1", "openai", "gpt-x", BeginMeta{ClientKey: "app-key"})
	snapshot := registry.Snapshot()
	for _, req := range snapshot {
		if req.RequestID == "retry-1" && req.RetryOf != "victim" {
			t.Fatalf("retry not correlated: %+v", req)
		}
	}
	releaseRetry()

	// A different model does not correlate.
	_, releaseOther, _ := registry.Begin(context.Background(), "other", "openai", "gpt-y", BeginMeta{ClientKey: "app-key"})
	for _, req := range registry.Snapshot() {
		if req.RequestID == "other" && req.RetryOf != "" {
			t.Fatalf("unrelated request correlated: %+v", req)
		}
	}
	releaseOther()
}

func TestRoundChainAndTransferMetrics(t *testing.T) {
	registry := New()
	_, release, _ := registry.Begin(context.Background(), "req-1", "openai", "m", BeginMeta{Stream: true})
	defer release()

	registry.Attempt("req-1", 1, "channel-a", "openai", "")
	registry.RoundOutcome("req-1", 1, "channel-a", false, "upstream_5xx")
	registry.Attempt("req-1", 2, "channel-b", "openai", "")
	registry.Progress("req-1", 320, 1024)
	registry.Progress("req-1", 320, 2048)
	snapshot := registry.Snapshot()
	req := snapshot[0]
	if len(req.History) != 2 ||
		req.History[0].Status != "failed" || req.History[0].Error != "upstream_5xx" ||
		req.History[1].Status != "running" {
		t.Fatalf("chain=%+v", req.History)
	}
	if req.FirstByteMs != 320 || req.BytesWritten != 2048 {
		t.Fatalf("progress=%+v", req)
	}

	registry.FinishSuccess("req-1", 320, 4096, 100, 200)
	final := registry.Snapshot()[0]
	if final.Status != StatusSuccess || final.BytesWritten != 4096 ||
		final.PromptTokens != 100 || final.CompletionTokens != 200 {
		t.Fatalf("final=%+v", final)
	}
}

func TestSubscribeReplaysSnapshotLargerThanLiveQueue(t *testing.T) {
	registry := New()
	// Retention keeps up to maxFinished requests, so the snapshot routinely
	// exceeds the live-update queue. Replaying it must never block: Subscribe
	// holds r.mu while sending, and a stalled send freezes the whole relay path
	// (2026-09-11 production outage: every model stopped responding).
	total := streamBuffer + 20
	for i := 0; i < total; i++ {
		id := "req-" + strconv.Itoa(i)
		_, release, ok := registry.Begin(context.Background(), id, "openai", "m", BeginMeta{})
		if !ok {
			t.Fatalf("Begin(%s) failed", id)
		}
		release()
	}
	got := len(registry.Snapshot())
	// Released rows settle as terminal, so retention caps the snapshot at
	// maxFinished — still larger than the live queue headroom, which is the
	// property under test.
	if got <= streamBuffer {
		t.Fatalf("snapshot size = %d, want more than the live queue headroom (%d)", got, streamBuffer)
	}

	streams := make(chan chan Request, 1)
	go func() { streams <- registry.Subscribe() }()
	var stream chan Request
	select {
	case stream = <-streams:
	case <-time.After(5 * time.Second):
		t.Fatal("Subscribe blocked replaying a snapshot larger than the live queue")
	}
	defer registry.Unsubscribe(stream)

	replayed := 0
Drain:
	for i := 0; i < got; i++ {
		select {
		case <-stream:
			replayed++
		default:
			break Drain
		}
	}
	if replayed != got {
		t.Fatalf("replayed %d updates, want %d", replayed, got)
	}

	// The registry must still be usable: queued relay work cannot be blocked.
	done := make(chan struct{})
	go func() {
		registry.Attempt("req-0", 2, "channel-x", "openai", "key")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Attempt blocked after a large snapshot replay")
	}
}

func TestInterruptCancelsAndMarks(t *testing.T) {
	registry := New()
	ctx, release, ok := registry.Begin(context.Background(), "req-i", "anthropic", "m", BeginMeta{})
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
	ctxA, releaseA, _ := registry.Begin(context.Background(), "a", "openai", "m", BeginMeta{})
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
	_, release, ok := registry.Begin(parent, "req-c", "openai", "m", BeginMeta{})
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
	_, release1, ok1 := registry.Begin(context.Background(), "dup", "openai", "m", BeginMeta{})
	if !ok1 {
		t.Fatal("first Begin must succeed")
	}
	defer release1()
	_, release2, ok2 := registry.Begin(context.Background(), "dup", "openai", "m", BeginMeta{})
	if ok2 {
		release2()
		t.Fatal("duplicate Begin must be rejected")
	}
}

func TestFinishedRequestsArePruned(t *testing.T) {
	registry := New()
	for i := 0; i < maxFinished+10; i++ {
		id := "req-" + string(rune('a'+i%26)) + string(rune('0'+i/26))
		_, release, ok := registry.Begin(context.Background(), id, "openai", "m", BeginMeta{})
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

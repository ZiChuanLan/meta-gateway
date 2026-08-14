package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/relay"
)

// TestGateRetryNoSelfDeadlock is the regression test for the audit finding:
// the gate slot was acquired inside the same-key retry loop with defer, so a
// retryable failure re-entered Acquire while still holding its own slot —
// with max_concurrent=1 the request deadlocked against itself until timeout.
// The slot is now held at the channel-attempt level, so a 429-then-200
// sequence must retry and succeed promptly.
func TestGateRetryNoSelfDeadlock(t *testing.T) {
	upstream := &queuedRelay{results: []*relay.Result{
		{StatusCode: http.StatusTooManyRequests, Header: make(http.Header)},
		{StatusCode: http.StatusOK, Header: make(http.Header)},
	}}
	service, db, _, _ := setupProxy(t, upstream)
	service.channelRetryTimes.Store(1)
	member, err := db.RouteMember.GetByID(1)
	if err != nil {
		t.Fatal(err)
	}
	channel, err := db.Channel.GetByID(member.ChannelID)
	if err != nil {
		t.Fatal(err)
	}
	channel.MaxConcurrent = 1
	if err := db.Channel.Update(channel); err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(map[string]any{
		"model": "model", "messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	started := time.Now()
	result, meta := service.ForwardWithMeta(context.Background(), Request{
		Model: "model", Method: http.MethodPost, OpenAIPath: "chat/completions",
		Body: body, ContentType: "application/json",
	})
	elapsed := time.Since(started)
	if result == nil || result.Err != nil || result.StatusCode != http.StatusOK {
		t.Fatalf("retry did not succeed: status=%d err=%v", resultStatus(result), resultErr(result))
	}
	if meta == nil || meta.ChannelID != channel.ID {
		t.Fatalf("meta = %+v", meta)
	}
	if len(upstream.calls) != 2 {
		t.Fatalf("calls = %d, want 2 (429 then 200)", len(upstream.calls))
	}
	if elapsed > 5*time.Second {
		t.Fatalf("retry took %v — self-deadlock not fixed", elapsed)
	}
	if service.gate.InFlight(channel.ID) != 0 {
		t.Fatalf("gate slot leaked: in-flight = %d after success", service.gate.InFlight(channel.ID))
	}
}

// TestGateRetryNoSelfDeadlockStream covers the stream-interrupted retry path
// (200 then silent first byte → stream_interrupted → same-key retry).
func TestGateRetryNoSelfDeadlockStream(t *testing.T) {
	upstream := &queuedRelay{results: []*relay.Result{
		{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(errorReader{err: errors.New("stream closed before first byte")})},
		{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("data: ok\n\n"))},
	}}
	service, db, _, _ := setupProxy(t, upstream)
	service.channelRetryTimes.Store(1)
	member, err := db.RouteMember.GetByID(1)
	if err != nil {
		t.Fatal(err)
	}
	channel, err := db.Channel.GetByID(member.ChannelID)
	if err != nil {
		t.Fatal(err)
	}
	channel.MaxConcurrent = 1
	if err := db.Channel.Update(channel); err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(map[string]any{
		"model": "model", "messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	result, _ := service.ForwardWithMeta(context.Background(), Request{
		Model: "model", Method: http.MethodPost, OpenAIPath: "chat/completions",
		Body: body, ContentType: "application/json", Stream: true,
	})
	if result == nil || result.Err != nil {
		t.Fatalf("stream retry did not succeed: %+v", result)
	}
	if len(upstream.calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(upstream.calls))
	}
	// The slot is handed to the body: held until the handler closes it.
	if service.gate.InFlight(channel.ID) != 1 {
		t.Fatalf("slot not held for stream lifetime: in-flight = %d", service.gate.InFlight(channel.ID))
	}
	if err := result.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if service.gate.InFlight(channel.ID) != 0 {
		t.Fatalf("slot leaked: in-flight = %d after body close", service.gate.InFlight(channel.ID))
	}
}

// TestGateBodyBoundRelease verifies the stream-lifetime semantics: the slot
// stays held after ForwardWithMeta returns until the response body is closed,
// and is released exactly once on Close.
func TestGateBodyBoundRelease(t *testing.T) {
	upstream := &queuedRelay{results: []*relay.Result{
		{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":"x","object":"chat.completion","model":"model","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))},
	}}
	service, db, _, _ := setupProxy(t, upstream)
	member, err := db.RouteMember.GetByID(1)
	if err != nil {
		t.Fatal(err)
	}
	channel, err := db.Channel.GetByID(member.ChannelID)
	if err != nil {
		t.Fatal(err)
	}
	channel.MaxConcurrent = 1
	if err := db.Channel.Update(channel); err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(map[string]any{
		"model": "model", "messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	result, _ := service.ForwardWithMeta(context.Background(), Request{
		Model: "model", Method: http.MethodPost, OpenAIPath: "chat/completions",
		Body: body, ContentType: "application/json",
	})
	if result == nil || result.Err != nil {
		t.Fatalf("result = %+v", result)
	}
	if service.gate.InFlight(channel.ID) != 1 {
		t.Fatalf("slot released before body close: in-flight = %d, want 1 (stream lifetime held)", service.gate.InFlight(channel.ID))
	}
	if err := result.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if service.gate.InFlight(channel.ID) != 0 {
		t.Fatalf("slot not released after body close: in-flight = %d", service.gate.InFlight(channel.ID))
	}
	// Idempotent: closing again must not double-release.
	if err := result.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if service.gate.InFlight(channel.ID) != 0 {
		t.Fatalf("double close re-released: in-flight = %d", service.gate.InFlight(channel.ID))
	}
}

// TestGateReleaseOnFailover ensures a 4xx failover path releases the slot
// (goto nextCandidate no longer leaks the gate).
func TestGateReleaseOnFailover(t *testing.T) {
	upstream := &queuedRelay{results: []*relay.Result{
		{StatusCode: http.StatusNotFound, Header: make(http.Header)},
	}}
	service, db, _, _ := setupProxy(t, upstream)
	service.retryTimes.Store(0)
	member, err := db.RouteMember.GetByID(1)
	if err != nil {
		t.Fatal(err)
	}
	channel, err := db.Channel.GetByID(member.ChannelID)
	if err != nil {
		t.Fatal(err)
	}
	channel.MaxConcurrent = 1
	if err := db.Channel.Update(channel); err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(map[string]any{
		"model": "model", "messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	result, _ := service.ForwardWithMeta(context.Background(), Request{
		Model: "model", Method: http.MethodPost, OpenAIPath: "chat/completions",
		Body: body, ContentType: "application/json",
	})
	if result == nil || result.Err == nil && result.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 failure result, got %+v", result)
	}
	if result.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", result.StatusCode)
	}
	if service.gate.InFlight(channel.ID) != 0 {
		t.Fatalf("slot leaked after failover: in-flight = %d", service.gate.InFlight(channel.ID))
	}
}

func resultStatus(r *relay.Result) int {
	if r == nil {
		return 0
	}
	return r.StatusCode
}

func resultErr(r *relay.Result) error {
	if r == nil {
		return nil
	}
	return r.Err
}

// errorReader immediately fails a Read, simulating an upstream that answers
// 200 and then dies before emitting a byte.
type errorReader struct{ err error }

func (r errorReader) Read([]byte) (int, error) { return 0, r.err }

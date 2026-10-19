package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/relay"
)

// fakeEventInterceptor stands in for the plugin host: the real one makes an
// HTTP call per event, and these tests are about what the stream reader does
// with the answer.
type fakeEventInterceptor struct {
	wants bool
	fn    func(HookInput) *HookResult
	calls int
	seen  []HookInput
}

func (f *fakeEventInterceptor) Wants(point HookPoint, _ string) bool {
	return f.wants && point == HookEvent
}

func (f *fakeEventInterceptor) Decide(_ context.Context, input HookInput) *HookResult {
	f.calls++
	f.seen = append(f.seen, input)
	if f.fn == nil {
		return nil
	}
	return f.fn(input)
}

// runEventHook pushes a stream through the event hook and returns what the
// client would receive.
func runEventHook(t *testing.T, stream, contentType string, interceptor Interceptor) (string, *Request) {
	t.Helper()
	req := &Request{Stream: true, Model: "gpt-test", RequestID: "req-1"}
	result := &relay.Result{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{contentType}},
		Body:       io.NopCloser(strings.NewReader(stream)),
	}
	service := &Service{}
	out := service.applyEventHook(context.Background(), req, &domain.RoutingCandidate{}, "gpt-test", 1, result, interceptor)
	body, err := io.ReadAll(out.Body)
	if err != nil {
		t.Fatalf("read stream: %v", err)
	}
	if err := out.Body.Close(); err != nil {
		t.Fatalf("close stream: %v", err)
	}
	return string(body), req
}

func eventJSON(t *testing.T, value string) json.RawMessage {
	t.Helper()
	return json.RawMessage(value)
}

// A JSON event is offered to the plugin, and an accepted answer replaces the
// frame's payload while everything else in the frame stays.
func TestEventHookRewritesOneFrame(t *testing.T) {
	stream := "event: message_delta\nid: 7\n: keepalive\ndata: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n"
	interceptor := &fakeEventInterceptor{
		wants: true,
		fn: func(input HookInput) *HookResult {
			if input.Point != HookEvent || input.EventName != "message_delta" {
				t.Fatalf("hook input = %s/%q, want event/message_delta", input.Point, input.EventName)
			}
			return &HookResult{Handled: true, PluginID: "p1", Body: eventJSON(t, `{"choices":[{"delta":{"content":"b"}}]}`)}
		},
	}
	out, req := runEventHook(t, stream, "text/event-stream", interceptor)
	want := "event: message_delta\nid: 7\n: keepalive\ndata: {\"choices\":[{\"delta\":{\"content\":\"b\"}}]}\n\n"
	if out != want {
		t.Fatalf("stream = %q, want %q", out, want)
	}
	if len(req.HookDecisions) != 1 || req.HookDecisions[0].Point != HookEvent || req.HookDecisions[0].PluginID != "p1" {
		t.Fatalf("hook decisions = %+v, want one event decision by p1", req.HookDecisions)
	}
}

// A dropped event writes nothing at all; the frames around it are unaffected.
func TestEventHookDropsOneFrame(t *testing.T) {
	stream := "data: {\"a\":1}\n\ndata: {\"b\":2}\n\n"
	interceptor := &fakeEventInterceptor{
		wants: true,
		fn: func(input HookInput) *HookResult {
			var payload map[string]int
			if err := json.Unmarshal(input.Body, &payload); err != nil {
				t.Fatalf("decode event: %v", err)
			}
			if payload["a"] == 1 {
				return &HookResult{Handled: true, Drop: true, PluginID: "p1"}
			}
			return nil
		},
	}
	out, _ := runEventHook(t, stream, "text/event-stream", interceptor)
	if want := "data: {\"b\":2}\n\n"; out != want {
		t.Fatalf("stream = %q, want %q", out, want)
	}
}

// Everything that is not a single JSON object is passed through byte for byte:
// the sentinel, plain text, arrays, and multi-line payloads.
func TestEventHookPassesThroughUnhookableFrames(t *testing.T) {
	stream := "data: [DONE]\n\ndata: plain text\n\ndata: [1,2]\n\ndata: {\"a\":\ndata: 1}\n\n"
	interceptor := &fakeEventInterceptor{
		wants: true,
		fn: func(input HookInput) *HookResult {
			t.Fatalf("unhookable frame reached the plugin: %q", input.Body)
			return &HookResult{Handled: true, Drop: true}
		},
	}
	out, _ := runEventHook(t, stream, "text/event-stream", interceptor)
	if out != stream {
		t.Fatalf("stream = %q, want it unchanged", out)
	}
	if interceptor.calls != 0 {
		t.Fatalf("plugin calls = %d, want 0", interceptor.calls)
	}
}

// Every way a plugin can fail leaves the bytes exactly as they were.
func TestEventHookFailsOpen(t *testing.T) {
	stream := "data: {\"a\":1}\n\n"
	cases := []struct {
		name string
		fn   func(HookInput) *HookResult
	}{
		{"no opinion", func(HookInput) *HookResult { return nil }},
		{"declined", func(HookInput) *HookResult { return &HookResult{Handled: false} }},
		{"error", func(HookInput) *HookResult { return &HookResult{Err: errors.New("boom")} }},
		{"not json", func(HookInput) *HookResult { return &HookResult{Handled: true, Body: eventJSON(t, "nope")} }},
		{"array body", func(HookInput) *HookResult { return &HookResult{Handled: true, Body: eventJSON(t, "[1]")} }},
		{"empty body", func(HookInput) *HookResult { return &HookResult{Handled: true} }},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			interceptor := &fakeEventInterceptor{wants: true, fn: testCase.fn}
			out, _ := runEventHook(t, stream, "text/event-stream", interceptor)
			if out != stream {
				t.Fatalf("stream = %q, want it unchanged", out)
			}
		})
	}
}

// A plugin that never claimed the model costs nothing: the body is not wrapped
// and no frame is parsed.
func TestEventHookSkippedWhenUnwanted(t *testing.T) {
	stream := "data: {\"a\":1}\n\n"
	interceptor := &fakeEventInterceptor{wants: false}
	out, _ := runEventHook(t, stream, "text/event-stream", interceptor)
	if out != stream {
		t.Fatalf("stream = %q, want it unchanged", out)
	}
	if interceptor.calls != 0 {
		t.Fatalf("plugin calls = %d, want 0", interceptor.calls)
	}
}

// A body that is not an event stream is never parsed line by line.
func TestEventHookLeavesNonEventStreamAlone(t *testing.T) {
	stream := `{"choices":[{"message":{"content":"x"}}]}`
	interceptor := &fakeEventInterceptor{
		wants: true,
		fn: func(HookInput) *HookResult {
			t.Fatal("a JSON body must not reach the event point")
			return nil
		},
	}
	out, _ := runEventHook(t, stream, "application/json", interceptor)
	if out != stream || interceptor.calls != 0 {
		t.Fatalf("stream = %q calls = %d, want it untouched", out, interceptor.calls)
	}
}

// A stream with a frame past the buffer budget stops being hooked and the rest
// is streamed as it arrived, rather than being held in memory.
func TestEventHookStopsAtOversizedFrame(t *testing.T) {
	huge := "data: {\"a\":\"" + strings.Repeat("x", maxEventHookFrameBytes+64) + "\"}\n\n"
	tail := "data: {\"b\":2}\n\n"
	interceptor := &fakeEventInterceptor{
		wants: true,
		fn: func(HookInput) *HookResult {
			t.Fatal("an oversized frame must not be offered")
			return nil
		},
	}
	out, _ := runEventHook(t, huge+tail, "text/event-stream", interceptor)
	if out != huge+tail {
		t.Fatalf("stream was altered (len %d, want %d)", len(out), len(huge)+len(tail))
	}
}

// The request context is the one that governs the plugin call, so a client that
// goes away stops paying for rewrites.
func TestEventHookUsesTheRequestContext(t *testing.T) {
	type key struct{}
	ctx := context.WithValue(context.Background(), key{}, "request")
	req := &Request{Stream: true, Model: "m"}
	result := &relay.Result{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader("data: {\"a\":1}\n\n")),
	}
	var seen context.Context
	interceptor := &fakeEventInterceptor{wants: true, fn: func(HookInput) *HookResult { return nil }}
	service := &Service{}
	service.applyEventHook(ctx, req, &domain.RoutingCandidate{}, "m", 1, result, interceptor)
	wrapped := result.Body.(*eventHookBody)
	seen = wrapped.ctx
	if seen.Value(key{}) != "request" {
		t.Fatalf("event hook context did not carry the request context")
	}
}

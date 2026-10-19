package proxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/relay"
)

// Plugin stream hook seam.
//
// A streaming answer is a sequence of SSE frames, so there is no complete
// document for the HookResponse point to rewrite. The event point closes that
// gap one frame at a time: the body is wrapped in a reader that parses frames
// on the way to the client and offers each JSON `data:` payload to the plugin
// chain.
//
// Three properties are load-bearing:
//
//  1. Fail-open, byte for byte. A frame is only replaced when the plugin
//     returned a complete, valid decision. Every other path — no opinion, a
//     timeout, a broken answer, a frame that is not a single JSON object —
//     writes the original bytes through unchanged.
//  2. Only text/event-stream bodies are parsed. Anything else (JSON, audio,
//     images) is left to the non-streaming HookResponse point or to no hook at
//     all; parsing a binary body line by line would buffer it whole.
//  3. A rewrite costs one call per frame, so an answer with a very large
//     number of frames stops being hooked after maxEventHookCalls and streams
//     through untouched. The budget bounds the plugin, not the client: a
//     plugin that fails trips the same circuit breaker as every other point.
const (
	// maxEventHookFrameBytes bounds one buffered frame and one replacement
	// body. A frame past it passes through unhooked rather than being held.
	maxEventHookFrameBytes = 1 << 20
	// maxEventHookCalls bounds how many frames of one answer are offered to the
	// plugin chain.
	maxEventHookCalls = 4000
)

// applyEventHook wraps a streaming answer so each SSE event is offered to the
// plugin chain. It returns the result untouched unless the answer really is an
// event stream for a model some plugin claimed.
func (s *Service) applyEventHook(
	ctx context.Context,
	req *Request,
	candidate *domain.RoutingCandidate,
	upstreamModel string,
	attempt int,
	result *relay.Result,
	interceptor Interceptor,
) *relay.Result {
	if req == nil || candidate == nil || result == nil || result.Body == nil {
		return result
	}
	if !req.Stream || result.Err != nil || result.StatusCode < 200 || result.StatusCode >= 300 {
		return result
	}
	if !isEventStream(result.Header) {
		// A client that asked for a stream but got whole JSON is answered by the
		// response point; a binary stream is never parsed.
		return result
	}
	if !interceptor.Wants(HookEvent, req.Model) {
		return result
	}
	base := HookInput{
		Point:         HookEvent,
		RequestID:     req.RequestID,
		Model:         req.Model,
		Stream:        true,
		Headers:       hookVisibleHeaders(req.Headers),
		RouteGroup:    req.RouteGroup,
		Attempt:       attempt,
		ChannelID:     candidate.Channel.ID,
		ChannelName:   candidate.Channel.Name,
		UpstreamModel: upstreamModel,
		UpstreamURL:   req.UpstreamURLActual,
	}
	result.Body = &eventHookBody{
		src:         result.Body,
		reader:      bufio.NewReaderSize(result.Body, 64<<10),
		ctx:         ctx,
		base:        base,
		req:         req,
		interceptor: interceptor,
	}
	return result
}

// isEventStream reports whether the answer carries an SSE body.
func isEventStream(header http.Header) bool {
	return strings.Contains(strings.ToLower(header.Get("Content-Type")), "event-stream")
}

// eventHookBody rewrites an SSE stream one frame at a time.
//
// The request context is kept because the plugin call has to be cancellable
// with the client that is still receiving this stream; the body outlives the
// hook setup but never the request.
type eventHookBody struct {
	src         io.ReadCloser
	reader      *bufio.Reader
	ctx         context.Context
	base        HookInput
	req         *Request
	interceptor Interceptor

	pending     []byte
	last        *HookResult
	calls       int
	passthrough bool
	recorded    bool
	closed      bool
}

func (b *eventHookBody) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for len(b.pending) == 0 {
		if b.passthrough {
			return b.reader.Read(p)
		}
		frame, keep, changed, err := b.next()
		if err != nil {
			return 0, err
		}
		if changed {
			b.record()
		}
		if !keep || len(frame) == 0 {
			continue
		}
		b.pending = frame
	}
	n := copy(p, b.pending)
	b.pending = b.pending[n:]
	return n, nil
}

func (b *eventHookBody) Close() error {
	if b.closed {
		return nil
	}
	b.closed = true
	return b.src.Close()
}

// next reads one frame and answers what to write: the bytes, whether to write
// them at all (a dropped frame writes nothing), and whether a plugin changed
// the answer.
func (b *eventHookBody) next() ([]byte, bool, bool, error) {
	raw, lines, oversized, err := readSSEFrame(b.reader, maxEventHookFrameBytes)
	if err != nil {
		return nil, false, false, err
	}
	if oversized {
		// Buffering this frame would have cost unbounded memory: stop hooking
		// and stream the rest as it arrives.
		b.passthrough = true
		return raw, true, false, nil
	}
	return b.rewrite(raw, lines)
}

func (b *eventHookBody) rewrite(raw []byte, lines [][]byte) ([]byte, bool, bool, error) {
	data, event, dataLines := sseFrameFields(lines)
	if dataLines != 1 || len(data) == 0 || b.calls >= maxEventHookCalls {
		return raw, true, false, nil
	}
	// Only a single JSON object can be re-encoded. "[DONE]", plain text and
	// multi-line payloads go through as they came.
	if !isJSONObject(data) {
		return raw, true, false, nil
	}
	input := b.base
	input.Body = json.RawMessage(append([]byte(nil), data...))
	input.EventName = event
	b.calls++
	decision := b.interceptor.Decide(b.ctx, input)
	if decision == nil || decision.Err != nil || !decision.Handled {
		return raw, true, false, nil
	}
	if decision.Drop {
		b.last = decision
		return nil, false, true, nil
	}
	body := bytes.TrimSpace(decision.Body)
	if len(body) == 0 || len(body) > maxEventHookFrameBytes || !isJSONObject(body) {
		return raw, true, false, nil
	}
	b.last = decision
	return rebuildSSEFrame(lines, body), true, true, nil
}

// last is the decision most recently applied to this stream, kept so the log
// row can name the plugin once instead of once per frame.

func (b *eventHookBody) record() {
	if b.recorded {
		return
	}
	b.recorded = true
	recordHookDecision(b.req, HookDecision{
		Point:      HookEvent,
		PluginID:   b.last.PluginID,
		Reason:     b.last.Reason,
		Confidence: b.last.Confidence,
		LatencyMs:  b.last.LatencyMs,
	})
	log.Printf("proxy: plugin %q rewrote a stream event (channel=%d model=%s request_id=%s)",
		b.last.PluginID, b.base.ChannelID, b.base.Model, b.base.RequestID)
}

// readSSEFrame reads up to and including the blank line that ends a frame, or
// stops at EOF. oversized reports that the frame passed the budget and the
// caller should stop hooking the rest of the stream.
func readSSEFrame(r *bufio.Reader, limit int) (raw []byte, lines [][]byte, oversized bool, err error) {
	for {
		line, readErr := r.ReadBytes('\n')
		if len(line) > 0 {
			raw = append(raw, line...)
			lines = append(lines, line)
			if len(raw) > limit {
				return raw, lines, true, nil
			}
		}
		if readErr != nil {
			if len(raw) == 0 {
				return nil, nil, false, readErr
			}
			// A stream that ends without its blank line is still a frame.
			return raw, lines, false, nil
		}
		if isBlankFrameLine(line) {
			return raw, lines, false, nil
		}
	}
}

// isBlankFrameLine reports the empty line that terminates an SSE frame.
func isBlankFrameLine(line []byte) bool {
	trimmed := bytes.TrimRight(line, "\r\n")
	return len(trimmed) == 0 && len(line) > 0
}

// sseFrameFields extracts the data payload and event name from one frame.
// Multi-line data is reported through dataLines so the caller can skip it
// instead of guessing how the two halves join.
func sseFrameFields(lines [][]byte) (data []byte, event string, dataLines int) {
	for _, line := range lines {
		trimmed := bytes.TrimRight(line, "\r\n")
		if len(trimmed) == 0 || trimmed[0] == ':' {
			continue
		}
		name, value, ok := bytes.Cut(trimmed, []byte(":"))
		if !ok {
			continue
		}
		value = bytes.TrimPrefix(value, []byte(" "))
		switch string(name) {
		case "data":
			data = value
			dataLines++
		case "event":
			event = string(value)
		}
	}
	return data, event, dataLines
}

// rebuildSSEFrame replaces the frame's data payload and keeps everything else
// (event name, id, retry, comments, terminators) where it was.
func rebuildSSEFrame(lines [][]byte, body []byte) []byte {
	var out bytes.Buffer
	out.Grow(len(body) + 64)
	for _, line := range lines {
		trimmed := bytes.TrimRight(line, "\r\n")
		if len(trimmed) > 0 {
			if name, _, ok := bytes.Cut(trimmed, []byte(":")); ok && string(name) == "data" {
				out.WriteString("data: ")
				out.Write(body)
				out.WriteByte('\n')
				continue
			}
		}
		out.Write(line)
		if !bytes.HasSuffix(line, []byte("\n")) {
			out.WriteByte('\n')
		}
	}
	return out.Bytes()
}

// isJSONObject reports whether the payload is a complete JSON object, the only
// shape a plugin may rewrite.
func isJSONObject(payload []byte) bool {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return false
	}
	return json.Valid(trimmed)
}

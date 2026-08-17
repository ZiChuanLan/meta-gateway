// Package proxy orchestrates routing, retries, upstream relay, and attempt logs.
package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/lan/meta-gateway/internal/relay"
)

// preserveReadLimit caps how many bytes preserve() buffers from an upstream
// response before handing the body back. Successful responses may legitimately
// be large (non-stream completions), so only error responses are capped to the
// error-text bound; the failure body is never surfaced whole to the client and
// only its leading text matters for retry classification.
const preserveErrorReadLimit = 64 * 1024

// preserveBodyReadLimit is the cap for non-error bodies that must be replayed,
// matching the historical relay bound.
const preserveBodyReadLimit = 10 * 1024 * 1024

func preserve(result *relay.Result) *relay.Result {
	if result == nil || result.Body == nil {
		return result
	}
	limit := int64(preserveBodyReadLimit)
	if result.StatusCode >= 400 {
		limit = preserveErrorReadLimit
	}
	body, err := io.ReadAll(io.LimitReader(result.Body, limit))
	// The original (possibly live) body is fully consumed; close it before
	// handing the replay buffer to the caller so the connection is released.
	_ = result.Body.Close()
	if err != nil {
		return &relay.Result{StatusCode: result.StatusCode, Header: result.Header, LatencyMs: result.LatencyMs, Err: fmt.Errorf("proxy: preserve upstream response: %w", err)}
	}
	return &relay.Result{StatusCode: result.StatusCode, Header: result.Header.Clone(), LatencyMs: result.LatencyMs, Body: io.NopCloser(bytes.NewReader(body))}
}

// streamFirstByteTimeout bounds how long a 200 stream may stay silent before
// its first byte. An upstream that answers 200 and then hangs (half-open
// connection) would otherwise pin the client forever with no data — this
// converts that into a retryable first-byte failure so the request can fail
// over to the next channel.
const streamFirstByteTimeout = 30 * time.Second

// streamIdleTimeout bounds the gap between bytes after a stream has started.
// Keepalive frames reset this naturally; a permanently stalled upstream does
// not hold a channel slot forever.
const streamIdleTimeout = 2 * time.Minute

// maxStreamFirstChunkBytes bounds how much of a stream prefix we buffer before
// committing the response to the client.
const maxStreamFirstChunkBytes = 256 * 1024

// nonStreamRequestTimeout caps the total duration of a non-streaming upstream
// attempt (request + full body read). Streaming requests are exempt.
const nonStreamRequestTimeout = 5 * time.Minute

// peekFirstChunk reads the leading bytes of an upstream stream response. SSE
// frames end with a blank line, so reading until "\n\n" (or a bounded amount of
// data) lets the gateway detect a 200 that immediately died and fail over to
// the next channel instead of surfacing a silent truncated response.
func peekFirstChunk(body io.Reader) ([]byte, error) {
	var buffered bytes.Buffer
	buffer := make([]byte, 4096)
	for {
		readN, readErr := body.Read(buffer)
		if readN > 0 {
			buffered.Write(buffer[:readN])
			if buffered.Len() >= maxStreamFirstChunkBytes || hasSSEFrameBoundary(buffered.Bytes()) {
				return buffered.Bytes(), nil
			}
		}
		if readErr != nil {
			if readErr == io.EOF && buffered.Len() > 0 {
				return buffered.Bytes(), nil
			}
			return nil, readErr
		}
	}
}

func hasSSEFrameBoundary(data []byte) bool {
	return bytes.Contains(data, []byte("\n\n")) || bytes.Contains(data, []byte("\r\n\r\n"))
}

// peekFirstChunkWithTimeout bounds peekFirstChunk: a silent 200 stream that
// never emits a byte is closed and reported as a first-byte timeout so the
// candidate loop can fail over (the client has not received anything yet).
func peekFirstChunkWithTimeout(body io.ReadCloser, timeout time.Duration) ([]byte, error) {
	type chunk struct {
		data []byte
		err  error
	}
	ch := make(chan chunk, 1)
	go func() {
		data, err := peekFirstChunk(body)
		ch <- chunk{data, err}
	}()
	select {
	case res := <-ch:
		return res.data, res.err
	case <-time.After(timeout):
		// Closing the body unblocks the pending Read (http guarantees this),
		// which releases the goroutine; the result is discarded.
		_ = body.Close()
		return nil, fmt.Errorf("first byte timeout after %s", timeout)
	}
}

// isSilentSSEStart reports whether a buffered SSE prefix contains only
// terminal or empty frames — i.e. the stream is 200 but will deliver no
// content. A standard OpenAI first chunk carries {"role":"assistant"} with
// no content, which is NOT silent; only frames with neither content nor role,
// or an immediate [DONE], are treated as silent failure.
func isSilentSSEStart(prefix []byte) bool {
	if len(prefix) == 0 {
		return false // empty prefix is handled by peekErr (death before first byte)
	}
	seenAnyData := false
	for _, line := range bytes.Split(prefix, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		payload := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
		if len(payload) == 0 {
			continue
		}
		seenAnyData = true
		if bytes.Equal(payload, []byte("[DONE]")) {
			continue
		}
		var frame struct {
			Choices []struct {
				Delta struct {
					Content          json.RawMessage   `json:"content"`
					Role             string            `json:"role"`
					ToolCalls        []json.RawMessage `json:"tool_calls"`
					FunctionCall     json.RawMessage   `json:"function_call"`
					ReasoningContent json.RawMessage   `json:"reasoning_content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(payload, &frame); err != nil {
			// Non-JSON SSE (e.g. raw keep-alive comments) — not silent.
			return false
		}
		if frame.Choices == nil {
			// A JSON frame without a choices array is not a standard OpenAI
			// shape (e.g. nonstandard upstreams, proxies); never classify it
			// as silent — fail open rather than retry valid streams.
			return false
		}
		if len(frame.Choices) == 0 {
			continue // usage-only frame; not content, but also not fatal yet
		}
		for _, choice := range frame.Choices {
			if jsonValuePresent(choice.Delta.Content) || choice.Delta.Role != "" || len(choice.Delta.ToolCalls) > 0 ||
				jsonValuePresent(choice.Delta.FunctionCall) || jsonValuePresent(choice.Delta.ReasoningContent) {
				return false // real content or a proper role header frame
			}
		}
	}
	// Silent only when we saw data frames and none carried content/role.
	return seenAnyData
}

func jsonValuePresent(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) > 0 && !bytes.Equal(raw, []byte("null")) && !bytes.Equal(raw, []byte(`""`)) && !bytes.Equal(raw, []byte("[]")) && !bytes.Equal(raw, []byte("{}"))
}

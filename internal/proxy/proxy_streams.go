// Package proxy orchestrates routing, retries, upstream relay, and attempt logs.
package proxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
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
// its first content. An upstream that answers 200 and then hangs (half-open
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

// streamPrefixDecision is what the frame walk decided about a buffered SSE
// prefix.
type streamPrefixDecision int

const (
	// streamNeedMore: every frame so far is inconclusive (role header, usage
	// frames, keep-alive comments) — keep reading.
	streamNeedMore streamPrefixDecision = iota
	// streamCommit: real content (or a non-OpenAI shape we must not judge) —
	// safe to hand the prefix to the client.
	streamCommit
	// streamSilent: the stream ended (EOF or [DONE]) without ever carrying
	// content — a 200 the client would see as an empty answer.
	streamSilent
)

// classifyStreamFrames walks the COMPLETE SSE frames in prefix (the trailing
// partial frame is ignored — more data may still arrive for it) and reports
// commit/need-more/silent. Fail-open rules: non-JSON data frames and JSON
// without a choices array (e.g. Anthropic-shaped streams after translation)
// commit immediately — the gateway only judges shapes it understands.
func classifyStreamFrames(prefix []byte) streamPrefixDecision {
	normalized := bytes.ReplaceAll(prefix, []byte("\r\n"), []byte("\n"))
	frames := bytes.Split(normalized, []byte("\n\n"))
	if len(frames) > 0 {
		frames = frames[:len(frames)-1]
	}
	for _, frame := range frames {
		if decision, ok := classifySSEFrame(frame); ok {
			return decision
		}
	}
	return streamNeedMore
}

// classifySSEFrame judges one complete SSE frame. ok=false means the frame is
// inconclusive (keep-alive comment, role header, usage-only frame).
func classifySSEFrame(frame []byte) (streamPrefixDecision, bool) {
	for _, line := range bytes.Split(frame, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		payload := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
		if len(payload) == 0 {
			continue
		}
		if bytes.Equal(payload, []byte("[DONE]")) {
			return streamSilent, true
		}
		var probe struct {
			Choices *json.RawMessage `json:"choices"`
		}
		if err := json.Unmarshal(payload, &probe); err != nil {
			// Non-JSON data (raw bytes, exotic shapes) — commit, don't judge.
			return streamCommit, true
		}
		if probe.Choices == nil {
			// JSON without a choices array (non-OpenAI contract, e.g. the
			// Anthropic pivot) — never classify it; commit.
			return streamCommit, true
		}
		var frameBody struct {
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
		if err := json.Unmarshal(payload, &frameBody); err != nil {
			return streamCommit, true
		}
		if len(frameBody.Choices) == 0 {
			continue // usage-only frame; keep reading
		}
		for _, choice := range frameBody.Choices {
			if jsonValuePresent(choice.Delta.Content) || len(choice.Delta.ToolCalls) > 0 ||
				jsonValuePresent(choice.Delta.FunctionCall) || jsonValuePresent(choice.Delta.ReasoningContent) {
				return streamCommit, true
			}
		}
		// Role-only header frame (or an empty delta): not evidence of content
		// either way — keep reading until real content or the stream ends.
	}
	return streamNeedMore, false
}

// peekStreamStart reads the leading frames of an upstream stream response
// until it can decide whether the 200 will deliver content. It returns the
// buffered prefix (to replay on commit) and whether the stream ended without
// content (silent failure — the client would see an empty answer).
func peekStreamStart(body io.Reader) (prefix []byte, silent bool, err error) {
	var buffered bytes.Buffer
	buffer := make([]byte, 4096)
	for {
		readN, readErr := body.Read(buffer)
		if readN > 0 {
			buffered.Write(buffer[:readN])
			if buffered.Len() >= maxStreamFirstChunkBytes {
				// Bounded fail-open: judge only what we understand.
				return buffered.Bytes(), false, nil
			}
			switch classifyStreamFrames(buffered.Bytes()) {
			case streamCommit:
				return buffered.Bytes(), false, nil
			case streamSilent:
				return buffered.Bytes(), true, nil
			}
		}
		if readErr != nil {
			if readErr == io.EOF && buffered.Len() > 0 {
				// Stream ended without any content frame — silent failure.
				return buffered.Bytes(), true, nil
			}
			return nil, false, readErr
		}
	}
}

// peekStreamStartWithTimeout bounds peekStreamStart: a 200 stream that never
// emits content (nor an end) is closed and reported as a first-byte timeout
// so the candidate loop can fail over.
func peekStreamStartWithTimeout(body io.ReadCloser, timeout time.Duration) (prefix []byte, silent bool, err error) {
	type outcome struct {
		prefix []byte
		silent bool
		err    error
	}
	ch := make(chan outcome, 1)
	go func() {
		prefix, silent, err := peekStreamStart(body)
		ch <- outcome{prefix, silent, err}
	}()
	select {
	case res := <-ch:
		return res.prefix, res.silent, res.err
	case <-time.After(timeout):
		// Closing the body unblocks the pending Read (http guarantees this),
		// which releases the goroutine; the result is discarded.
		_ = body.Close()
		return nil, false, fmt.Errorf("first byte timeout after %s", timeout)
	}
}

// isEmptyChatSuccess reports whether a 2xx non-streaming chat/completions
// body would hand the client an empty answer: no choices, every choice with
// an empty message, or a 2xx carrying an error object. A content_filter
// finish reason is a real answer and passes. Bodies that are empty, non-JSON,
// or lack a choices key (other endpoints/contracts) fail open.
func isEmptyChatSuccess(body []byte) bool {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return true
	}
	if !bytes.HasPrefix(trimmed, []byte("{")) {
		return false
	}
	var payload struct {
		Choices *json.RawMessage `json:"choices"`
		Error   json.RawMessage  `json:"error"`
	}
	if err := json.Unmarshal(trimmed, &payload); err != nil {
		return false
	}
	if len(payload.Error) > 0 && !bytes.Equal(bytes.TrimSpace(payload.Error), []byte("null")) {
		return true
	}
	if payload.Choices == nil {
		return false
	}
	var frame struct {
		Choices []struct {
			Message struct {
				Content          json.RawMessage   `json:"content"`
				ToolCalls        []json.RawMessage `json:"tool_calls"`
				FunctionCall     json.RawMessage   `json:"function_call"`
				ReasoningContent json.RawMessage   `json:"reasoning_content"`
				Refusal          json.RawMessage   `json:"refusal"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(trimmed, &frame); err != nil {
		return false
	}
	if len(frame.Choices) == 0 {
		return true
	}
	for _, choice := range frame.Choices {
		if jsonValuePresent(choice.Message.Content) || len(choice.Message.ToolCalls) > 0 ||
			jsonValuePresent(choice.Message.FunctionCall) || jsonValuePresent(choice.Message.ReasoningContent) ||
			jsonValuePresent(choice.Message.Refusal) {
			return false
		}
	}
	for _, choice := range frame.Choices {
		if choice.FinishReason == "content_filter" {
			return false
		}
	}
	return true
}

func jsonValuePresent(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) > 0 && !bytes.Equal(raw, []byte("null")) && !bytes.Equal(raw, []byte(`""`)) && !bytes.Equal(raw, []byte("[]")) && !bytes.Equal(raw, []byte("{}"))
}

// —— Per-channel stream policy ——

// forceStreamRequestBody rewrites a chat/completions body to ask the upstream
// for a stream (with usage reporting, so the aggregated completion still
// meters). A body that is not a JSON object fails open: the caller drops the
// policy for this attempt and the client's original shape goes through.
func forceStreamRequestBody(body []byte) ([]byte, bool) {
	return rewriteStreamFlag(body, true)
}

// forceNonStreamRequestBody rewrites a chat/completions body to a plain
// non-streaming upstream request.
func forceNonStreamRequestBody(body []byte) ([]byte, bool) {
	return rewriteStreamFlag(body, false)
}

func rewriteStreamFlag(body []byte, stream bool) ([]byte, bool) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil || payload == nil {
		return body, false
	}
	payload["stream"] = stream
	if stream {
		options, ok := payload["stream_options"].(map[string]any)
		if !ok {
			options = map[string]any{}
			payload["stream_options"] = options
		}
		options["include_usage"] = true
	} else {
		delete(payload, "stream_options")
	}
	out, err := json.Marshal(payload)
	if err != nil {
		return body, false
	}
	return out, true
}

// aggregateChatStream drains an OpenAI-shaped SSE stream and rebuilds the
// equivalent non-streaming chat.completion JSON: delta fragments merge in
// order (content, reasoning, tool-call arguments by index), the last
// non-null finish_reason and usage frame win. Used by channels whose policy
// forces a streaming upstream for non-streaming clients.
func aggregateChatStream(body io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(body)
	if err != nil {
		return nil, err
	}
	completion := map[string]any{}
	message := map[string]any{}
	content := strings.Builder{}
	reasoning := strings.Builder{}
	var toolCalls []map[string]any
	finishReason := any(nil)
	var usage map[string]any
	for _, frame := range bytes.Split(normalizeSSEFrames(raw), []byte("\n\n")) {
		for _, line := range bytes.Split(frame, []byte("\n")) {
			trimmed := bytes.TrimSpace(line)
			if !bytes.HasPrefix(trimmed, []byte("data:")) {
				continue
			}
			payload := bytes.TrimSpace(trimmed[len("data:"):])
			if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
				continue
			}
			var chunk struct {
				ID      string `json:"id"`
				Created any    `json:"created"`
				Model   string `json:"model"`
				Choices []struct {
					Delta struct {
						Role             string `json:"role"`
						Content          string `json:"content"`
						ReasoningContent string `json:"reasoning_content"`
						ToolCalls        []struct {
							Index    int    `json:"index"`
							ID       string `json:"id"`
							Type     string `json:"type"`
							Function struct {
								Name      string `json:"name"`
								Arguments string `json:"arguments"`
							} `json:"function"`
						} `json:"tool_calls"`
					} `json:"delta"`
					FinishReason any            `json:"finish_reason"`
					FinishRsnStr string         `json:"-"`
					Usage        map[string]any `json:"-"`
					Raw          map[string]any `json:"-"`
				} `json:"choices"`
				Usage map[string]any `json:"usage"`
			}
			// Tool-call merging needs the raw map; decode twice, it is cheap
			// relative to the relay itself.
			if err := json.Unmarshal(payload, &chunk); err != nil {
				continue
			}
			if completion["id"] == nil && chunk.ID != "" {
				completion["id"] = chunk.ID
			}
			if completion["created"] == nil && chunk.Created != nil {
				completion["created"] = chunk.Created
			}
			if completion["model"] == nil && chunk.Model != "" {
				completion["model"] = chunk.Model
			}
			if chunk.Usage != nil {
				usage = chunk.Usage
			}
			for _, choice := range chunk.Choices {
				if choice.Delta.Role != "" {
					message["role"] = choice.Delta.Role
				}
				content.WriteString(choice.Delta.Content)
				reasoning.WriteString(choice.Delta.ReasoningContent)
				for _, call := range choice.Delta.ToolCalls {
					for toolCalls == nil || call.Index >= len(toolCalls) {
						toolCalls = append(toolCalls, map[string]any{
							"index": len(toolCalls), "id": "", "type": "", "function": map[string]any{"name": "", "arguments": ""},
						})
					}
					slot := toolCalls[call.Index]
					if call.ID != "" {
						slot["id"] = call.ID
					}
					if call.Type != "" {
						slot["type"] = call.Type
					}
					fn := slot["function"].(map[string]any)
					if call.Function.Name != "" {
						fn["name"] = call.Function.Name
					}
					fn["arguments"] = fn["arguments"].(string) + call.Function.Arguments
				}
				if choice.FinishReason != nil {
					finishReason = choice.FinishReason
				}
			}
		}
	}
	if role, _ := message["role"].(string); role == "" {
		message["role"] = "assistant"
	}
	message["content"] = content.String()
	if reasoning.Len() > 0 {
		message["reasoning_content"] = reasoning.String()
	}
	if toolCalls != nil {
		message["tool_calls"] = toolCalls
	}
	choiceOut := map[string]any{"index": 0, "message": message, "finish_reason": finishReason}
	completion["object"] = "chat.completion"
	completion["choices"] = []any{choiceOut}
	if usage != nil {
		completion["usage"] = usage
	}
	if _, ok := completion["created"]; !ok {
		completion["created"] = time.Now().Unix()
	}
	out, err := json.Marshal(completion)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// normalizeSSEFrames normalizes CRLF to LF so frame splitting is uniform.
func normalizeSSEFrames(raw []byte) []byte {
	if bytes.IndexByte(raw, '\r') < 0 {
		return raw
	}
	return bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
}

// synthesizeStreamFromCompletion wraps a complete chat.completion JSON into a
// minimal OpenAI chunk stream — one delta carrying the whole message, one
// close frame with the finish reason and usage, then [DONE]. Used by
// channels whose policy forces a non-streaming upstream for streaming
// clients: the client still receives valid SSE, just without token-by-token
// pacing.
func synthesizeStreamFromCompletion(completion []byte) ([]byte, error) {
	var parsed struct {
		ID      string `json:"id"`
		Created any    `json:"created"`
		Model   string `json:"model"`
		Choices []struct {
			Message map[string]any `json:"message"`
		} `json:"choices"`
		Usage map[string]any `json:"usage"`
	}
	if err := json.Unmarshal(completion, &parsed); err != nil {
		return nil, err
	}
	if len(parsed.Choices) == 0 {
		return nil, errors.New("completion has no choices")
	}
	delta := map[string]any{}
	message := parsed.Choices[0].Message
	if role, _ := message["role"].(string); role != "" {
		delta["role"] = role
	}
	if content, ok := message["content"].(string); ok && content != "" {
		delta["content"] = content
	}
	if reasoning, ok := message["reasoning_content"].(string); ok && reasoning != "" {
		delta["reasoning_content"] = reasoning
	}
	if toolCalls, ok := message["tool_calls"]; ok {
		delta["tool_calls"] = toolCalls
	}
	base := map[string]any{
		"id": parsed.ID, "object": "chat.completion.chunk",
		"created": parsed.Created, "model": parsed.Model,
		"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": nil}},
	}
	final := map[string]any{
		"id": parsed.ID, "object": "chat.completion.chunk",
		"created": parsed.Created, "model": parsed.Model,
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}},
	}
	if parsed.Usage != nil {
		final["usage"] = parsed.Usage
	}
	stream := bytes.Buffer{}
	for _, chunk := range []map[string]any{base, final} {
		encoded, err := json.Marshal(chunk)
		if err != nil {
			return nil, err
		}
		stream.Write([]byte("data: "))
		stream.Write(encoded)
		stream.Write([]byte("\n\n"))
	}
	stream.Write([]byte("data: [DONE]\n\n"))
	return stream.Bytes(), nil
}

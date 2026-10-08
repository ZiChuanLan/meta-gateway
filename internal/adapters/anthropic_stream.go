package adapters

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// AnthropicToOpenAIStream converts Anthropic Messages SSE into OpenAI
// chat.completion.chunk SSE so OpenAI-compatible clients can stream through
// Anthropic-family channels.
type AnthropicToOpenAIStream struct {
	streamSource
	frames sseFrameReader

	// pending holds already-formatted OpenAI SSE bytes ready to return.
	pending bytes.Buffer

	messageID string
	model     string
	created   int64
	roleSent  bool
	done      bool
	sourceErr error

	// tools maps an Anthropic content-block index onto the OpenAI tool_calls
	// index its arguments belong to. Without the content_block_start that
	// registers it, streamed tool input has nowhere to go.
	tools         map[int]int
	nextToolIndex int
}

// NewAnthropicToOpenAIStream wraps an Anthropic SSE body.
func NewAnthropicToOpenAIStream(source io.ReadCloser) *AnthropicToOpenAIStream {
	return &AnthropicToOpenAIStream{
		streamSource: newStreamSource(source),
		frames:       newSSEFrameReader(source, true),
		created:      time.Now().Unix(),
	}
}

func (s *AnthropicToOpenAIStream) Read(p []byte) (int, error) {
	for s.pending.Len() == 0 && !s.done && s.sourceErr == nil {
		if err := s.pullEvent(); err != nil {
			s.sourceErr = err
			break
		}
	}
	if s.pending.Len() > 0 {
		n, _ := s.pending.Read(p)
		if s.pending.Len() == 0 && s.done {
			return n, io.EOF
		}
		return n, nil
	}
	if s.sourceErr != nil {
		if s.sourceErr == io.EOF {
			if !s.done {
				s.emitDone()
				s.done = true
				if s.pending.Len() > 0 {
					n, _ := s.pending.Read(p)
					return n, nil
				}
			}
			return 0, io.EOF
		}
		return 0, s.sourceErr
	}
	return 0, io.EOF
}

func (s *AnthropicToOpenAIStream) pullEvent() error {
	event, data, err := s.frames.next()
	if err != nil {
		return err
	}
	s.handleEvent(event, data)
	return nil
}

func (s *AnthropicToOpenAIStream) handleEvent(eventType, data string) {
	data = strings.TrimSpace(data)
	if data == "" || data == "[DONE]" {
		return
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		return
	}
	// Prefer explicit event: line; fall back to payload type.
	if eventType == "" {
		if typ, ok := payload["type"].(string); ok {
			eventType = typ
		}
	}
	switch eventType {
	case "message_start":
		s.onMessageStart(payload)
	case "content_block_start":
		s.onContentBlockStart(payload)
	case "content_block_delta":
		s.onContentBlockDelta(payload)
	case "content_block_stop":
		// no-op
	case "message_delta":
		s.onMessageDelta(payload)
	case "message_stop":
		s.emitDone()
		s.done = true
	case "ping", "error":
		// skip ping; errors still surface via HTTP status on non-2xx bodies
	default:
		// Some hosts only send data without event lines; try type inside.
		if typ, ok := payload["type"].(string); ok && typ != eventType {
			s.handleEvent(typ, data)
		}
	}
}

func (s *AnthropicToOpenAIStream) onMessageStart(payload map[string]any) {
	message, _ := payload["message"].(map[string]any)
	if message == nil {
		return
	}
	if id, ok := message["id"].(string); ok {
		s.messageID = id
	}
	if model, ok := message["model"].(string); ok {
		s.model = model
	}
	if s.messageID == "" {
		s.messageID = fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
	}
	// Emit role-only first chunk (OpenAI convention). Anthropic may place the
	// input-token count on message_start and output-token count on message_delta;
	// preserve the former so the usage tee can merge both snapshots.
	var usage map[string]any
	if rawUsage, ok := message["usage"].(map[string]any); ok {
		prompt := intFromAny(rawUsage["input_tokens"])
		if prompt > 0 {
			usage = map[string]any{"prompt_tokens": prompt}
			if cached := intFromAny(rawUsage["cache_read_input_tokens"]); cached > 0 {
				usage["cache_read_tokens"] = cached
			}
			if created := intFromAny(rawUsage["cache_creation_input_tokens"]); created > 0 {
				usage["cache_creation_tokens"] = created
			}
		}
	}
	if !s.roleSent {
		s.roleSent = true
		s.writeChunk(map[string]any{
			"role": "assistant",
		}, nil, usage)
	}
}

// onContentBlockStart registers a tool_use block and opens its tool_calls
// entry in the OpenAI stream. Text blocks need no opener: their deltas carry
// the content directly.
func (s *AnthropicToOpenAIStream) onContentBlockStart(payload map[string]any) {
	block, _ := payload["content_block"].(map[string]any)
	if block == nil {
		return
	}
	if blockType, _ := block["type"].(string); blockType != "tool_use" {
		return
	}
	index := intFromAny(payload["index"])
	toolIndex := s.nextToolIndex
	s.nextToolIndex++
	if s.tools == nil {
		s.tools = map[int]int{}
	}
	s.tools[index] = toolIndex
	id, _ := block["id"].(string)
	name, _ := block["name"].(string)
	s.writeChunk(map[string]any{
		"tool_calls": []any{map[string]any{
			"index": toolIndex,
			"id":    id,
			"type":  "function",
			"function": map[string]any{
				"name":      name,
				"arguments": "",
			},
		}},
	}, nil, nil)
}

// onInputJSONDelta forwards streamed tool arguments as tool_calls fragments
// (the mirror of Anthropic's input_json_delta).
func (s *AnthropicToOpenAIStream) onInputJSONDelta(payload, delta map[string]any) {
	partial, _ := delta["partial_json"].(string)
	if partial == "" {
		return
	}
	toolIndex, ok := s.tools[intFromAny(payload["index"])]
	if !ok {
		return
	}
	s.writeChunk(map[string]any{
		"tool_calls": []any{map[string]any{
			"index":    toolIndex,
			"function": map[string]any{"arguments": partial},
		}},
	}, nil, nil)
}

func (s *AnthropicToOpenAIStream) onContentBlockDelta(payload map[string]any) {
	delta, _ := payload["delta"].(map[string]any)
	if delta == nil {
		return
	}
	deltaType, _ := delta["type"].(string)
	switch deltaType {
	case "input_json_delta":
		s.onInputJSONDelta(payload, delta)
		return
	case "thinking_delta", "signature_delta", "citations_delta":
		// No OpenAI chat equivalent in this reshape.
		return
	}
	if deltaType == "text_delta" || deltaType == "" {
		text, _ := delta["text"].(string)
		if text == "" {
			return
		}
		if !s.roleSent {
			s.roleSent = true
			s.writeChunk(map[string]any{"role": "assistant", "content": text}, nil, nil)
			return
		}
		s.writeChunk(map[string]any{"content": text}, nil, nil)
	}
}

func (s *AnthropicToOpenAIStream) onMessageDelta(payload map[string]any) {
	delta, _ := payload["delta"].(map[string]any)
	var finishReason any
	if delta != nil {
		if stop, ok := delta["stop_reason"].(string); ok && stop != "" {
			finishReason = mapAnthropicStopReason(stop)
		}
	}
	var usage map[string]any
	if rawUsage, ok := payload["usage"].(map[string]any); ok {
		prompt := intFromAny(rawUsage["input_tokens"])
		completion := intFromAny(rawUsage["output_tokens"])
		usage = map[string]any{
			"prompt_tokens":     prompt,
			"completion_tokens": completion,
		}
		if prompt > 0 && completion > 0 {
			usage["total_tokens"] = prompt + completion
		}
		// Pass cache accounting through for downstream usage metering.
		if cached := intFromAny(rawUsage["cache_read_input_tokens"]); cached > 0 {
			usage["cache_read_tokens"] = cached
		}
		if created := intFromAny(rawUsage["cache_creation_input_tokens"]); created > 0 {
			usage["cache_creation_tokens"] = created
		}
	}
	if finishReason != nil || usage != nil {
		s.writeChunk(map[string]any{}, finishReason, usage)
	}
}

func mapAnthropicStopReason(stop string) string {
	switch stop {
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	default:
		return "stop"
	}
}

func intFromAny(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	case int64:
		return int(typed)
	case json.Number:
		n, _ := typed.Int64()
		return int(n)
	default:
		return 0
	}
}

func (s *AnthropicToOpenAIStream) writeChunk(delta map[string]any, finishReason any, usage map[string]any) {
	if s.messageID == "" {
		s.messageID = fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
	}
	chunk := map[string]any{
		"id":      s.messageID,
		"object":  "chat.completion.chunk",
		"created": s.created,
		"model":   s.model,
		"choices": []map[string]any{
			{
				"index":         0,
				"delta":         delta,
				"finish_reason": finishReason,
			},
		},
	}
	if usage != nil {
		chunk["usage"] = usage
	}
	encoded, err := json.Marshal(chunk)
	if err != nil {
		return
	}
	s.pending.WriteString("data: ")
	s.pending.Write(encoded)
	s.pending.WriteString("\n\n")
}

func (s *AnthropicToOpenAIStream) emitDone() {
	s.pending.WriteString("data: [DONE]\n\n")
}

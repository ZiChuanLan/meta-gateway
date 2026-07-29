package adapters

import (
	"bufio"
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
	source io.ReadCloser
	reader *bufio.Reader

	// pending holds already-formatted OpenAI SSE bytes ready to return.
	pending bytes.Buffer

	messageID string
	model     string
	created   int64
	roleSent  bool
	done      bool
	closed    bool
	sourceErr error
}

// NewAnthropicToOpenAIStream wraps an Anthropic SSE body.
func NewAnthropicToOpenAIStream(source io.ReadCloser) *AnthropicToOpenAIStream {
	return &AnthropicToOpenAIStream{
		source:  source,
		reader:  bufio.NewReader(source),
		created: time.Now().Unix(),
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

func (s *AnthropicToOpenAIStream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	if s.source != nil {
		return s.source.Close()
	}
	return nil
}

func (s *AnthropicToOpenAIStream) pullEvent() error {
	var dataLines []string
	var eventType string
	for {
		line, err := s.reader.ReadString('\n')
		if err != nil {
			if err == io.EOF && (len(dataLines) > 0 || eventType != "") {
				s.handleEvent(eventType, strings.Join(dataLines, "\n"))
				return nil
			}
			return err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if len(dataLines) > 0 || eventType != "" {
				s.handleEvent(eventType, strings.Join(dataLines, "\n"))
			}
			return nil
		}
		if strings.HasPrefix(line, "event:") {
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			continue
		}
		// Ignore id:/comment lines.
	}
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
		// no-op for text blocks; tool_use ignored in v1 reshape
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
	// Emit role-only first chunk (OpenAI convention).
	if !s.roleSent {
		s.roleSent = true
		s.writeChunk(map[string]any{
			"role": "assistant",
		}, nil, nil)
	}
}

func (s *AnthropicToOpenAIStream) onContentBlockDelta(payload map[string]any) {
	delta, _ := payload["delta"].(map[string]any)
	if delta == nil {
		return
	}
	deltaType, _ := delta["type"].(string)
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
			"total_tokens":      prompt + completion,
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

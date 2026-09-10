// Responses-API stream reshaping: chat.completion.chunk SSE in, Responses
// SSE events out. The wrapper emits the canonical event sequence OpenAI
// Responses streaming clients (Codex, the OpenAI SDK wire_api=responses)
// expect, including the terminal response.completed that carries usage.
package adapters

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"time"
)

// ChatStreamToResponsesStream converts an OpenAI chat SSE body into the
// OpenAI Responses SSE event contract.
type ChatStreamToResponsesStream struct {
	source io.ReadCloser
	reader *bufio.Reader

	pending bytes.Buffer

	respID       string
	msgID        string
	model        string
	started      int64
	seq          int
	preambleSent bool
	finished     bool
	completed    bool
	closed       bool
	usage        map[string]any
}

// NewChatStreamToResponsesStream wraps an OpenAI chat-completion SSE body.
func NewChatStreamToResponsesStream(source io.ReadCloser) *ChatStreamToResponsesStream {
	return &ChatStreamToResponsesStream{
		source:    source,
		reader:    bufio.NewReader(source),
		respID:    "resp_" + hexString(randomIDBytes(16)),
		msgID:     "msg_" + hexString(randomIDBytes(12)),
		started:   time.Now().Unix(),
		completed: false,
		usage:     map[string]any{},
	}
}

func hexString(buf []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, len(buf)*2)
	for _, b := range buf {
		out = append(out, digits[b>>4], digits[b&0x0f])
	}
	return string(out)
}

func (s *ChatStreamToResponsesStream) Read(p []byte) (int, error) {
	for s.pending.Len() == 0 && !s.completed {
		if err := s.pullEvent(); err != nil {
			if err == io.EOF {
				s.finish()
			} else {
				return 0, err
			}
		}
	}
	if s.pending.Len() > 0 {
		n, _ := s.pending.Read(p)
		return n, nil
	}
	return 0, io.EOF
}

func (s *ChatStreamToResponsesStream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	if s.source != nil {
		return s.source.Close()
	}
	return nil
}

// pullEvent reads one SSE frame from the chat stream.
func (s *ChatStreamToResponsesStream) pullEvent() error {
	var dataLines []string
	for {
		line, err := s.reader.ReadString('\n')
		if err != nil {
			if err == io.EOF && len(dataLines) > 0 {
				s.handleFrame(strings.Join(dataLines, "\n"))
				return nil
			}
			return err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if len(dataLines) > 0 {
				s.handleFrame(strings.Join(dataLines, "\n"))
			}
			return nil
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
		// event:/id:/comments are not used by chat streams.
	}
}

func (s *ChatStreamToResponsesStream) handleFrame(data string) {
	data = strings.TrimSpace(data)
	if data == "" || data == "[DONE]" {
		return
	}
	var frame struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Choices []struct {
			Delta struct {
				Role    *string `json:"role"`
				Content any     `json:"content"`
			} `json:"delta"`
			FinishReason *string `json:"finish_reason"`
		} `json:"choices"`
		Usage json.RawMessage `json:"usage"`
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal([]byte(data), &frame); err != nil {
		return
	}
	if s.model == "" && frame.Model != "" {
		s.model = frame.Model
	}
	if len(frame.Usage) > 0 && string(frame.Usage) != "null" && string(frame.Usage) != "{}" {
		_ = json.Unmarshal(frame.Usage, &s.usage)
	}
	if len(frame.Error) > 0 && string(frame.Error) != "null" {
		var errObj struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		}
		_ = json.Unmarshal(frame.Error, &errObj)
		if errObj.Message == "" {
			errObj.Message = "upstream stream error"
		}
		if errObj.Type == "" {
			errObj.Type = "stream_error"
		}
		s.pending.WriteString("event: response.failed\n")
		s.writeData(map[string]any{"type": "response.failed", "response": map[string]any{
			"id": s.respID, "object": "response", "status": "failed", "model": s.model,
			"error": map[string]any{"code": errObj.Code, "message": errObj.Message, "type": errObj.Type},
		}})
		s.completed = true
		return
	}
	for _, choice := range frame.Choices {
		delta := choice.Delta
		if delta.Role != nil {
			continue // role-only header frame
		}
		var text string
		switch value := delta.Content.(type) {
		case string:
			text = value
		case nil:
			if choice.FinishReason != nil {
				continue
			}
		default:
			if raw, err := json.Marshal(value); err == nil {
				var parts []inputPart
				if json.Unmarshal(raw, &parts) == nil {
					for _, part := range parts {
						if part.Type == "text" {
							text += part.Text
						}
					}
				}
			}
		}
		if text != "" {
			s.sequenceAnnounce()
			s.pending.WriteString("event: response.output_text.delta\n")
			s.writeData(map[string]any{
				"type": "response.output_text.delta", "sequence_number": s.nextSeq(),
				"item_id": s.msgID, "output_index": 0, "content_index": 0,
				"delta": text,
			})
		}
		if choice.FinishReason != nil {
			s.sequenceAnnounce()
		}
	}
}

// sequenceAnnounce emits the stream preamble once per response.
func (s *ChatStreamToResponsesStream) sequenceAnnounce() {
	if s.preambleSent {
		return
	}
	s.preambleSent = true
	s.pending.WriteString("event: response.created\n")
	s.writeData(map[string]any{
		"type": "response.created", "sequence_number": s.nextSeq(),
		"response": map[string]any{
			"id": s.respID, "object": "response", "created_at": s.started,
			"status": "in_progress", "model": s.model, "output": []any{},
		},
	})
	s.pending.WriteString("event: response.in_progress\n")
	s.writeData(map[string]any{
		"type": "response.in_progress", "sequence_number": s.nextSeq(),
		"response": map[string]any{
			"id": s.respID, "object": "response", "created_at": s.started,
			"status": "in_progress", "model": s.model, "output": []any{},
		},
	})
	s.pending.WriteString("event: response.output_item.added\n")
	s.writeData(map[string]any{
		"type": "response.output_item.added", "sequence_number": s.nextSeq(),
		"output_index": 0,
		"item": map[string]any{
			"id": s.msgID, "type": "message", "status": "in_progress",
			"role": "assistant", "content": []any{},
		},
	})
	s.pending.WriteString("event: response.content_part.added\n")
	s.writeData(map[string]any{
		"type": "response.content_part.added", "sequence_number": s.nextSeq(),
		"item_id": s.msgID, "output_index": 0, "content_index": 0,
		"part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}},
	})
}

func (s *ChatStreamToResponsesStream) nextSeq() int {
	s.seq++
	return s.seq
}

// finish emits the terminal events after the last delta.
func (s *ChatStreamToResponsesStream) finish() {
	if s.finished {
		return
	}
	s.finished = true
	s.sequenceAnnounce()
	outText := ""
	s.pending.WriteString("event: response.output_text.done\n")
	s.writeData(map[string]any{
		"type": "response.output_text.done", "sequence_number": s.nextSeq(),
		"item_id": s.msgID, "output_index": 0, "content_index": 0, "text": outText,
	})
	s.pending.WriteString("event: response.content_part.done\n")
	s.writeData(map[string]any{
		"type": "response.content_part.done", "sequence_number": s.nextSeq(),
		"item_id": s.msgID, "output_index": 0, "content_index": 0,
		"part": map[string]any{"type": "output_text", "text": outText, "annotations": []any{}},
	})
	s.pending.WriteString("event: response.output_item.done\n")
	s.writeData(map[string]any{
		"type": "response.output_item.done", "sequence_number": s.nextSeq(),
		"output_index": 0,
		"item": map[string]any{
			"id": s.msgID, "type": "message", "status": "completed",
			"role": "assistant",
			"content": []any{map[string]any{
				"type": "output_text", "text": outText, "annotations": []any{},
			}},
		},
	})
	usage := map[string]any{
		"input_tokens":  0,
		"output_tokens": 0,
		"total_tokens":  0,
	}
	if len(s.usage) > 0 {
		usage = s.usage
		if _, ok := usage["input_tokens"]; !ok {
			if prompt, ok := usage["prompt_tokens"]; ok {
				usage["input_tokens"] = prompt
			}
		}
		if _, ok := usage["output_tokens"]; !ok {
			if completion, ok := usage["completion_tokens"]; ok {
				usage["output_tokens"] = completion
			}
		}
		if _, ok := usage["total_tokens"]; !ok {
			total := intFromAny(usage["input_tokens"]) + intFromAny(usage["output_tokens"])
			if total > 0 {
				usage["total_tokens"] = total
			}
		}
	}
	s.pending.WriteString("event: response.completed\n")
	s.writeData(map[string]any{
		"type": "response.completed", "sequence_number": s.nextSeq(),
		"response": map[string]any{
			"id": s.respID, "object": "response", "created_at": s.started,
			"status": "completed", "model": s.model,
			"output": []any{map[string]any{
				"id": s.msgID, "type": "message", "status": "completed",
				"role": "assistant",
				"content": []any{map[string]any{
					"type": "output_text", "text": outText, "annotations": []any{},
				}},
			}},
			"usage": usage,
		},
	})
	s.completed = true
}

func (s *ChatStreamToResponsesStream) writeData(payload map[string]any) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return
	}
	s.pending.WriteString("data: ")
	s.pending.Write(encoded)
	s.pending.WriteString("\n\n")
}

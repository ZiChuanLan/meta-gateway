// Responses-API stream reshaping: chat.completion.chunk SSE in, Responses
// SSE events out. The wrapper emits the canonical event sequence OpenAI
// Responses streaming clients (Codex, the OpenAI SDK wire_api=responses)
// expect, including the terminal response.completed that carries the whole
// answer and its usage.
//
// Two contracts matter to those clients and are easy to get wrong:
//   - the terminal events are authoritative. A client renders the deltas for
//     responsiveness, then replaces the item with what output_item.done /
//     response.completed say. Terminal events that carry no text therefore
//     blank out the message the user just watched stream in.
//   - function calls are output items too. Dropping delta.tool_calls leaves an
//     agentic client with an empty answer and no call to execute.
package adapters

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"sort"
	"strings"
	"time"
)

// streamedToolCall is one function call being streamed, assembled from the
// chat deltas that reference the same tool-call index.
type streamedToolCall struct {
	outputIndex int
	itemID      string
	callID      string
	name        string
	args        strings.Builder
	announced   bool
}

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

	// outputText accumulates every streamed text delta. The terminal events
	// (output_text.done / content_part.done / output_item.done and the
	// response.completed document) are authoritative for Responses clients:
	// they replace what was rendered during the stream, so an empty text there
	// blanks the answer the user just watched arrive.
	outputText strings.Builder

	// Output items are numbered in the order they first appear on the stream:
	// the assistant message (allocated lazily, only once text shows up) and one
	// function_call item per tool-call index. msgIndex is -1 until allocated.
	msgIndex        int
	msgAnnounced    bool
	nextOutputIndex int
	toolCalls       []*streamedToolCall
	toolCallByIndex map[int]*streamedToolCall
}

// NewChatStreamToResponsesStream wraps an OpenAI chat-completion SSE body.
func NewChatStreamToResponsesStream(source io.ReadCloser) *ChatStreamToResponsesStream {
	return &ChatStreamToResponsesStream{
		source:          source,
		reader:          bufio.NewReader(source),
		respID:          "resp_" + hexString(randomIDBytes(16)),
		msgID:           "msg_" + hexString(randomIDBytes(12)),
		started:         time.Now().Unix(),
		completed:       false,
		usage:           map[string]any{},
		msgIndex:        -1,
		toolCallByIndex: map[int]*streamedToolCall{},
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

type chatStreamToolCall struct {
	Index    *int   `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function *struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
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
				Role      *string              `json:"role"`
				Content   any                  `json:"content"`
				ToolCalls []chatStreamToolCall `json:"tool_calls"`
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
		for _, call := range delta.ToolCalls {
			s.appendToolCall(call)
		}
		var text string
		switch value := delta.Content.(type) {
		case string:
			text = value
		case nil:
			// finish_reason-only frames carry no content.
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
			s.announceMessage()
			s.outputText.WriteString(text)
			s.pending.WriteString("event: response.output_text.delta\n")
			s.writeData(map[string]any{
				"type": "response.output_text.delta", "sequence_number": s.nextSeq(),
				"item_id": s.msgID, "output_index": s.msgIndex, "content_index": 0,
				"delta": text,
			})
		}
		if choice.FinishReason != nil {
			s.preamble()
		}
	}
}

// appendToolCall folds one chat tool-call delta into its assembled item,
// announcing the item on first sight and streaming argument fragments as they
// arrive (upstreams split a call across many frames).
func (s *ChatStreamToResponsesStream) appendToolCall(call chatStreamToolCall) {
	index := 0
	if call.Index != nil {
		index = *call.Index
	}
	tc := s.toolCallByIndex[index]
	if tc == nil {
		tc = &streamedToolCall{itemID: "fc_" + hexString(randomIDBytes(12))}
		s.toolCallByIndex[index] = tc
		s.toolCalls = append(s.toolCalls, tc)
	}
	if call.ID != "" {
		tc.callID = call.ID
	}
	if call.Function != nil && call.Function.Name != "" {
		tc.name = call.Function.Name
	}
	if tc.callID == "" {
		tc.callID = "call_" + hexString(randomIDBytes(12))
	}
	if !tc.announced {
		tc.announced = true
		tc.outputIndex = s.allocOutputIndex()
		s.preamble()
		s.pending.WriteString("event: response.output_item.added\n")
		s.writeData(map[string]any{
			"type": "response.output_item.added", "sequence_number": s.nextSeq(),
			"output_index": tc.outputIndex,
			"item": map[string]any{
				"id": tc.itemID, "type": "function_call", "status": "in_progress",
				"call_id": tc.callID, "name": tc.name, "arguments": "",
			},
		})
	}
	if call.Function == nil || call.Function.Arguments == "" {
		return
	}
	tc.args.WriteString(call.Function.Arguments)
	s.pending.WriteString("event: response.function_call_arguments.delta\n")
	s.writeData(map[string]any{
		"type": "response.function_call_arguments.delta", "sequence_number": s.nextSeq(),
		"item_id": tc.itemID, "output_index": tc.outputIndex,
		"delta": call.Function.Arguments,
	})
}

func (s *ChatStreamToResponsesStream) allocOutputIndex() int {
	index := s.nextOutputIndex
	s.nextOutputIndex++
	return index
}

// preamble emits response.created / response.in_progress once per response.
func (s *ChatStreamToResponsesStream) preamble() {
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
}

// announceMessage emits the assistant message item the text deltas belong to.
// It is allocated lazily so a tool-call-only answer does not grow a phantom
// empty message (the client would render an empty bubble for it).
func (s *ChatStreamToResponsesStream) announceMessage() {
	if s.msgAnnounced {
		return
	}
	s.msgAnnounced = true
	s.msgIndex = s.allocOutputIndex()
	s.preamble()
	s.pending.WriteString("event: response.output_item.added\n")
	s.writeData(map[string]any{
		"type": "response.output_item.added", "sequence_number": s.nextSeq(),
		"output_index": s.msgIndex,
		"item": map[string]any{
			"id": s.msgID, "type": "message", "status": "in_progress",
			"role": "assistant", "content": []any{},
		},
	})
	s.pending.WriteString("event: response.content_part.added\n")
	s.writeData(map[string]any{
		"type": "response.content_part.added", "sequence_number": s.nextSeq(),
		"item_id": s.msgID, "output_index": s.msgIndex, "content_index": 0,
		"part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}},
	})
}

func (s *ChatStreamToResponsesStream) nextSeq() int {
	s.seq++
	return s.seq
}

// finish emits the terminal events after the last delta: the done events for
// every output item, followed by response.completed carrying the assembled
// output array and usage.
func (s *ChatStreamToResponsesStream) finish() {
	if s.finished {
		return
	}
	s.finished = true
	s.preamble()
	if !s.msgAnnounced && len(s.toolCalls) == 0 {
		// Nothing streamed at all: still emit the message shape so the
		// document carries an output item (ChatToResponses does the same).
		s.announceMessage()
	}
	outText := s.outputText.String()

	type outputItem struct {
		index int
		item  map[string]any
	}
	items := make([]outputItem, 0, len(s.toolCalls)+1)
	if s.msgAnnounced {
		s.pending.WriteString("event: response.output_text.done\n")
		s.writeData(map[string]any{
			"type": "response.output_text.done", "sequence_number": s.nextSeq(),
			"item_id": s.msgID, "output_index": s.msgIndex, "content_index": 0, "text": outText,
		})
		s.pending.WriteString("event: response.content_part.done\n")
		s.writeData(map[string]any{
			"type": "response.content_part.done", "sequence_number": s.nextSeq(),
			"item_id": s.msgID, "output_index": s.msgIndex, "content_index": 0,
			"part": map[string]any{"type": "output_text", "text": outText, "annotations": []any{}},
		})
		items = append(items, outputItem{index: s.msgIndex, item: s.messageItem(outText, "completed")})
	}
	for _, tc := range s.toolCalls {
		arguments := tc.args.String()
		s.pending.WriteString("event: response.function_call_arguments.done\n")
		s.writeData(map[string]any{
			"type": "response.function_call_arguments.done", "sequence_number": s.nextSeq(),
			"item_id": tc.itemID, "output_index": tc.outputIndex, "arguments": arguments,
		})
		items = append(items, outputItem{index: tc.outputIndex, item: s.functionCallItem(tc, arguments, "completed")})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].index < items[j].index })
	output := make([]any, 0, len(items))
	for _, entry := range items {
		s.pending.WriteString("event: response.output_item.done\n")
		s.writeData(map[string]any{
			"type": "response.output_item.done", "sequence_number": s.nextSeq(),
			"output_index": entry.index, "item": entry.item,
		})
		output = append(output, entry.item)
	}

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
			"output": output,
			"usage":  usage,
		},
	})
	s.completed = true
}

func (s *ChatStreamToResponsesStream) messageItem(text, status string) map[string]any {
	return map[string]any{
		"id": s.msgID, "type": "message", "status": status,
		"role": "assistant",
		"content": []any{map[string]any{
			"type": "output_text", "text": text, "annotations": []any{},
		}},
	}
}

func (s *ChatStreamToResponsesStream) functionCallItem(tc *streamedToolCall, arguments, status string) map[string]any {
	return map[string]any{
		"id": tc.itemID, "type": "function_call", "status": status,
		"call_id": tc.callID, "name": tc.name, "arguments": arguments,
	}
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

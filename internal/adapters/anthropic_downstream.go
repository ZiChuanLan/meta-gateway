// Downstream Anthropic protocol support: lets /v1/messages (native Anthropic
// Messages API clients, e.g. Claude Code) be served by any channel. The
// gateway translates Anthropic requests into the internal OpenAI contract,
// routes normally, then converts responses/streams back to Anthropic format.
// Anthropic-native channels keep their verbatim passthrough path.
package adapters

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// ---- request: Anthropic Messages → OpenAI chat/completions ----

type anthropicMessagePart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type anthropicRequestMessage struct {
	Role    string                 `json:"role"`
	Content json.RawMessage        `json:"content"`
}

// MessagesToOpenAIChat converts a native Anthropic Messages request body into
// an OpenAI chat/completions body (system extracted, stream flag preserved).
func MessagesToOpenAIChat(body []byte) ([]byte, error) {
	var incoming struct {
		Model       string                   `json:"model"`
		MaxTokens   *int                     `json:"max_tokens"`
		Temperature *float64                 `json:"temperature"`
		TopP        *float64                 `json:"top_p"`
		Stream      bool                     `json:"stream"`
		Stop        json.RawMessage          `json:"stop_sequences"`
		System      json.RawMessage          `json:"system"`
		Messages    []anthropicRequestMessage `json:"messages"`
	}
	if err := json.Unmarshal(body, &incoming); err != nil {
		return nil, fmt.Errorf("anthropic: decode messages body: %w", err)
	}
	if strings.TrimSpace(incoming.Model) == "" {
		return nil, errorsNew("anthropic: model is required")
	}

	systemText := ""
	if len(incoming.System) > 0 && string(incoming.System) != "null" {
		var text string
		if err := json.Unmarshal(incoming.System, &text); err == nil {
			systemText = strings.TrimSpace(text)
		} else {
			// Array form: [{"type":"text","text":"..."}]
			var parts []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			if err := json.Unmarshal(incoming.System, &parts); err == nil {
				var builder strings.Builder
				for _, part := range parts {
					builder.WriteString(part.Text)
				}
				systemText = strings.TrimSpace(builder.String())
			}
		}
	}

	messages := make([]map[string]any, 0, len(incoming.Messages)+1)
	if systemText != "" {
		messages = append(messages, map[string]any{"role": "system", "content": systemText})
	}
	for _, message := range incoming.Messages {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		if role == "" {
			continue
		}
		// Anthropic roles: user/assistant; map tool messages to user content.
		content := anthropicPartsToOpenAIContent(message.Content)
		messages = append(messages, map[string]any{"role": role, "content": content})
	}

	outbound := map[string]any{
		"model":    incoming.Model,
		"messages": messages,
		"stream":   incoming.Stream,
	}
	if incoming.MaxTokens != nil && *incoming.MaxTokens > 0 {
		outbound["max_tokens"] = *incoming.MaxTokens
	}
	if incoming.Temperature != nil {
		outbound["temperature"] = *incoming.Temperature
	}
	if incoming.TopP != nil {
		outbound["top_p"] = *incoming.TopP
	}
	if len(incoming.Stop) > 0 && string(incoming.Stop) != "null" {
		var stops []string
		if err := json.Unmarshal(incoming.Stop, &stops); err == nil && len(stops) > 0 {
			outbound["stop"] = stops
		}
	}
	// Ask the upstream for usage in the final stream chunk so conversion has it.
	if incoming.Stream {
		outbound["stream_options"] = map[string]any{"include_usage": true}
	}
	return json.Marshal(outbound)
}

// anthropicPartsToOpenAIContent flattens Anthropic message content (string or
// part array) into OpenAI string content (text parts only).
func anthropicPartsToOpenAIContent(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	var parts []anthropicMessagePart
	if err := json.Unmarshal(raw, &parts); err == nil {
		var builder strings.Builder
		for _, part := range parts {
			if part.Type == "text" || part.Type == "" {
				builder.WriteString(part.Text)
			}
		}
		return builder.String()
	}
	return ""
}

func errorsNew(text string) error { return fmt.Errorf("%s", text) }

// ---- response: OpenAI chat/completions → Anthropic Messages ----

// OpenAIChatToMessages converts an OpenAI chat.completion body into an
// Anthropic Messages response (content block + usage mapping).
func OpenAIChatToMessages(openaiBody []byte) ([]byte, error) {
	var incoming struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(openaiBody, &incoming); err != nil {
		return nil, fmt.Errorf("anthropic: decode chat response: %w", err)
	}
	content := ""
	stopReason := "end_turn"
	if len(incoming.Choices) > 0 {
		content = incoming.Choices[0].Message.Content
		stopReason = mapOpenAIStopReason(incoming.Choices[0].FinishReason)
	}
	outbound := map[string]any{
		"id":         "msg_" + strings.TrimPrefix(incoming.ID, "chatcmpl-"),
		"type":       "message",
		"role":       "assistant",
		"model":      incoming.Model,
		"content":    []map[string]any{{"type": "text", "text": content}},
		"stop_reason": stopReason,
		"stop_sequence": nil,
		"usage": map[string]any{
			"input_tokens":  incoming.Usage.PromptTokens,
			"output_tokens": incoming.Usage.CompletionTokens,
		},
	}
	return json.Marshal(outbound)
}

func mapOpenAIStopReason(reason string) string {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "stop":
		return "end_turn"
	case "length":
		return "max_tokens"
	case "content_filter":
		return "refusal"
	case "tool_calls":
		return "tool_use"
	default:
		return "end_turn"
	}
}

// ---- stream: OpenAI SSE → Anthropic Messages SSE ----

// OpenAIStreamToAnthropicStream converts OpenAI chat.completion.chunk SSE into
// Anthropic Messages event-stream SSE so native clients (Claude Code etc.) can
// stream through any channel.
type OpenAIStreamToAnthropicStream struct {
	source io.ReadCloser
	reader *bufio.Reader

	pending bytes.Buffer

	messageID  string
	model      string
	created    int64
	contentIdx int
	done       bool
	closed     bool
	sourceErr  error
}

func NewOpenAIStreamToAnthropicStream(source io.ReadCloser) *OpenAIStreamToAnthropicStream {
	return &OpenAIStreamToAnthropicStream{
		source:  source,
		reader:  bufio.NewReader(source),
		model:   "claude",
		created: nowUnix(),
	}
}

func (s *OpenAIStreamToAnthropicStream) Read(p []byte) (int, error) {
	if s.closed {
		return 0, io.EOF
	}
	if s.done && s.pending.Len() == 0 {
		return 0, io.EOF
	}
	if s.pending.Len() > 0 {
		n, _ := s.pending.Read(p)
		if s.pending.Len() == 0 {
			s.pending.Reset()
		}
		return n, nil
	}
	for s.pending.Len() == 0 && !s.done {
		if err := s.pullEvent(); err != nil {
			if err == io.EOF {
				s.writeStop()
				break
			}
			s.sourceErr = err
			s.done = true
			return 0, err
		}
	}
	if s.pending.Len() == 0 && s.done {
		return 0, io.EOF
	}
	n, _ := s.pending.Read(p)
	if s.pending.Len() == 0 {
		s.pending.Reset()
	}
	return n, nil
}

func (s *OpenAIStreamToAnthropicStream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	if s.source != nil {
		return s.source.Close()
	}
	return nil
}

func (s *OpenAIStreamToAnthropicStream) pullEvent() error {
	line, err := s.reader.ReadString('\n')
	if err != nil && line == "" {
		return err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	if strings.HasPrefix(line, "data:") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	}
	if line == "[DONE]" {
		s.writeStop()
		return nil
	}
	var payload struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Choices []struct {
			Delta struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"delta"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal([]byte(line), &payload); err != nil {
		return nil // skip non-JSON lines
	}
	if payload.ID != "" && s.messageID == "" {
		s.messageID = payload.ID
	}
	if payload.Model != "" {
		s.model = payload.Model
	}
	first := len(payload.Choices) > 0 && payload.Choices[0].Delta.Role == "assistant"
	text := ""
	if len(payload.Choices) > 0 {
		text = payload.Choices[0].Delta.Content
	}
	if first {
		s.writeMessageStart()
	}
	if text != "" {
		s.writeContentDelta(text)
	}
	finishReason := ""
	if len(payload.Choices) > 0 {
		finishReason = payload.Choices[0].FinishReason
	}
	if finishReason != "" || payload.Usage.PromptTokens > 0 || payload.Usage.CompletionTokens > 0 {
		s.writeMessageDelta(finishReason, payload.Usage.PromptTokens, payload.Usage.CompletionTokens)
	}
	return nil
}

func (s *OpenAIStreamToAnthropicStream) writeMessageStart() {
	s.messageID = "msg_" + strings.TrimPrefix(s.messageID, "chatcmpl-")
	if s.messageID == "msg_" {
		s.messageID = fmt.Sprintf("msg_%d", s.created)
	}
	s.writeEvent("message_start", map[string]any{
		"message": map[string]any{
			"id":      s.messageID,
			"type":    "message",
			"role":    "assistant",
			"model":   s.model,
			"content": []any{},
		},
	})
}

func (s *OpenAIStreamToAnthropicStream) writeContentDelta(text string) {
	s.writeEvent("content_block_delta", map[string]any{
		"index": s.contentIdx,
		"delta": map[string]any{"type": "text_delta", "text": text},
	})
}

func (s *OpenAIStreamToAnthropicStream) writeMessageDelta(finishReason string, promptTokens, completionTokens int) {
	delta := map[string]any{}
	if finishReason != "" {
		delta["stop_reason"] = mapOpenAIStopReason(finishReason)
	}
	usage := map[string]any{}
	if promptTokens > 0 {
		usage["input_tokens"] = promptTokens
	}
	if completionTokens > 0 {
		usage["output_tokens"] = completionTokens
	}
	event := map[string]any{}
	if len(delta) > 0 {
		event["delta"] = delta
	}
	if len(usage) > 0 {
		event["usage"] = usage
	}
	if len(event) > 0 {
		s.writeEvent("message_delta", event)
	}
}

func (s *OpenAIStreamToAnthropicStream) writeStop() {
	if s.done {
		return
	}
	s.done = true
	s.writeEvent("message_stop", map[string]any{})
}

func (s *OpenAIStreamToAnthropicStream) writeEvent(eventType string, payload map[string]any) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		s.sourceErr = fmt.Errorf("anthropic stream: encode: %w", err)
		s.done = true
		return
	}
	s.pending.WriteString("event: ")
	s.pending.WriteString(eventType)
	s.pending.WriteString("\n")
	s.pending.WriteString("data: ")
	s.pending.Write(encoded)
	s.pending.WriteString("\n\n")
}

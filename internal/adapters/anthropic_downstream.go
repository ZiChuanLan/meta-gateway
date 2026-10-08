// Downstream Anthropic protocol support: lets /v1/messages (native Anthropic
// Messages API clients, e.g. Claude Code) be served by any channel. The
// gateway translates Anthropic requests into the internal OpenAI contract,
// routes normally, then converts responses/streams back to Anthropic format.
// Anthropic-native channels keep their verbatim passthrough path.
package adapters

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// ---- request: Anthropic Messages → OpenAI chat/completions ----

type anthropicRequestMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// anthropicContentBlock is one entry of a message's content array. Anthropic
// reuses the array shape for text, tool_use and tool_result blocks, so the
// struct carries the union of the fields those blocks use: flattening to text
// only (as this used to) loses whole tool turns.
type anthropicContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

// anthropicTool is one Anthropic tool declaration (input_schema → parameters).
type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// MessagesToOpenAIChat converts a native Anthropic Messages request body into
// an OpenAI chat/completions body (system extracted, stream flag preserved).
func MessagesToOpenAIChat(body []byte) ([]byte, error) {
	var incoming struct {
		Model       string                    `json:"model"`
		MaxTokens   *int                      `json:"max_tokens"`
		Temperature *float64                  `json:"temperature"`
		TopP        *float64                  `json:"top_p"`
		Stream      bool                      `json:"stream"`
		Stop        json.RawMessage           `json:"stop_sequences"`
		System      json.RawMessage           `json:"system"`
		Messages    []anthropicRequestMessage `json:"messages"`
		Tools       []anthropicTool           `json:"tools"`
		ToolChoice  json.RawMessage           `json:"tool_choice"`
	}
	if err := json.Unmarshal(body, &incoming); err != nil {
		return nil, fmt.Errorf("anthropic: decode messages body: %w", err)
	}
	if strings.TrimSpace(incoming.Model) == "" {
		return nil, errorsNew("anthropic: model is required")
	}

	systemText := anthropicSystemText(incoming.System)

	messages := make([]map[string]any, 0, len(incoming.Messages)+1)
	if systemText != "" {
		messages = append(messages, map[string]any{"role": "system", "content": systemText})
	}
	for _, message := range incoming.Messages {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		if role == "" {
			continue
		}
		switch role {
		case "assistant":
			// One Anthropic assistant turn carries text and tool_use blocks;
			// OpenAI splits those into content and tool_calls.
			text, calls := anthropicAssistantBlocks(message.Content)
			entry := map[string]any{"role": "assistant"}
			if text != "" {
				entry["content"] = text
			} else {
				// content:null is what OpenAI itself returns for a tool-only turn.
				entry["content"] = nil
			}
			if len(calls) > 0 {
				entry["tool_calls"] = calls
			}
			messages = append(messages, entry)
		case "user":
			// tool_result blocks become the role:tool messages that pair with
			// the assistant's tool_calls; the rest of the turn stays user text.
			toolMessages, text := anthropicUserBlocks(message.Content)
			messages = append(messages, toolMessages...)
			if text != "" || len(toolMessages) == 0 {
				messages = append(messages, map[string]any{"role": "user", "content": text})
			}
		default:
			// Unknown roles carry text only; OpenAI chat has no equivalent.
			messages = append(messages, map[string]any{"role": role, "content": anthropicPartsToOpenAIContent(message.Content)})
		}
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
	// Tool declarations travel as OpenAI function tools: without them the
	// upstream cannot call a tool at all, and an agentic client (Claude Code)
	// then answers the user with prose instead of doing the work.
	if tools := anthropicToolsToOpenAI(incoming.Tools); len(tools) > 0 {
		outbound["tools"] = tools
		if choice := anthropicToolChoiceToOpenAI(incoming.ToolChoice); choice != nil {
			outbound["tool_choice"] = choice
		}
	}
	// Ask the upstream for usage in the final stream chunk so conversion has it.
	if incoming.Stream {
		outbound["stream_options"] = map[string]any{"include_usage": true}
	}
	return json.Marshal(outbound)
}

// anthropicPartsToOpenAIContent flattens Anthropic content (string or block
// array) into OpenAI string content (text blocks only).
func anthropicPartsToOpenAIContent(raw json.RawMessage) string {
	var builder strings.Builder
	for _, block := range anthropicBlocks(raw) {
		if block.Type == "text" || block.Type == "" {
			builder.WriteString(block.Text)
		}
	}
	return builder.String()
}

// anthropicBlocks decodes a message's content into blocks. A plain string (or
// null) becomes a single text block so callers only handle one shape.
func anthropicBlocks(raw json.RawMessage) []anthropicContentBlock {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		if text == "" {
			return nil
		}
		return []anthropicContentBlock{{Type: "text", Text: text}}
	}
	var blocks []anthropicContentBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		return blocks
	}
	return nil
}

// anthropicSystemText flattens the system field (string or text-block array).
func anthropicSystemText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.TrimSpace(text)
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err == nil {
		var builder strings.Builder
		for _, part := range parts {
			builder.WriteString(part.Text)
		}
		return strings.TrimSpace(builder.String())
	}
	return ""
}

// anthropicAssistantBlocks splits an assistant turn into its text and its
// tool_use calls: OpenAI keeps those in different fields, Anthropic in one
// block array.
func anthropicAssistantBlocks(raw json.RawMessage) (string, []map[string]any) {
	var builder strings.Builder
	var calls []map[string]any
	for _, block := range anthropicBlocks(raw) {
		switch block.Type {
		case "text", "":
			builder.WriteString(block.Text)
		case "tool_use":
			id := strings.TrimSpace(block.ID)
			if id == "" {
				continue
			}
			calls = append(calls, map[string]any{
				"id": id, "type": "function",
				"function": map[string]any{
					"name":      block.Name,
					"arguments": jsonObjectOrEmpty(block.Input),
				},
			})
		}
	}
	return builder.String(), calls
}

// anthropicUserBlocks turns tool_result blocks into OpenAI tool messages (the
// only shape an OpenAI upstream pairs with tool_calls) and returns the user's
// own text separately.
func anthropicUserBlocks(raw json.RawMessage) ([]map[string]any, string) {
	var tools []map[string]any
	var builder strings.Builder
	for _, block := range anthropicBlocks(raw) {
		switch block.Type {
		case "text", "":
			builder.WriteString(block.Text)
		case "tool_result":
			id := strings.TrimSpace(block.ToolUseID)
			if id == "" {
				continue
			}
			tools = append(tools, map[string]any{
				"role":         "tool",
				"tool_call_id": id,
				"content":      anthropicToolResultText(block.Content),
			})
		}
	}
	return tools, builder.String()
}

// anthropicToolResultText flattens a tool_result payload (string or block
// array). Non-text results (images) have no OpenAI chat equivalent, so the raw
// JSON is handed through rather than silently dropped.
func anthropicToolResultText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	var blocks []anthropicContentBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var builder strings.Builder
		for _, block := range blocks {
			if block.Type == "text" || block.Type == "" {
				builder.WriteString(block.Text)
			}
		}
		if builder.Len() > 0 {
			return builder.String()
		}
	}
	return strings.TrimSpace(string(raw))
}

// jsonObjectOrEmpty returns raw as-is when it is a JSON object, else "{}" —
// OpenAI tool arguments are a JSON string holding an object.
func jsonObjectOrEmpty(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, "{") {
		return trimmed
	}
	return "{}"
}

// anthropicToolsToOpenAI maps Anthropic tool declarations onto OpenAI function
// tools (input_schema → parameters).
func anthropicToolsToOpenAI(tools []anthropicTool) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		name := strings.TrimSpace(tool.Name)
		if name == "" {
			continue
		}
		function := map[string]any{"name": name}
		if strings.TrimSpace(tool.Description) != "" {
			function["description"] = tool.Description
		}
		if schema := strings.TrimSpace(string(tool.InputSchema)); strings.HasPrefix(schema, "{") {
			function["parameters"] = json.RawMessage(schema)
		} else {
			function["parameters"] = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		out = append(out, map[string]any{"type": "function", "function": function})
	}
	return out
}

// anthropicToolChoiceToOpenAI maps Anthropic tool_choice onto the OpenAI enum
// (auto / required / none / {type:function}).
func anthropicToolChoiceToOpenAI(raw json.RawMessage) any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var choice struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &choice); err != nil {
		return nil
	}
	switch choice.Type {
	case "auto":
		return "auto"
	case "any":
		return "required"
	case "none":
		return "none"
	case "tool":
		if strings.TrimSpace(choice.Name) != "" {
			return map[string]any{"type": "function", "function": map[string]any{"name": choice.Name}}
		}
		return "required"
	default:
		return nil
	}
}

func errorsNew(text string) error { return fmt.Errorf("%s", text) }

// ---- response: OpenAI chat/completions → Anthropic Messages ----

// OpenAIChatToMessages converts an OpenAI chat.completion body into an
// Anthropic Messages response (content blocks + usage mapping). Tool calls
// become tool_use blocks — dropping them (as this used to) leaves an agentic
// client with a tool_use stop_reason and nothing to execute.
func OpenAIChatToMessages(openaiBody []byte) ([]byte, error) {
	var incoming struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content   json.RawMessage `json:"content"`
				ToolCalls []chatConvCall  `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens        int `json:"prompt_tokens"`
			CompletionTokens    int `json:"completion_tokens"`
			CacheReadTokens     int `json:"cache_read_tokens"`
			CacheCreationTokens int `json:"cache_creation_tokens"`
			PromptTokensDetails struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(openaiBody, &incoming); err != nil {
		return nil, fmt.Errorf("anthropic: decode chat response: %w", err)
	}
	text := ""
	finishReason := ""
	var toolCalls []chatConvCall
	if len(incoming.Choices) > 0 {
		text = chatDeltaTextOrParts(incoming.Choices[0].Message.Content)
		toolCalls = incoming.Choices[0].Message.ToolCalls
		finishReason = incoming.Choices[0].FinishReason
	}
	blocks := make([]map[string]any, 0, len(toolCalls)+1)
	if text != "" || len(toolCalls) == 0 {
		blocks = append(blocks, map[string]any{"type": "text", "text": text})
	}
	blocks = append(blocks, anthropicToolUseBlocks(toolCalls)...)
	stopReason := mapOpenAIStopReason(finishReason)
	if len(toolCalls) > 0 {
		// A turn that carries tool_use blocks must say so: the client decides
		// whether to run the tools from stop_reason.
		stopReason = "tool_use"
	}
	usage := map[string]any{
		"input_tokens":  incoming.Usage.PromptTokens,
		"output_tokens": incoming.Usage.CompletionTokens,
	}
	if cacheRead := incoming.Usage.CacheReadTokens + incoming.Usage.PromptTokensDetails.CachedTokens; cacheRead > 0 {
		usage["cache_read_input_tokens"] = cacheRead
	}
	if incoming.Usage.CacheCreationTokens > 0 {
		usage["cache_creation_input_tokens"] = incoming.Usage.CacheCreationTokens
	}
	outbound := map[string]any{
		"id":            "msg_" + strings.TrimPrefix(incoming.ID, "chatcmpl-"),
		"type":          "message",
		"role":          "assistant",
		"model":         incoming.Model,
		"content":       blocks,
		"stop_reason":   stopReason,
		"stop_sequence": nil,
		"usage":         usage,
	}
	return json.Marshal(outbound)
}

// chatDeltaTextOrParts reads a chat message content field, which may be a
// plain string or a part array (some upstreams return the array form).
func chatDeltaTextOrParts(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return chatDeltaText(value)
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

// anthropicStreamBlock is one Anthropic content block as it is written out.
// Anthropic streams blocks strictly one at a time: content_block_start opens
// it, deltas fill it, content_block_stop closes it before the next one.
type anthropicStreamBlock struct {
	index   int
	kind    string // "text" or "tool_use"
	toolIdx int    // upstream tool-call index when kind == "tool_use", else -1
	stopped bool
	id      string
	name    string
}

// OpenAIStreamToAnthropicStream converts OpenAI chat.completion.chunk SSE into
// Anthropic Messages event-stream SSE so native clients (Claude Code etc.) can
// stream through any channel.
//
// The event sequence is the one every Anthropic client expects — and the one
// the official SDK enforces (MessageStream.js): an event before message_start
// throws "Unexpected event order", and a content_block_delta whose index has no
// content_block_start is silently dropped, so the assembled message ends up
// with no content at all. Both events therefore get emitted no matter what the
// upstream stream looks like.
type OpenAIStreamToAnthropicStream struct {
	streamSource
	frames sseFrameReader

	pending bytes.Buffer

	messageID string
	model     string
	created   int64
	done      bool
	sourceErr error

	started   bool                          // message_start already written
	nextIndex int                           // next content block index
	open      *anthropicStreamBlock         // the block currently being filled
	text      *anthropicStreamBlock         // the text block (created on demand)
	tools     map[int]*anthropicStreamBlock // upstream tool-call index → block

	// Usage and finish_reason are collected as they arrive and emitted once, in
	// the single message_delta the protocol expects (Anthropic sends exactly one).
	stopReason       string
	stopSet          bool
	promptTokens     int
	completionTokens int
	cacheRead        int
	cacheCreation    int
}

func NewOpenAIStreamToAnthropicStream(source io.ReadCloser) *OpenAIStreamToAnthropicStream {
	return &OpenAIStreamToAnthropicStream{
		streamSource: newStreamSource(source),
		frames:       newSSEFrameReader(source, false),
		model:        "claude",
		created:      nowUnix(),
		tools:        map[int]*anthropicStreamBlock{},
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
				s.finish()
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

func (s *OpenAIStreamToAnthropicStream) pullEvent() error {
	_, data, err := s.frames.next()
	if err != nil {
		return err
	}
	s.handleFrame(data)
	return nil
}

func (s *OpenAIStreamToAnthropicStream) handleFrame(data string) {
	data = strings.TrimSpace(data)
	if data == "" {
		return
	}
	if data == "[DONE]" {
		s.finish()
		return
	}
	var payload struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Choices []struct {
			Delta struct {
				Role      string               `json:"role"`
				Content   any                  `json:"content"`
				ToolCalls []chatStreamToolCall `json:"tool_calls"`
			} `json:"delta"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens        int `json:"prompt_tokens"`
			CompletionTokens    int `json:"completion_tokens"`
			CacheReadTokens     int `json:"cache_read_tokens"`
			CacheCreationTokens int `json:"cache_creation_tokens"`
			PromptTokensDetails struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		return // skip non-JSON lines
	}
	if payload.ID != "" && s.messageID == "" {
		s.messageID = payload.ID
	}
	if payload.Model != "" {
		s.model = payload.Model
	}
	s.ensureStarted()
	for _, choice := range payload.Choices {
		for _, call := range choice.Delta.ToolCalls {
			s.writeToolCall(call)
		}
		if text := chatDeltaText(choice.Delta.Content); text != "" {
			s.writeTextDelta(text)
		}
		if choice.FinishReason != "" {
			s.stopReason = choice.FinishReason
			s.stopSet = true
		}
	}
	if payload.Usage.PromptTokens > 0 || payload.Usage.CompletionTokens > 0 || payload.Usage.CacheReadTokens > 0 || payload.Usage.CacheCreationTokens > 0 {
		s.promptTokens = payload.Usage.PromptTokens
		s.completionTokens = payload.Usage.CompletionTokens
		s.cacheRead = payload.Usage.CacheReadTokens + payload.Usage.PromptTokensDetails.CachedTokens
		s.cacheCreation = payload.Usage.CacheCreationTokens
	}
}

// chatDeltaText flattens a chat delta's content, which a few upstreams send as
// a part array instead of a plain string.
func chatDeltaText(raw any) string {
	switch typed := raw.(type) {
	case string:
		return typed
	case []any:
		var builder strings.Builder
		for _, entry := range typed {
			part, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			if text, ok := part["text"].(string); ok {
				builder.WriteString(text)
			}
		}
		return builder.String()
	default:
		return ""
	}
}

// ensureStarted writes message_start before any other event. Anthropic clients
// (and the official SDK) reject a stream whose first event is not message_start,
// so this can never be skipped — not even when the upstream opens straight with
// content and never sends a role frame.
func (s *OpenAIStreamToAnthropicStream) ensureStarted() {
	if s.started {
		return
	}
	s.started = true
	id := "msg_" + strings.TrimPrefix(s.messageID, "chatcmpl-")
	if id == "msg_" {
		id = fmt.Sprintf("msg_%d", s.created)
	}
	s.messageID = id
	s.writeEvent("message_start", map[string]any{
		"message": map[string]any{
			"id":            id,
			"type":          "message",
			"role":          "assistant",
			"model":         s.model,
			"content":       []any{},
			"stop_reason":   nil,
			"stop_sequence": nil,
			// The SDK merges message_delta usage into this object, so it has to
			// exist here already (it dereferences usage.output_tokens).
			"usage": map[string]any{"input_tokens": 0, "output_tokens": 0},
		},
	})
}

// ensureTextBlock opens the text content block on first use and re-opens it
// when a tool block interrupted it. A block that was already closed gets a new
// index (Anthropic never reopens a stopped block).
func (s *OpenAIStreamToAnthropicStream) ensureTextBlock() *anthropicStreamBlock {
	if s.text != nil && !s.text.stopped {
		if s.open != s.text {
			s.closeOpenBlock()
			s.open = s.text
		}
		return s.text
	}
	s.closeOpenBlock()
	block := &anthropicStreamBlock{index: s.nextIndex, kind: "text", toolIdx: -1}
	s.nextIndex++
	s.text = block
	s.open = block
	s.writeEvent("content_block_start", map[string]any{
		"index":         block.index,
		"content_block": map[string]any{"type": "text", "text": ""},
	})
	return block
}

func (s *OpenAIStreamToAnthropicStream) writeTextDelta(text string) {
	block := s.ensureTextBlock()
	s.writeEvent("content_block_delta", map[string]any{
		"index": block.index,
		"delta": map[string]any{"type": "text_delta", "text": text},
	})
}

// writeToolCall folds one upstream tool-call fragment into its tool_use block.
// Arguments stream as input_json_delta (partial_json), which is how Anthropic
// delivers tool input — a client that never sees the block start drops them.
func (s *OpenAIStreamToAnthropicStream) writeToolCall(call chatStreamToolCall) {
	index := 0
	if call.Index != nil {
		index = *call.Index
	}
	block := s.tools[index]
	if block == nil {
		block = &anthropicStreamBlock{index: s.nextIndex, kind: "tool_use", toolIdx: index}
		s.nextIndex++
		s.tools[index] = block
	}
	if call.ID != "" {
		block.id = call.ID
	}
	if call.Function != nil && call.Function.Name != "" {
		block.name = call.Function.Name
	}
	if block.id == "" {
		block.id = fmt.Sprintf("toolu_%d_%d", s.created, index)
	}
	if block.stopped {
		// A fragment for an already closed block cannot be replayed without
		// reopening it, which the protocol forbids. Upstreams stream arguments
		// for one call back to back, so this only drops malformed input.
		return
	}
	if s.open != block {
		s.closeOpenBlock()
		s.open = block
		s.writeEvent("content_block_start", map[string]any{
			"index": block.index,
			"content_block": map[string]any{
				"type": "tool_use", "id": block.id, "name": block.name, "input": map[string]any{},
			},
		})
	}
	if call.Function == nil || call.Function.Arguments == "" {
		return
	}
	s.writeEvent("content_block_delta", map[string]any{
		"index": block.index,
		"delta": map[string]any{"type": "input_json_delta", "partial_json": call.Function.Arguments},
	})
}

func (s *OpenAIStreamToAnthropicStream) closeOpenBlock() {
	if s.open == nil || s.open.stopped {
		s.open = nil
		return
	}
	block := s.open
	block.stopped = true
	s.open = nil
	s.writeEvent("content_block_stop", map[string]any{"index": block.index})
}

// writeMessageDelta emits the single message_delta that precedes message_stop:
// the stop reason plus the usage numbers that arrived during the stream.
func (s *OpenAIStreamToAnthropicStream) writeMessageDelta() {
	usage := map[string]any{
		"input_tokens":  s.promptTokens,
		"output_tokens": s.completionTokens,
	}
	if s.cacheRead > 0 {
		usage["cache_read_input_tokens"] = s.cacheRead
	}
	if s.cacheCreation > 0 {
		usage["cache_creation_input_tokens"] = s.cacheCreation
	}
	s.writeEvent("message_delta", map[string]any{
		"delta": map[string]any{
			"stop_reason":   s.resolvedStopReason(),
			"stop_sequence": nil,
		},
		"usage": usage,
	})
}

// resolvedStopReason prefers the upstream's finish_reason. A turn that carried
// tool_use blocks is always tool_use: the client has to run them, whatever the
// upstream called the finish, and a client that miscategorises it answers the
// user with nothing executed.
func (s *OpenAIStreamToAnthropicStream) resolvedStopReason() string {
	if len(s.tools) > 0 && mapOpenAIStopReason(s.stopReason) != "tool_use" {
		return "tool_use"
	}
	if !s.stopSet {
		return "end_turn"
	}
	return mapOpenAIStopReason(s.stopReason)
}

// finish closes the stream: last open block, message_delta, message_stop. It
// runs on [DONE] and on upstream EOF alike, and emits message_start first when
// the upstream produced no frame at all, so the client always sees a
// well-ordered (if empty) message.
func (s *OpenAIStreamToAnthropicStream) finish() {
	if s.done {
		return
	}
	s.done = true
	s.ensureStarted()
	s.closeOpenBlock()
	s.writeMessageDelta()
	s.writeEvent("message_stop", map[string]any{})
}

func (s *OpenAIStreamToAnthropicStream) writeEvent(eventType string, payload map[string]any) {
	// Anthropic clients read the event type from the DATA payload — the event:
	// line is only a hint. Without it the official SDK sees type=undefined and
	// throws `Unexpected event order, got undefined before "message_start"`
	// on the very first event.
	if _, ok := payload["type"]; !ok {
		payload["type"] = eventType
	}
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

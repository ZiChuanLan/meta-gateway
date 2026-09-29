package adapters

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestMessagesToOpenAIChat(t *testing.T) {
	body := []byte(`{
		"model": "gpt-4o",
		"max_tokens": 256,
		"system": "You are helpful.",
		"messages": [
			{"role": "user", "content": "Hello"},
			{"role": "assistant", "content": "Hi"},
			{"role": "user", "content": [{"type": "text", "text": "How are you?"}]}
		],
		"stream": true
	}`)
	converted, err := MessagesToOpenAIChat(body)
	if err != nil {
		t.Fatalf("MessagesToOpenAIChat: %v", err)
	}
	var outbound struct {
		Model     string `json:"model"`
		Stream    bool   `json:"stream"`
		MaxTokens int    `json:"max_tokens"`
		Messages  []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		StreamOptions struct {
			IncludeUsage bool `json:"include_usage"`
		} `json:"stream_options"`
	}
	if err := json.Unmarshal(converted, &outbound); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if outbound.Model != "gpt-4o" || !outbound.Stream || outbound.MaxTokens != 256 {
		t.Fatalf("outbound = %+v", outbound)
	}
	if len(outbound.Messages) != 4 {
		t.Fatalf("messages = %d, want 4 (system + 3)", len(outbound.Messages))
	}
	if outbound.Messages[0].Role != "system" || outbound.Messages[0].Content != "You are helpful." {
		t.Fatalf("messages[0] = %+v", outbound.Messages[0])
	}
	if outbound.Messages[3].Content != "How are you?" {
		t.Fatalf("messages[3] = %+v (part array must flatten)", outbound.Messages[3])
	}
	if !outbound.StreamOptions.IncludeUsage {
		t.Fatalf("stream_options.include_usage must be set")
	}
}

func TestOpenAIChatToMessages(t *testing.T) {
	body := []byte(`{
		"id": "chatcmpl-abc123",
		"object": "chat.completion",
		"model": "gpt-4o",
		"choices": [{"message": {"role": "assistant", "content": "Hello!"}, "finish_reason": "stop"}],
		"usage": {"prompt_tokens": 12, "completion_tokens": 3, "total_tokens": 15}
	}`)
	converted, err := OpenAIChatToMessages(body)
	if err != nil {
		t.Fatalf("OpenAIChatToMessages: %v", err)
	}
	var outbound struct {
		ID         string `json:"id"`
		Type       string `json:"type"`
		Role       string `json:"role"`
		StopReason string `json:"stop_reason"`
		Content    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(converted, &outbound); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if outbound.Type != "message" || outbound.Role != "assistant" {
		t.Fatalf("outbound = %+v", outbound)
	}
	if outbound.StopReason != "end_turn" {
		t.Fatalf("stop_reason = %q", outbound.StopReason)
	}
	if len(outbound.Content) != 1 || outbound.Content[0].Text != "Hello!" {
		t.Fatalf("content = %+v", outbound.Content)
	}
	if outbound.Usage.InputTokens != 12 || outbound.Usage.OutputTokens != 3 {
		t.Fatalf("usage = %+v", outbound.Usage)
	}
}

func TestOpenAIStreamToAnthropicStream(t *testing.T) {
	// OpenAI chunks: role, two text deltas, finish + usage.
	upstream := strings.NewReader(
		"data: {\"id\":\"chatcmpl-1\",\"model\":\"gpt-4o\",\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n" +
			"data: {\"id\":\"chatcmpl-1\",\"model\":\"gpt-4o\",\"choices\":[{\"delta\":{\"content\":\"Hel\"}}]}\n\n" +
			"data: {\"id\":\"chatcmpl-1\",\"model\":\"gpt-4o\",\"choices\":[{\"delta\":{\"content\":\"lo\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":2}}\n\n" +
			"data: [DONE]\n\n",
	)
	stream := NewOpenAIStreamToAnthropicStream(io.NopCloser(upstream))
	raw, err := io.ReadAll(stream)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	text := string(raw)
	for _, want := range []string{
		"event: message_start",
		`"type":"message"`,
		"event: content_block_start",
		"event: content_block_delta",
		`"text":"Hel"`,
		`"type":"text_delta"`,
		`"text":"lo"`,
		"event: content_block_stop",
		"event: message_delta",
		`"stop_reason":"end_turn"`,
		`"input_tokens":5`,
		`"output_tokens":2`,
		"event: message_stop",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("stream missing %q in %s", want, text)
		}
	}
	// Event order and block framing are what Anthropic clients enforce: the SDK's
	// MessageStream throws on any event before message_start and drops a
	// content_block_delta whose index has no content_block_start.
	if got := sseEventNames(text); got != "message_start,content_block_start,content_block_delta,content_block_delta,content_block_stop,message_delta,message_stop" {
		t.Fatalf("event sequence = %s\n%s", got, text)
	}
	message, _ := sseData(t, text, "message_start")["message"].(map[string]any)
	if _, ok := message["usage"]; !ok {
		t.Fatalf("message_start must carry usage (the SDK merges message_delta into it): %v", message)
	}
	if message["stop_reason"] != nil {
		t.Fatalf("message_start.stop_reason must be null: %v", message)
	}
	block, _ := sseData(t, text, "content_block_start")["content_block"].(map[string]any)
	if block["type"] != "text" {
		t.Fatalf("first block = %v, want an empty text block", block)
	}
	assertSSEPayloadTypes(t, text)
}

// sseEventNames lists the event: names in the order they were written.
func sseEventNames(body string) string {
	var names []string
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "event: ") {
			names = append(names, strings.TrimSpace(strings.TrimPrefix(line, "event: ")))
		}
	}
	return strings.Join(names, ",")
}

// assertSSEPayloadTypes checks that every frame's data payload repeats the
// event name in its "type" field: Anthropic clients (and the official SDK)
// read the type from the payload, so a frame that only names it on the event:
// line arrives as type=undefined.
func assertSSEPayloadTypes(t *testing.T, body string) {
	t.Helper()
	for _, frame := range strings.Split(body, "\n\n") {
		frame = strings.TrimSpace(frame)
		if frame == "" {
			continue
		}
		lines := strings.Split(frame, "\n")
		if !strings.HasPrefix(lines[0], "event: ") {
			t.Fatalf("frame without event line: %q", frame)
		}
		name := strings.TrimSpace(strings.TrimPrefix(lines[0], "event: "))
		var data string
		for _, line := range lines[1:] {
			if strings.HasPrefix(line, "data: ") {
				data = strings.TrimPrefix(line, "data: ")
			}
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(data), &payload); err != nil {
			t.Fatalf("decode %s payload: %v", name, err)
		}
		if payload["type"] != name {
			t.Fatalf("event %s carries type=%v, want %q in payload", name, payload["type"], name)
		}
	}
}

// TestOpenAIStreamToAnthropicStreamOpensWithoutRoleFrame pins the case the SDK
// used to hard-fail on: an upstream whose first chunk already carries content
// and never sends a role frame. message_start must still come first.
func TestOpenAIStreamToAnthropicStreamOpensWithoutRoleFrame(t *testing.T) {
	upstream := strings.NewReader(
		"data: {\"id\":\"cmpl-9\",\"model\":\"m\",\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n" +
			"data: [DONE]\n\n",
	)
	stream := NewOpenAIStreamToAnthropicStream(io.NopCloser(upstream))
	raw, err := io.ReadAll(stream)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	text := string(raw)
	if !strings.HasPrefix(text, "event: message_start\n") {
		t.Fatalf("stream must open with message_start:\n%s", text)
	}
	if got := sseEventNames(text); got != "message_start,content_block_start,content_block_delta,content_block_stop,message_delta,message_stop" {
		t.Fatalf("event sequence = %s\n%s", got, text)
	}
	if delta, _ := sseData(t, text, "content_block_delta")["delta"].(map[string]any); delta["text"] != "hi" {
		t.Fatalf("text delta = %v", delta)
	}
}

// TestOpenAIStreamToAnthropicStreamToolCalls covers streamed tool calls: they
// become a tool_use block whose arguments arrive as input_json_delta. Ignoring
// delta.tool_calls (as this used to) leaves an agentic client with nothing to
// execute and no way to ever see the arguments.
func TestOpenAIStreamToAnthropicStreamToolCalls(t *testing.T) {
	upstream := strings.NewReader(
		"data: {\"id\":\"cmpl-2\",\"model\":\"m\",\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n" +
			"data: {\"id\":\"cmpl-2\",\"model\":\"m\",\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"shell\",\"arguments\":\"{\\\"cmd\\\":\"}}]}}]}\n\n" +
			"data: {\"id\":\"cmpl-2\",\"model\":\"m\",\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"ls\\\"}\"}}]}}]}\n\n" +
			"data: {\"id\":\"cmpl-2\",\"model\":\"m\",\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":4}}\n\n" +
			"data: [DONE]\n\n",
	)
	stream := NewOpenAIStreamToAnthropicStream(io.NopCloser(upstream))
	raw, err := io.ReadAll(stream)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	text := string(raw)
	block, _ := sseData(t, text, "content_block_start")["content_block"].(map[string]any)
	if block["type"] != "tool_use" || block["id"] != "call_1" || block["name"] != "shell" {
		t.Fatalf("tool_use block = %v\n%s", block, text)
	}
	var arguments strings.Builder
	for _, frame := range strings.Split(text, "\n\n") {
		if !strings.HasPrefix(frame, "event: content_block_delta") {
			continue
		}
		for _, line := range strings.Split(frame, "\n") {
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var payload struct {
				Delta struct {
					Type        string `json:"type"`
					PartialJSON string `json:"partial_json"`
				} `json:"delta"`
			}
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &payload); err != nil {
				t.Fatalf("decode delta: %v", err)
			}
			if payload.Delta.Type != "input_json_delta" {
				t.Fatalf("delta type = %q, want input_json_delta", payload.Delta.Type)
			}
			arguments.WriteString(payload.Delta.PartialJSON)
		}
	}
	if arguments.String() != `{"cmd":"ls"}` {
		t.Fatalf("streamed arguments = %q\n%s", arguments.String(), text)
	}
	delta, _ := sseData(t, text, "message_delta")["delta"].(map[string]any)
	if delta["stop_reason"] != "tool_use" {
		t.Fatalf("stop_reason = %v, want tool_use", delta["stop_reason"])
	}
	if got := sseEventNames(text); got != "message_start,content_block_start,content_block_delta,content_block_delta,content_block_stop,message_delta,message_stop" {
		t.Fatalf("event sequence = %s\n%s", got, text)
	}
	assertSSEPayloadTypes(t, text)
}

// TestMessagesToOpenAIChatTools covers the request direction: tool declarations
// and the tool_use / tool_result blocks of a multi-turn tool conversation must
// survive the pivot, or the upstream never learns it can call a tool and never
// sees what one returned.
func TestMessagesToOpenAIChatTools(t *testing.T) {
	body := []byte(`{
		"model": "claude-x",
		"max_tokens": 128,
		"tools": [{"name":"shell","description":"run a command","input_schema":{"type":"object","properties":{"cmd":{"type":"string"}}}}],
		"tool_choice": {"type": "tool", "name": "shell"},
		"messages": [
			{"role": "user", "content": "list the files"},
			{"role": "assistant", "content": [
				{"type": "text", "text": "on it"},
				{"type": "tool_use", "id": "toolu_1", "name": "shell", "input": {"cmd": "ls"}}
			]},
			{"role": "user", "content": [
				{"type": "tool_result", "tool_use_id": "toolu_1", "content": [{"type": "text", "text": "a.txt"}]}
			]}
		]
	}`)
	converted, err := MessagesToOpenAIChat(body)
	if err != nil {
		t.Fatalf("MessagesToOpenAIChat: %v", err)
	}
	var outbound struct {
		Tools []struct {
			Type     string `json:"type"`
			Function struct {
				Name       string `json:"name"`
				Parameters struct {
					Properties map[string]any `json:"properties"`
				} `json:"parameters"`
			} `json:"function"`
		} `json:"tools"`
		ToolChoice struct {
			Type     string `json:"type"`
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tool_choice"`
		Messages []struct {
			Role       string `json:"role"`
			Content    any    `json:"content"`
			ToolCallID string `json:"tool_call_id"`
			ToolCalls  []struct {
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(converted, &outbound); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(outbound.Tools) != 1 || outbound.Tools[0].Type != "function" || outbound.Tools[0].Function.Name != "shell" {
		t.Fatalf("tools = %+v", outbound.Tools)
	}
	if _, ok := outbound.Tools[0].Function.Parameters.Properties["cmd"]; !ok {
		t.Fatalf("input_schema must become parameters: %+v", outbound.Tools[0].Function.Parameters)
	}
	if outbound.ToolChoice.Type != "function" || outbound.ToolChoice.Function.Name != "shell" {
		t.Fatalf("tool_choice = %+v", outbound.ToolChoice)
	}
	if len(outbound.Messages) != 3 {
		t.Fatalf("messages = %d, want 3 (user, assistant, tool): %+v", len(outbound.Messages), outbound.Messages)
	}
	assistant := outbound.Messages[1]
	if assistant.Role != "assistant" || assistant.Content != "on it" || len(assistant.ToolCalls) != 1 {
		t.Fatalf("assistant message = %+v", assistant)
	}
	if assistant.ToolCalls[0].ID != "toolu_1" || assistant.ToolCalls[0].Function.Name != "shell" {
		t.Fatalf("assistant tool_calls = %+v", assistant.ToolCalls[0])
	}
	var arguments struct {
		Cmd string `json:"cmd"`
	}
	if err := json.Unmarshal([]byte(assistant.ToolCalls[0].Function.Arguments), &arguments); err != nil || arguments.Cmd != "ls" {
		t.Fatalf("assistant tool arguments = %q (err %v)", assistant.ToolCalls[0].Function.Arguments, err)
	}
	toolMessage := outbound.Messages[2]
	if toolMessage.Role != "tool" || toolMessage.ToolCallID != "toolu_1" || toolMessage.Content != "a.txt" {
		t.Fatalf("tool message = %+v", toolMessage)
	}
}

// TestOpenAIChatToMessagesToolCalls covers the response direction: a chat
// completion whose answer is a function call must come back as a tool_use
// block, otherwise the client is handed stop_reason=tool_use with nothing in
// content to execute.
func TestOpenAIChatToMessagesToolCalls(t *testing.T) {
	body := []byte(`{
		"id": "chatcmpl-77",
		"object": "chat.completion",
		"model": "gpt-4o",
		"choices": [{"message": {"role": "assistant", "content": null,
			"tool_calls": [{"id": "call_1", "type": "function", "function": {"name": "shell", "arguments": "{\"cmd\":\"ls\"}"}}]},
			"finish_reason": "tool_calls"}],
		"usage": {"prompt_tokens": 9, "completion_tokens": 4, "prompt_tokens_details": {"cached_tokens": 3}}
	}`)
	converted, err := OpenAIChatToMessages(body)
	if err != nil {
		t.Fatalf("OpenAIChatToMessages: %v", err)
	}
	var outbound struct {
		StopReason string `json:"stop_reason"`
		Content    []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
		Usage struct {
			InputTokens          int `json:"input_tokens"`
			OutputTokens         int `json:"output_tokens"`
			CacheReadInputTokens int `json:"cache_read_input_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(converted, &outbound); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if outbound.StopReason != "tool_use" {
		t.Fatalf("stop_reason = %q, want tool_use", outbound.StopReason)
	}
	if len(outbound.Content) != 1 || outbound.Content[0].Type != "tool_use" || outbound.Content[0].ID != "call_1" || outbound.Content[0].Name != "shell" {
		t.Fatalf("content = %+v", outbound.Content)
	}
	if strings.TrimSpace(string(outbound.Content[0].Input)) != `{"cmd":"ls"}` {
		t.Fatalf("input = %s", outbound.Content[0].Input)
	}
	if outbound.Usage.InputTokens != 9 || outbound.Usage.OutputTokens != 4 || outbound.Usage.CacheReadInputTokens != 3 {
		t.Fatalf("usage = %+v", outbound.Usage)
	}
}

// TestOpenAIChatToMessagesContentParts tolerates upstreams that return content
// as a part array (the old string-only field made the whole conversion fail).
func TestOpenAIChatToMessagesContentParts(t *testing.T) {
	body := []byte(`{"id":"chatcmpl-8","model":"m","choices":[{"message":{"role":"assistant",
		"content":[{"type":"text","text":"Hel"},{"type":"text","text":"lo"}]},"finish_reason":"stop"}],"usage":{}}`)
	converted, err := OpenAIChatToMessages(body)
	if err != nil {
		t.Fatalf("OpenAIChatToMessages: %v", err)
	}
	if !strings.Contains(string(converted), `"text":"Hello"`) {
		t.Fatalf("array content must flatten: %s", converted)
	}
}

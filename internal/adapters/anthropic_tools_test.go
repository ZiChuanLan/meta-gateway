// Tool-call fidelity across the Anthropic Messages boundary, both directions:
// Anthropic clients served by OpenAI-compatible channels (Messages → chat) and
// OpenAI clients served by Anthropic channels (chat → Messages). A dropped
// tool_use / tool_calls entry is invisible in logs but breaks every agentic
// client: it is handed a stop reason that promises a call with nothing to run.
package adapters

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestChatToAnthropicMessagesToolCalls(t *testing.T) {
	body := []byte(`{
		"model": "glm-5.3-flash",
		"max_tokens": 256,
		"tools": [{"type":"function","function":{"name":"shell","description":"run a command","parameters":{"type":"object","properties":{"cmd":{"type":"string"}}}}}],
		"tool_choice": {"type":"function","function":{"name":"shell"}},
		"messages": [
			{"role": "user", "content": "list the files"},
			{"role": "assistant", "content": "on it", "tool_calls": [{"id":"call_1","type":"function","function":{"name":"shell","arguments":"{\"cmd\":\"ls\"}"}}]},
			{"role": "tool", "tool_call_id": "call_1", "content": "a.txt"}
		]
	}`)
	converted, err := ChatToAnthropicMessages(body)
	if err != nil {
		t.Fatalf("ChatToAnthropicMessages: %v", err)
	}
	var outbound struct {
		Tools []struct {
			Name        string          `json:"name"`
			InputSchema json.RawMessage `json:"input_schema"`
		} `json:"tools"`
		ToolChoice struct {
			Type string `json:"type"`
			Name string `json:"name"`
		} `json:"tool_choice"`
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(converted, &outbound); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// A user turn stays a plain string; the turns carrying blocks decode as arrays.
	type block struct {
		Type      string          `json:"type"`
		Text      string          `json:"text"`
		ID        string          `json:"id"`
		Name      string          `json:"name"`
		Input     json.RawMessage `json:"input"`
		ToolUseID string          `json:"tool_use_id"`
		Content   string          `json:"content"`
	}
	blocksOf := func(raw json.RawMessage) []block {
		var blocks []block
		if err := json.Unmarshal(raw, &blocks); err == nil {
			return blocks
		}
		var text string
		if err := json.Unmarshal(raw, &text); err == nil && text != "" {
			return []block{{Type: "text", Text: text}}
		}
		return nil
	}
	if len(outbound.Tools) != 1 || outbound.Tools[0].Name != "shell" {
		t.Fatalf("tools = %+v", outbound.Tools)
	}
	var schema struct {
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(outbound.Tools[0].InputSchema, &schema); err != nil {
		t.Fatalf("input_schema: %v", err)
	}
	if _, ok := schema.Properties["cmd"]; !ok {
		t.Fatalf("parameters must become input_schema: %s", outbound.Tools[0].InputSchema)
	}
	if outbound.ToolChoice.Type != "tool" || outbound.ToolChoice.Name != "shell" {
		t.Fatalf("tool_choice = %+v", outbound.ToolChoice)
	}
	if len(outbound.Messages) != 3 {
		t.Fatalf("messages = %+v", outbound.Messages)
	}
	if got := blocksOf(outbound.Messages[0].Content); len(got) != 1 || got[0].Text != "list the files" {
		t.Fatalf("user message = %s", outbound.Messages[0].Content)
	}
	assistant := outbound.Messages[1]
	assistantBlocks := blocksOf(assistant.Content)
	if assistant.Role != "assistant" || len(assistantBlocks) != 2 {
		t.Fatalf("assistant message = %+v (%s)", assistant, assistant.Content)
	}
	if assistantBlocks[0].Type != "text" || assistantBlocks[0].Text != "on it" {
		t.Fatalf("assistant text block = %+v", assistantBlocks[0])
	}
	call := assistantBlocks[1]
	if call.Type != "tool_use" || call.ID != "call_1" || call.Name != "shell" {
		t.Fatalf("assistant tool_use block = %+v", call)
	}
	var input struct {
		Cmd string `json:"cmd"`
	}
	if err := json.Unmarshal(call.Input, &input); err != nil || input.Cmd != "ls" {
		t.Fatalf("tool_use input = %s (err %v)", call.Input, err)
	}
	result := outbound.Messages[2]
	resultBlocks := blocksOf(result.Content)
	if result.Role != "user" || len(resultBlocks) != 1 || resultBlocks[0].Type != "tool_result" {
		t.Fatalf("tool result message = %+v (%s)", result, result.Content)
	}
	if resultBlocks[0].ToolUseID != "call_1" || resultBlocks[0].Content != "a.txt" {
		t.Fatalf("tool_result block = %+v", resultBlocks[0])
	}
}

func TestAnthropicMessagesToChatToolUse(t *testing.T) {
	body := []byte(`{
		"id": "msg_1",
		"type": "message",
		"role": "assistant",
		"model": "claude-x",
		"content": [
			{"type": "text", "text": "on it"},
			{"type": "tool_use", "id": "toolu_1", "name": "shell", "input": {"cmd": "ls"}}
		],
		"stop_reason": "tool_use",
		"usage": {"input_tokens": 11, "output_tokens": 6}
	}`)
	converted, err := AnthropicMessagesToChat(body)
	if err != nil {
		t.Fatalf("AnthropicMessagesToChat: %v", err)
	}
	var outbound struct {
		Choices []struct {
			Message struct {
				Content   any `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(converted, &outbound); err != nil {
		t.Fatalf("decode: %v", err)
	}
	choice := outbound.Choices[0]
	if choice.FinishReason != "tool_calls" {
		t.Fatalf("finish_reason = %q, want tool_calls", choice.FinishReason)
	}
	if choice.Message.Content != "on it" || len(choice.Message.ToolCalls) != 1 {
		t.Fatalf("message = %+v", choice.Message)
	}
	call := choice.Message.ToolCalls[0]
	if call.ID != "toolu_1" || call.Type != "function" || call.Function.Name != "shell" {
		t.Fatalf("tool call = %+v", call)
	}
	var arguments struct {
		Cmd string `json:"cmd"`
	}
	if err := json.Unmarshal([]byte(call.Function.Arguments), &arguments); err != nil || arguments.Cmd != "ls" {
		t.Fatalf("arguments = %q (err %v)", call.Function.Arguments, err)
	}
}

func TestAnthropicToOpenAIStreamToolUse(t *testing.T) {
	upstream := strings.NewReader(
		"event: message_start\n" +
			`data: {"type":"message_start","message":{"id":"msg_1","model":"claude-x","content":[],"usage":{"input_tokens":11,"output_tokens":0}}}` + "\n\n" +
			"event: content_block_start\n" +
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"shell","input":{}}}` + "\n\n" +
			"event: content_block_delta\n" +
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"cmd\":"}}` + "\n\n" +
			"event: content_block_delta\n" +
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"\"ls\"}"}}` + "\n\n" +
			"event: content_block_stop\n" +
			`data: {"type":"content_block_stop","index":0}` + "\n\n" +
			"event: message_delta\n" +
			`data: {"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":6}}` + "\n\n" +
			"event: message_stop\n" +
			`data: {"type":"message_stop"}` + "\n\n",
	)
	stream := NewAnthropicToOpenAIStream(io.NopCloser(upstream))
	raw, err := io.ReadAll(stream)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	type toolCallDelta struct {
		Index    int    `json:"index"`
		ID       string `json:"id"`
		Type     string `json:"type"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	}
	var opener *toolCallDelta
	var arguments strings.Builder
	finishReason := ""
	var stopReasonSeen bool
	for _, frame := range strings.Split(string(raw), "\n\n") {
		line := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(frame), "data:"))
		if line == "" || line == "[DONE]" {
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					ToolCalls []toolCallDelta `json:"tool_calls"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(line), &chunk); err != nil {
			t.Fatalf("decode chunk %q: %v", line, err)
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		if chunk.Choices[0].FinishReason != "" {
			finishReason = chunk.Choices[0].FinishReason
		}
		for _, call := range chunk.Choices[0].Delta.ToolCalls {
			if call.ID != "" {
				copied := call
				opener = &copied
			}
			if call.Function.Arguments != "" {
				arguments.WriteString(call.Function.Arguments)
			}
		}
		if strings.Contains(line, "\"arguments\"") {
			stopReasonSeen = true
		}
	}
	if opener == nil {
		t.Fatalf("no tool_calls opener chunk in stream:\n%s", raw)
	}
	if opener.ID != "toolu_1" || opener.Type != "function" || opener.Function.Name != "shell" {
		t.Fatalf("tool_calls opener = %+v", opener)
	}
	if !stopReasonSeen {
		t.Fatalf("no argument fragments forwarded:\n%s", raw)
	}
	if arguments.String() != `{"cmd":"ls"}` {
		t.Fatalf("streamed arguments = %q (want %q)", arguments.String(), `{"cmd":"ls"}`)
	}
	if finishReason != "tool_calls" {
		t.Fatalf("finish_reason = %q, want tool_calls", finishReason)
	}
}

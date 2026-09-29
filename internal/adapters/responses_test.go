package adapters

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestResponsesToChatBasic(t *testing.T) {
	body := []byte(`{
		"model":"gpt-4o",
		"instructions":"You are a helpful assistant.",
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello!"}]}
		],
		"stream":true,
		"max_output_tokens":1200,
		"temperature":0.7,
		"tools":[{"type":"function","name":"lookup","description":"look things up","parameters":{"type":"object"}}],
		"tool_choice":"auto",
		"text":{"format":{"type":"json_object"}}
	}`)
	converted, err := ResponsesToChat(body)
	if err != nil {
		t.Fatalf("ResponsesToChat: %v", err)
	}
	var chat struct {
		Model    string `json:"model"`
		Stream   bool   `json:"stream"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		MaxTokens   int      `json:"max_tokens"`
		Temperature *float64 `json:"temperature"`
		Tools       []struct {
			Type     string `json:"type"`
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
		ToolChoice     string `json:"tool_choice"`
		ResponseFormat struct {
			Type string `json:"type"`
		} `json:"response_format"`
		StreamOptions struct {
			IncludeUsage bool `json:"include_usage"`
		} `json:"stream_options"`
	}
	if err := json.Unmarshal(converted, &chat); err != nil {
		t.Fatalf("converted chat body not JSON: %v (%s)", err, converted)
	}
	if chat.Model != "gpt-4o" || !chat.Stream || len(chat.Messages) != 3 {
		t.Fatalf("unexpected chat: %+v", chat)
	}
	if chat.Messages[0].Role != "system" || chat.Messages[0].Content != "You are a helpful assistant." {
		t.Fatalf("instructions not mapped to system: %+v", chat.Messages[0])
	}
	if chat.Messages[1].Role != "user" || chat.Messages[1].Content != "hi" {
		t.Fatalf("user message wrong: %+v", chat.Messages[1])
	}
	if chat.Messages[2].Role != "assistant" || chat.Messages[2].Content != "hello!" {
		t.Fatalf("assistant message wrong: %+v", chat.Messages[2])
	}
	if chat.MaxTokens != 1200 || chat.Temperature == nil || *chat.Temperature != 0.7 {
		t.Fatalf("params not mapped: %+v", chat)
	}
	if len(chat.Tools) != 1 || chat.Tools[0].Function.Name != "lookup" {
		t.Fatalf("tools not mapped: %+v", chat.Tools)
	}
	if chat.ToolChoice != "auto" {
		t.Fatalf("tool_choice not mapped: %+v", chat.ToolChoice)
	}
	if chat.ResponseFormat.Type != "json_object" {
		t.Fatalf("text.format not mapped: %+v", chat.ResponseFormat)
	}
	if !chat.StreamOptions.IncludeUsage {
		t.Fatal("stream_options.include_usage must be forced on streams")
	}
}

func TestResponsesToChatStringInput(t *testing.T) {
	converted, err := ResponsesToChat([]byte(`{"model":"m","input":"just text"}`))
	if err != nil {
		t.Fatal(err)
	}
	var chat struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(converted, &chat); err != nil {
		t.Fatal(err)
	}
	if len(chat.Messages) != 1 || chat.Messages[0].Role != "user" || chat.Messages[0].Content != "just text" {
		t.Fatalf("string input not mapped: %+v", chat.Messages)
	}
}

func TestResponsesToChatToolCalls(t *testing.T) {
	body := []byte(`{"model":"m","input":[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"weather?"}]},
		{"type":"function_call","call_id":"call_1","name":"get_weather","output":"{\"city\":\"tokyo\"}"},
		{"type":"function_call_output","call_id":"call_1","output":"sunny"}
	]}`)
	converted, err := ResponsesToChat(body)
	if err != nil {
		t.Fatal(err)
	}
	var chat struct {
		Messages []struct {
			Role       string `json:"role"`
			Content    string `json:"content"`
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
	if err := json.Unmarshal(converted, &chat); err != nil {
		t.Fatal(err)
	}
	desired := len(chat.Messages)
	if desired != 3 {
		t.Fatalf("expected 3 messages, got %d: %+v", desired, chat.Messages)
	}
	call := chat.Messages[1]
	if call.Role != "assistant" || len(call.ToolCalls) != 1 ||
		call.ToolCalls[0].ID != "call_1" || call.ToolCalls[0].Function.Name != "get_weather" || call.ToolCalls[0].Function.Arguments != `{"city":"tokyo"}` {
		t.Fatalf("function_call not mapped: %+v", call)
	}
	out := chat.Messages[2]
	if out.Role != "tool" || out.ToolCallID != "call_1" || out.Content != "sunny" {
		t.Fatalf("function_call_output not mapped: %+v", out)
	}
}

func TestChatToResponses(t *testing.T) {
	chat := `{
		"id":"chatcmpl-123","object":"chat.completion","created":1720000000,"model":"gpt-4o",
		"choices":[{"index":0,"message":{"role":"assistant","content":"the answer"},"finish_reason":"stop"}],
		"usage":{"prompt_tokens":10,"completion_tokens":4,"total_tokens":14,"prompt_tokens_details":{"cached_tokens":6}}
	}`
	converted, err := ChatToResponses([]byte(chat))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Object string `json:"object"`
		Status string `json:"status"`
		Model  string `json:"model"`
		Output []struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
			TotalTokens  int `json:"total_tokens"`
			InputDetails struct {
				Cached int `json:"cached_tokens"`
			} `json:"input_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(converted, &doc); err != nil {
		t.Fatalf("converted responses doc invalid: %v (%s)", err, converted)
	}
	if doc.Object != "response" || doc.Status != "completed" || doc.Model != "gpt-4o" {
		t.Fatalf("bad envelope: %+v", doc)
	}
	if len(doc.Output) != 1 || doc.Output[0].Type != "message" || doc.Output[0].Content[0].Text != "the answer" {
		t.Fatalf("output not mapped: %+v", doc.Output)
	}
	if doc.Usage.InputTokens != 10 || doc.Usage.OutputTokens != 4 || doc.Usage.TotalTokens != 14 {
		t.Fatalf("usage not mapped: %+v", doc.Usage)
	}
	if doc.Usage.InputDetails.Cached != 6 {
		t.Fatalf("cached tokens not mapped: %+v", doc.Usage.InputDetails)
	}
}

func TestChatToResponsesToolCall(t *testing.T) {
	chat := `{
		"id":"chatcmpl-2","created":1,"model":"m",
		"choices":[{"index":0,"finish_reason":"tool_calls","message":{"role":"assistant","content":null,
			"tool_calls":[{"id":"call_9","type":"function","function":{"name":"f","arguments":"{\"a\":1}"}}]}}]
	}`
	converted, err := ChatToResponses([]byte(chat))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Output []struct {
			Type      string `json:"type"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"output"`
	}
	if err := json.Unmarshal(converted, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Output) != 1 || doc.Output[0].Type != "function_call" || doc.Output[0].Name != "f" || doc.Output[0].Arguments != `{"a":1}` {
		t.Fatalf("tool call not mapped: %+v", doc.Output)
	}
}

func TestChatStreamToResponsesStream(t *testing.T) {
	source := "data: {\"id\":\"cmpl-1\",\"model\":\"gpt\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"}}]}\n\n" +
		"data: {\"id\":\"cmpl-1\",\"model\":\"gpt\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hello\"}}]}\n\n" +
		"data: {\"id\":\"cmpl-1\",\"model\":\"gpt\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\" world\"}}]}\n\n" +
		"data: {\"id\":\"cmpl-1\",\"model\":\"gpt\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: {\"id\":\"cmpl-1\",\"model\":\"gpt\",\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2,\"total_tokens\":5}}\n\n" +
		"data: [DONE]\n\n"
	wrapped := NewChatStreamToResponsesStream(io.NopCloser(strings.NewReader(source)))
	defer wrapped.Close()

	var out strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := wrapped.Read(buf)
		if n > 0 {
			out.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}
	events := out.String()
	for _, want := range []string{
		"event: response.created",
		"event: response.in_progress",
		"event: response.output_item.added",
		"event: response.content_part.added",
		"event: response.output_text.delta",
		"event: response.output_text.done",
		"event: response.content_part.done",
		"event: response.output_item.done",
		"event: response.completed",
	} {
		if !strings.Contains(events, want) {
			t.Fatalf("missing event %q in stream:\n%s", want, events)
		}
	}
	if !strings.Contains(events, "\"delta\":\"Hello\"") || !strings.Contains(events, "\"delta\":\" world\"") {
		t.Fatalf("deltas missing:\n%s", events)
	}
	if !strings.Contains(events, "\"total_tokens\":5") {
		t.Fatalf("usage missing from completed event:\n%s", events)
	}
	// The terminal events are authoritative for Responses clients: they carry
	// the whole answer, and a client that sees an empty final text discards
	// everything it streamed (Codex/ChatGPT blank out the message).
	if got := eventText(t, events, "response.output_text.done"); got != "Hello world" {
		t.Fatalf("output_text.done text = %q, want %q", got, "Hello world")
	}
	if got := eventPartText(t, events, "response.content_part.done"); got != "Hello world" {
		t.Fatalf("content_part.done text = %q, want %q", got, "Hello world")
	}
	if got := eventItemText(t, events, "response.output_item.done"); got != "Hello world" {
		t.Fatalf("output_item.done text = %q, want %q", got, "Hello world")
	}
	if got := completedOutputText(t, events); got != "Hello world" {
		t.Fatalf("response.completed output text = %q, want %q", got, "Hello world")
	}
}

// sseData returns the decoded data payload of the last event with the given
// name from a rendered Responses SSE body.
func sseData(t *testing.T, events, name string) map[string]any {
	t.Helper()
	var payload map[string]any
	found := false
	for _, frame := range strings.Split(events, "\n\n") {
		lines := strings.Split(strings.TrimSpace(frame), "\n")
		if len(lines) == 0 || strings.TrimSpace(lines[0]) != "event: "+name {
			continue
		}
		for _, line := range lines[1:] {
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			payload = map[string]any{}
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &payload); err != nil {
				t.Fatalf("decode %s payload: %v", name, err)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("event %s missing in:\n%s", name, events)
	}
	return payload
}

func eventText(t *testing.T, events, name string) string {
	t.Helper()
	text, _ := sseData(t, events, name)["text"].(string)
	return text
}

func eventPartText(t *testing.T, events, name string) string {
	t.Helper()
	part, _ := sseData(t, events, name)["part"].(map[string]any)
	text, _ := part["text"].(string)
	return text
}

func eventItemText(t *testing.T, events, name string) string {
	t.Helper()
	item, _ := sseData(t, events, name)["item"].(map[string]any)
	return itemContentText(item)
}

// completedOutputText reads response.output[0]'s text out of the terminal
// response.completed event — the value the client keeps after streaming.
func completedOutputText(t *testing.T, events string) string {
	t.Helper()
	items := completedOutputItems(t, events)
	if len(items) == 0 {
		t.Fatalf("response.completed carries no output items:\n%s", events)
	}
	return itemContentText(items[0])
}

// completedOutputItems returns the output array of the terminal
// response.completed event in order.
func completedOutputItems(t *testing.T, events string) []map[string]any {
	t.Helper()
	response, _ := sseData(t, events, "response.completed")["response"].(map[string]any)
	raw, _ := response["output"].([]any)
	items := make([]map[string]any, 0, len(raw))
	for _, entry := range raw {
		item, _ := entry.(map[string]any)
		items = append(items, item)
	}
	return items
}

// TestChatStreamToResponsesStreamToolCalls covers function-call streaming: the
// chat tool_call deltas assemble into a function_call output item instead of
// being dropped (an agentic client would otherwise get an empty answer and no
// call to execute).
func TestChatStreamToResponsesStreamToolCalls(t *testing.T) {
	source := `data: {"id":"cmpl-1","model":"gpt","choices":[{"index":0,"delta":{"role":"assistant"}}]}` + "\n\n" +
		`data: {"id":"cmpl-1","model":"gpt","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"shell","arguments":"{\"cmd\":"}}]}}]}` + "\n\n" +
		`data: {"id":"cmpl-1","model":"gpt","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"ls\"}"}}]}}]}` + "\n\n" +
		`data: {"id":"cmpl-1","model":"gpt","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n" +
		`data: [DONE]` + "\n\n"
	wrapped := NewChatStreamToResponsesStream(io.NopCloser(strings.NewReader(source)))
	defer wrapped.Close()

	events := drainStream(t, wrapped)
	added := sseData(t, events, "response.output_item.added")
	item, _ := added["item"].(map[string]any)
	if item["type"] != "function_call" || item["name"] != "shell" || item["call_id"] != "call_1" {
		t.Fatalf("function_call item not announced: %+v", item)
	}
	argumentsDone, _ := sseData(t, events, "response.function_call_arguments.done")["arguments"].(string)
	if argumentsDone != `{"cmd":"ls"}` {
		t.Fatalf("arguments assembled = %q, want %q", argumentsDone, `{"cmd":"ls"}`)
	}
	if !strings.Contains(events, `"delta":"{\"cmd\":"`) || !strings.Contains(events, `"delta":"\"ls\"}"`) {
		t.Fatalf("argument deltas missing:\n%s", events)
	}
	items := completedOutputItems(t, events)
	if len(items) != 1 {
		t.Fatalf("tool-call-only turn must carry exactly one item, got %d:\n%s", len(items), events)
	}
	if items[0]["type"] != "function_call" || items[0]["name"] != "shell" || items[0]["arguments"] != `{"cmd":"ls"}` {
		t.Fatalf("completed function_call wrong: %+v", items[0])
	}
}

// TestChatStreamToResponsesStreamTextAndToolCall keeps both output kinds in
// first-seen order: text opens the message item, the later call follows it.
func TestChatStreamToResponsesStreamTextAndToolCall(t *testing.T) {
	source := `data: {"id":"cmpl-1","model":"gpt","choices":[{"index":0,"delta":{"content":"let me check"}}]}` + "\n\n" +
		`data: {"id":"cmpl-1","model":"gpt","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_9","type":"function","function":{"name":"shell","arguments":"{}"}}]}}]}` + "\n\n" +
		`data: [DONE]` + "\n\n"
	wrapped := NewChatStreamToResponsesStream(io.NopCloser(strings.NewReader(source)))
	defer wrapped.Close()

	events := drainStream(t, wrapped)
	items := completedOutputItems(t, events)
	if len(items) != 2 {
		t.Fatalf("expected message + function_call, got %d:\n%s", len(items), events)
	}
	if items[0]["type"] != "message" || itemContentText(items[0]) != "let me check" {
		t.Fatalf("message item wrong: %+v", items[0])
	}
	if items[1]["type"] != "function_call" || items[1]["arguments"] != "{}" {
		t.Fatalf("function_call item wrong: %+v", items[1])
	}
}

// TestChatStreamToResponsesStreamToolCallBeforeText pins the index allocation
// when the call shows up first: output items keep first-seen order rather than
// assuming the message always owns index 0.
func TestChatStreamToResponsesStreamToolCallBeforeText(t *testing.T) {
	source := `data: {"id":"cmpl-1","model":"gpt","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_3","type":"function","function":{"name":"shell","arguments":"{}"}}]}}]}` + "\n\n" +
		`data: {"id":"cmpl-1","model":"gpt","choices":[{"index":0,"delta":{"content":"done"}}]}` + "\n\n" +
		`data: [DONE]` + "\n\n"
	wrapped := NewChatStreamToResponsesStream(io.NopCloser(strings.NewReader(source)))
	defer wrapped.Close()

	events := drainStream(t, wrapped)
	items := completedOutputItems(t, events)
	if len(items) != 2 {
		t.Fatalf("expected function_call + message, got %d:\n%s", len(items), events)
	}
	if items[0]["type"] != "function_call" || items[1]["type"] != "message" {
		t.Fatalf("items must follow first-seen order, got %s then %s", items[0]["type"], items[1]["type"])
	}
	if itemContentText(items[1]) != "done" {
		t.Fatalf("message item must keep its text: %+v", items[1])
	}
}

// drainStream renders the whole reshaped body.
func drainStream(t *testing.T, wrapped *ChatStreamToResponsesStream) string {
	t.Helper()
	var out strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := wrapped.Read(buf)
		if n > 0 {
			out.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}
	return out.String()
}

func itemContentText(item map[string]any) string {
	content, _ := item["content"].([]any)
	var builder strings.Builder
	for _, raw := range content {
		part, _ := raw.(map[string]any)
		if part["type"] != "output_text" {
			continue
		}
		text, _ := part["text"].(string)
		builder.WriteString(text)
	}
	return builder.String()
}

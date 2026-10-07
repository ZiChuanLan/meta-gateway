package adapters

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestResponsesStandardArgumentsAndImplicitMessage(t *testing.T) {
	raw := []byte(`{"model":"m","input":[{"role":"user","content":"weather"},{"type":"function_call","call_id":"c","name":"weather","arguments":"{\"city\":\"Tokyo\"}","output":"wrong"},{"type":"function_call_output","call_id":"c","output":"sunny"}],"tools":[{"type":"function","name":"weather","strict":false}]}`)
	out, err := ResponsesToChat(raw)
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Messages []struct {
			Role      string
			ToolCalls []struct{ Function struct{ Arguments string } } `json:"tool_calls"`
		}
		Tools []struct{ Function struct{ Strict *bool } }
	}
	if err = json.Unmarshal(out, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Messages) != 3 || body.Messages[0].Role != "user" || body.Messages[1].ToolCalls[0].Function.Arguments != `{"city":"Tokyo"}` {
		t.Fatalf("lost standard input: %s", out)
	}
	if body.Tools[0].Function.Strict == nil || *body.Tools[0].Function.Strict {
		t.Fatalf("strict=false lost: %s", out)
	}
}

func TestResponsesImageInputIsNotSilentlyDropped(t *testing.T) {
	out, err := ResponsesToChat([]byte(`{"model":"m","input":[{"role":"user","content":[{"type":"input_text","text":"describe"},{"type":"input_image","image_url":"https://example.test/image.png","detail":"low"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Messages []struct {
			Content []struct {
				Type     string
				ImageURL struct{ URL, Detail string } `json:"image_url"`
			}
		}
	}
	if err = json.Unmarshal(out, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Messages[0].Content) != 2 || body.Messages[0].Content[1].ImageURL.URL != "https://example.test/image.png" || body.Messages[0].Content[1].ImageURL.Detail != "low" {
		t.Fatalf("image lost: %s", out)
	}
}

// State that a chat upstream cannot express at all is still refused: the stored
// conversation lives on the upstream that produced it, and a file reference is
// an id that upstream issued. There is nothing to forward.
func TestResponsesUnsupportedStateDoesNotProduceLossyChat(t *testing.T) {
	for _, raw := range []string{
		`{"model":"m","input":"next","previous_response_id":"resp-old"}`,
		`{"model":"m","input":"next","conversation":"conv-old"}`,
		`{"model":"m","input":"next","background":true}`,
		`{"model":"m","input":[{"role":"user","content":[{"type":"input_file","file_id":"file-1"}]}]}`,
	} {
		if _, err := ResponsesToChat([]byte(raw)); !errors.Is(err, ErrUnsupportedFeature) {
			t.Fatalf("lossy conversion allowed: %s %v", raw, err)
		}
	}
}

// A server-side tool has no chat/completions equivalent, but that is not a
// reason to fail the request: the tool is dropped and reported, the function
// tools survive, and the caller can say what was lost. Refusing instead is how a
// real client ends up with no service at all — Codex 0.155.1 sends web_search
// and a namespace tool with every turn, so the Responses→chat fallback could
// never run for it (production 2026-10-07: the upstream's 404 was passed through
// and the client retried every two seconds).
func TestResponsesDropsServerSideToolsAndReportsThem(t *testing.T) {
	raw := `{"model":"m","input":"next","tools":[` +
		`{"type":"function","name":"exec_command","parameters":{"type":"object"}},` +
		`{"type":"web_search"},` +
		`{"type":"namespace","name":"multi_agent_v1"},` +
		`{"type":"function","name":"view_image","parameters":{"type":"object"}}]}`
	converted, dropped, err := ResponsesToChatReport([]byte(raw))
	if err != nil {
		t.Fatalf("conversion refused: %v", err)
	}
	if len(dropped) != 2 || dropped[0] != "web_search" || dropped[1] != "namespace:multi_agent_v1" {
		t.Fatalf("dropped = %v, want web_search and namespace:multi_agent_v1", dropped)
	}
	var body struct {
		Tools []struct {
			Type     string
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(converted, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Tools) != 2 {
		t.Fatalf("chat tools = %+v, want the two function tools", body.Tools)
	}
	for _, tool := range body.Tools {
		if tool.Type != "function" || tool.Function.Name == "" {
			t.Fatalf("a non-function tool reached the chat body: %+v", tool)
		}
	}
	// The single-argument form stays the contract for callers that do not need
	// the report, and it converts the same way.
	if _, err := ResponsesToChat([]byte(raw)); err != nil {
		t.Fatalf("ResponsesToChat refused a droppable tool: %v", err)
	}
}

// tool_choice forcing a dropped tool is the one case that still refuses: the
// chat request would name a tool the upstream does not have.
func TestResponsesRefusesAForcedDroppedTool(t *testing.T) {
	raw := `{"model":"m","input":"next","tools":[{"type":"web_search"}],"tool_choice":{"type":"web_search"}}`
	if _, _, err := ResponsesToChatReport([]byte(raw)); !errors.Is(err, ErrUnsupportedFeature) {
		t.Fatalf("forced dropped tool returned %v, want ErrUnsupportedFeature", err)
	}
}

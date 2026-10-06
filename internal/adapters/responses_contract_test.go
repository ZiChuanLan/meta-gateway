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

func TestResponsesUnsupportedStateDoesNotProduceLossyChat(t *testing.T) {
	for _, raw := range []string{
		`{"model":"m","input":"next","previous_response_id":"resp-old"}`,
		`{"model":"m","input":"next","conversation":"conv-old"}`,
		`{"model":"m","input":"next","background":true}`,
		`{"model":"m","input":"next","tools":[{"type":"web_search"}]}`,
		`{"model":"m","input":[{"role":"user","content":[{"type":"input_file","file_id":"file-1"}]}]}`,
	} {
		if _, err := ResponsesToChat([]byte(raw)); !errors.Is(err, ErrUnsupportedFeature) {
			t.Fatalf("lossy conversion allowed: %s %v", raw, err)
		}
	}
}

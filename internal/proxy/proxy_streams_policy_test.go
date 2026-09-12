package proxy

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestRewriteStreamFlag(t *testing.T) {
	forced, ok := forceStreamRequestBody([]byte(`{"model":"m","stream":false,"messages":[]}`))
	if !ok {
		t.Fatal("valid body rejected")
	}
	var payload map[string]any
	if err := json.Unmarshal(forced, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["stream"] != true {
		t.Fatalf("stream not forced: %v", payload["stream"])
	}
	options, _ := payload["stream_options"].(map[string]any)
	if options["include_usage"] != true {
		t.Fatalf("include_usage missing: %v", options)
	}

	plain, ok := forceNonStreamRequestBody([]byte(`{"model":"m","stream":true,"stream_options":{"include_usage":true}}`))
	if !ok {
		t.Fatal("valid body rejected")
	}
	// A fresh map: json.Unmarshal merges into an existing map, so a reused
	// one would keep the stream_options key from the first case.
	var plainPayload map[string]any
	if err := json.Unmarshal(plain, &plainPayload); err != nil {
		t.Fatal(err)
	}
	if plainPayload["stream"] != false {
		t.Fatalf("stream not cleared: %v", plainPayload["stream"])
	}
	if _, has := plainPayload["stream_options"]; has {
		t.Fatal("stream_options must be dropped for non-stream requests")
	}

	// Undecodable body fails open.
	if _, ok := forceStreamRequestBody([]byte("not-json")); ok {
		t.Fatal("garbage body must fail open")
	}
}

const sampleStream = "data: {\"id\":\"c1\",\"created\":1,\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"},\"finish_reason\":null}]}\n\n" +
	"data: {\"id\":\"c1\",\"created\":1,\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hel\"},\"finish_reason\":null}]}\n\n" +
	"data: {\"id\":\"c1\",\"created\":1,\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"lo\"},\"finish_reason\":null}]}\n\n" +
	"data: {\"id\":\"c1\",\"created\":1,\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"f\",\"arguments\":\"{\\\"a\\\":\"}}]}}]}\n\n" +
	"data: {\"id\":\"c1\",\"created\":1,\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"1}\"}}]}}]}\n\n" +
	"data: {\"id\":\"c1\",\"created\":1,\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":7,\"total_tokens\":10}}\n\n" +
	"data: [DONE]\n\n"

func TestAggregateChatStream(t *testing.T) {
	raw, err := aggregateChatStream(strings.NewReader(sampleStream))
	if err != nil {
		t.Fatal(err)
	}
	var completion struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Choices []struct {
			Message struct {
				Role      string `json:"role"`
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
			FinishReason any `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			TotalTokens int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &completion); err != nil {
		t.Fatal(err)
	}
	if completion.ID != "c1" || completion.Object != "chat.completion" {
		t.Fatalf("header=%+v", completion)
	}
	msg := completion.Choices[0].Message
	if msg.Content != "Hello" {
		t.Fatalf("content=%q", msg.Content)
	}
	if msg.Role != "assistant" {
		t.Fatalf("role=%q", msg.Role)
	}
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].Function.Arguments != `{"a":1}` || msg.ToolCalls[0].Function.Name != "f" {
		t.Fatalf("tool_calls=%+v", msg.ToolCalls)
	}
	if completion.Choices[0].FinishReason != "tool_calls" {
		t.Fatalf("finish_reason=%v", completion.Choices[0].FinishReason)
	}
	if completion.Usage.TotalTokens != 10 {
		t.Fatalf("usage=%+v", completion.Usage)
	}
}

func TestAggregateChatStreamCRLF(t *testing.T) {
	crlf := strings.ReplaceAll("data: {\"id\":\"x\",\"created\":1,\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\r\n\r\ndata: [DONE]\r\n\r\n", "\n", "\r\n")
	raw, err := aggregateChatStream(strings.NewReader(crlf))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"content":"ok"`)) {
		t.Fatalf("content missing: %s", raw)
	}
}

func TestSynthesizeStreamFromCompletion(t *testing.T) {
	completion := []byte(`{"id":"c9","created":7,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"all at once"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`)
	raw, err := synthesizeStreamFromCompletion(completion)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	// Frames are map-marshaled (alphabetical keys); parse instead of matching
	// substrings so the assertion survives key ordering.
	frames := strings.Split(strings.TrimSuffix(text, "data: [DONE]\n\n"), "\n\n")
	if len(frames) != 3 {
		t.Fatalf("frame count=%d: %s", len(frames), text)
	}
	var first, final struct {
		Object  string `json:"object"`
		Choices []struct {
			Delta struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"delta"`
			FinishReason any `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			TotalTokens int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(frames[0], "data: ")), &first); err != nil {
		t.Fatal(err)
	}
	if first.Object != "chat.completion.chunk" || first.Choices[0].Delta.Content != "all at once" || first.Choices[0].Delta.Role != "assistant" {
		t.Fatalf("content frame=%+v", first)
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(frames[1], "data: ")), &final); err != nil {
		t.Fatal(err)
	}
	if final.Choices[0].FinishReason != "stop" || final.Usage.TotalTokens != 3 {
		t.Fatalf("finish frame=%+v", final)
	}
}

package adapters

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestChatToGeminiBasic(t *testing.T) {
	body := []byte(`{
		"model": "gemini-2.5-flash",
		"messages": [
			{"role": "system", "content": "You are helpful."},
			{"role": "user", "content": "Hello"},
			{"role": "assistant", "content": "Hi there"},
			{"role": "user", "content": "How are you?"}
		],
		"temperature": 0.5,
		"max_tokens": 128
	}`)
	path, converted, err := chatToGemini(body)
	if err != nil {
		t.Fatalf("chatToGemini: %v", err)
	}
	if path != "models/gemini-2.5-flash:generateContent" {
		t.Fatalf("path = %q", path)
	}
	var request geminiGenerateRequest
	if err := json.Unmarshal(converted, &request); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(request.Contents) != 3 {
		t.Fatalf("contents = %d, want 3 (system extracted)", len(request.Contents))
	}
	if request.Contents[0].Role != "user" || request.Contents[0].Parts[0].Text != "Hello" {
		t.Fatalf("contents[0] = %+v", request.Contents[0])
	}
	if request.Contents[1].Role != "model" || request.Contents[1].Parts[0].Text != "Hi there" {
		t.Fatalf("contents[1] = %+v", request.Contents[1])
	}
	if request.SystemInstruction == nil || request.SystemInstruction.Parts[0].Text != "You are helpful." {
		t.Fatalf("systemInstruction = %+v", request.SystemInstruction)
	}
	if request.GenerationConfig == nil || request.GenerationConfig.Temperature == nil ||
		*request.GenerationConfig.Temperature != 0.5 {
		t.Fatalf("generationConfig = %+v", request.GenerationConfig)
	}
	if request.GenerationConfig.MaxOutputTokens == nil || *request.GenerationConfig.MaxOutputTokens != 128 {
		t.Fatalf("maxOutputTokens = %+v", request.GenerationConfig.MaxOutputTokens)
	}
}

func TestChatToGeminiStreamingPath(t *testing.T) {
	body := []byte(`{"model":"gemini-2.5-pro","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	path, _, err := chatToGemini(body)
	if err != nil {
		t.Fatalf("chatToGemini: %v", err)
	}
	if !strings.HasPrefix(path, "models/gemini-2.5-pro:streamGenerateContent") {
		t.Fatalf("path = %q, want streamGenerateContent", path)
	}
}

func TestGeminiToChatResponse(t *testing.T) {
	body := []byte(`{
		"candidates": [{
			"content": {"role": "model", "parts": [{"text": "Hello!"}]},
			"finishReason": "STOP"
		}],
		"usageMetadata": {"promptTokenCount": 7, "candidatesTokenCount": 2, "totalTokenCount": 9},
		"modelVersion": "gemini-2.5-flash"
	}`)
	converted, err := geminiToChat(body)
	if err != nil {
		t.Fatalf("geminiToChat: %v", err)
	}
	var outbound struct {
		Object  string `json:"object"`
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(converted, &outbound); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if outbound.Object != "chat.completion" {
		t.Fatalf("object = %q", outbound.Object)
	}
	if len(outbound.Choices) != 1 {
		t.Fatalf("choices = %d", len(outbound.Choices))
	}
	if outbound.Choices[0].Message.Content != "Hello!" {
		t.Fatalf("content = %q", outbound.Choices[0].Message.Content)
	}
	if outbound.Choices[0].FinishReason != "stop" {
		t.Fatalf("finish_reason = %q", outbound.Choices[0].FinishReason)
	}
	if outbound.Usage.PromptTokens != 7 || outbound.Usage.CompletionTokens != 2 || outbound.Usage.TotalTokens != 9 {
		t.Fatalf("usage = %+v", outbound.Usage)
	}
}

func TestGeminiToChatContentFilter(t *testing.T) {
	body := []byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":""}]},"finishReason":"SAFETY"}]}`)
	converted, err := geminiToChat(body)
	if err != nil {
		t.Fatalf("geminiToChat: %v", err)
	}
	var outbound struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(converted, &outbound); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if outbound.Choices[0].FinishReason != "content_filter" {
		t.Fatalf("finish_reason = %q, want content_filter", outbound.Choices[0].FinishReason)
	}
}

func TestGeminiEmbeddingsConversion(t *testing.T) {
	body := []byte(`{"model":"text-embedding-004","input":["hello","world"]}`)
	path, converted, err := embeddingsToGemini(body)
	if err != nil {
		t.Fatalf("embeddingsToGemini: %v", err)
	}
	if path != "models/text-embedding-004:batchEmbedContents" {
		t.Fatalf("path = %q", path)
	}
	var request geminiBatchEmbedRequest
	if err := json.Unmarshal(converted, &request); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(request.Requests) != 2 {
		t.Fatalf("requests = %d", len(request.Requests))
	}

	response := []byte(`{"embeddings":[{"values":[0.1,0.2]},{"values":[0.3,0.4]}]}`)
	convertedResp, err := geminiToEmbeddings(response)
	if err != nil {
		t.Fatalf("geminiToEmbeddings: %v", err)
	}
	var outbound struct {
		Object string `json:"object"`
		Data   []struct {
			Object    string    `json:"object"`
			Index     int       `json:"index"`
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(convertedResp, &outbound); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if outbound.Object != "list" || len(outbound.Data) != 2 {
		t.Fatalf("outbound = %+v", outbound)
	}
	if outbound.Data[1].Index != 1 || len(outbound.Data[1].Embedding) != 2 {
		t.Fatalf("data[1] = %+v", outbound.Data[1])
	}
}

func TestGeminiToOpenAIStream(t *testing.T) {
	// Two SSE events: one text delta, then a final event with usage + finish.
	upstream := strings.NewReader(
		"data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"Hel\"}]}}]}\n\n" +
			"data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"lo\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":3,\"candidatesTokenCount\":2,\"totalTokenCount\":5}}\n\n",
	)
	stream := NewGeminiToOpenAIStream(io.NopCloser(upstream))
	raw, err := io.ReadAll(stream)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, `"delta":{"role":"assistant"}`) {
		t.Fatalf("missing role chunk: %s", text)
	}
	if !strings.Contains(text, `"delta":{"content":"Hel"}`) || !strings.Contains(text, `"delta":{"content":"lo"}`) {
		t.Fatalf("missing content deltas: %s", text)
	}
	if !strings.Contains(text, `"finish_reason":"stop"`) {
		t.Fatalf("missing finish_reason: %s", text)
	}
	if !strings.Contains(text, `"prompt_tokens":3`) || !strings.Contains(text, `"completion_tokens":2`) {
		t.Fatalf("missing usage: %s", text)
	}
	if !strings.Contains(text, "data: [DONE]") {
		t.Fatalf("missing [DONE]: %s", text)
	}
}

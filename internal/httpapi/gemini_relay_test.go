package httpapi_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lan/meta-gateway/internal/config"
	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/httpapi"
	"github.com/lan/meta-gateway/internal/store"
)

// mockGeminiUpstream simulates the native Gemini API (generateContent /
// streamGenerateContent / batchEmbedContents) so the full relay path can be
// tested end to end.
func mockGeminiUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-goog-api-key") == "" {
			http.Error(w, "missing api key", http.StatusUnauthorized)
			return
		}
		switch {
		case strings.Contains(r.URL.Path, ":streamGenerateContent"):
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, _ := w.(http.Flusher)
			fmt.Fprint(w, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"Hi\"}]}}]}\n\n")
			fmt.Fprint(w, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\" there\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":4,\"candidatesTokenCount\":2,\"totalTokenCount\":6}}\n\n")
			if flusher != nil {
				flusher.Flush()
			}
		case strings.Contains(r.URL.Path, ":batchEmbedContents"):
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"embeddings":[{"values":[0.1,0.2,0.3]}]}`)
		default: // generateContent
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"Hello from Gemini"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":9,"candidatesTokenCount":5,"totalTokenCount":14},"modelVersion":"gemini-2.5-flash"}`)
		}
	}))
}

// setupGeminiRelay boots a full gateway with a gemini channel wired to baseURL
// and returns the server URL plus a downstream relay token.
func setupGeminiRelay(t *testing.T, baseURL string) (string, string, int64) {
	return setupRelay(t, baseURL, "gemini")
}

func setupRelay(t *testing.T, baseURL, typeHint string) (string, string, int64) {
	t.Helper()
	dataDir := t.TempDir()
	db, err := store.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	enc, err := crypto.New("gemini-test-master-key-at-least-32-characters!")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{AdminToken: "admin-test", AdminTokens: []string{"admin-test"}, MetricsToken: "metrics-test", BackupDir: filepath.Join(dataDir, "backups"), MaxAdminBodyBytes: 1 << 20, AuditRetentionDays: 90, AuditRetentionRows: 100000, ExchangeAllowSecretExport: true, OutboundAllowCIDRs: []string{"127.0.0.1/32"}}
	server := httptest.NewServer(httpapi.New(cfg, db, enc))
	t.Cleanup(server.Close)

	var site struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/sites", map[string]any{
		"name": "gemini-site", "base_url": baseURL, "platform": "gemini", "status": "enabled",
	}), &site)

	var cred struct{ ID int64 }
	json.Unmarshal(post(t, fmt.Sprintf("%s/admin/sites/%d/credentials", server.URL, site.ID), map[string]any{
		"kind": "api_key", "secret": "test-gemini-key-abcdef", "status": "enabled",
	}), &cred)

	var channel struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/channels", map[string]any{
		"site_id": site.ID, "credential_id": cred.ID, "name": "relay-ch",
		"base_url": baseURL, "type_hint": typeHint, "status": "enabled",
	}), &channel)

	var route struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/routes", map[string]any{
		"model_pattern": "gemini-2.5-flash", "enabled": true,
	}), &route)
	post(t, fmt.Sprintf("%s/admin/routes/%d/members", server.URL, route.ID), map[string]any{
		"channel_id": channel.ID, "priority": 1, "weight": 100, "enabled": true,
	})

	var key struct{ Token string }
	json.Unmarshal(post(t, server.URL+"/admin/downstream-keys", map[string]any{
		"name": "test-key", "scopes": "relay",
	}), &key)
	return server.URL, key.Token, channel.ID
}

func post(t *testing.T, url string, payload any) []byte {
	t.Helper()
	encoded, _ := json.Marshal(payload)
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(encoded))
	req.Header.Set("Authorization", "Bearer admin-test")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		t.Fatalf("%s: status %d body %s", url, resp.StatusCode, body)
	}
	return body
}

func relayChat(t *testing.T, serverURL, token, body string) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, serverURL+"/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw
}

func TestGeminiRelayNonStream(t *testing.T) {
	upstream := mockGeminiUpstream(t)
	defer upstream.Close()
	serverURL, token, _ := setupGeminiRelay(t, upstream.URL)

	status, body := relayChat(t, serverURL, token, `{
		"model": "gemini-2.5-flash",
		"messages": [{"role": "user", "content": "hi"}],
		"max_tokens": 64
	}`)
	if status != http.StatusOK {
		t.Fatalf("status %d body %s", status, body)
	}
	var outbound struct {
		Object  string `json:"object"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &outbound); err != nil {
		t.Fatalf("decode: %v body %s", err, body)
	}
	if outbound.Object != "chat.completion" {
		t.Fatalf("object = %q", outbound.Object)
	}
	if len(outbound.Choices) == 0 || outbound.Choices[0].Message.Content != "Hello from Gemini" {
		t.Fatalf("choices = %+v", outbound.Choices)
	}
	if outbound.Usage.PromptTokens != 9 || outbound.Usage.CompletionTokens != 5 {
		t.Fatalf("usage = %+v", outbound.Usage)
	}
}

func TestGeminiRelayStream(t *testing.T) {
	upstream := mockGeminiUpstream(t)
	defer upstream.Close()
	serverURL, token, _ := setupGeminiRelay(t, upstream.URL)

	status, body := relayChat(t, serverURL, token, `{
		"model": "gemini-2.5-flash",
		"messages": [{"role": "user", "content": "hi"}],
		"stream": true
	}`)
	if status != http.StatusOK {
		t.Fatalf("status %d body %s", status, body)
	}
	text := string(body)
	for _, want := range []string{
		`"delta":{"role":"assistant"}`,
		`"delta":{"content":"Hi"}`,
		`"delta":{"content":" there"}`,
		`"finish_reason":"stop"`,
		`"prompt_tokens":4`,
		`"completion_tokens":2`,
		"data: [DONE]",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("stream missing %q in %s", want, text)
		}
	}
}

func TestGeminiRelayEmbeddings(t *testing.T) {
	upstream := mockGeminiUpstream(t)
	defer upstream.Close()
	serverURL, token, channelID := setupGeminiRelay(t, upstream.URL)

	// Route for the embedding model too.
	var route struct{ ID int64 }
	json.Unmarshal(post(t, serverURL+"/admin/routes", map[string]any{
		"model_pattern": "text-embedding-004", "enabled": true,
	}), &route)
	post(t, fmt.Sprintf("%s/admin/routes/%d/members", serverURL, route.ID), map[string]any{
		"channel_id": channelID, "priority": 1, "weight": 100, "enabled": true,
	})

	chatReq, _ := http.NewRequest(http.MethodPost, serverURL+"/v1/embeddings", strings.NewReader(`{
		"model": "text-embedding-004",
		"input": "hello"
	}`))
	chatReq.Header.Set("Authorization", "Bearer "+token)
	chatReq.Header.Set("Content-Type", "application/json")
	chatResp, err := http.DefaultClient.Do(chatReq)
	if err != nil {
		t.Fatal(err)
	}
	defer chatResp.Body.Close()
	body, _ := io.ReadAll(chatResp.Body)
	if chatResp.StatusCode != http.StatusOK {
		t.Fatalf("status %d body %s", chatResp.StatusCode, body)
	}
	var outbound struct {
		Object string `json:"object"`
		Data   []struct {
			Index     int       `json:"index"`
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &outbound); err != nil {
		t.Fatalf("decode: %v body %s", err, body)
	}
	if outbound.Object != "list" || len(outbound.Data) != 1 || len(outbound.Data[0].Embedding) != 3 {
		t.Fatalf("outbound = %+v", outbound)
	}
}

// mockOpenAIUpstream simulates an OpenAI-compatible upstream for the
// /v1/messages downstream translation test.
func mockOpenAIUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			http.Error(w, "no auth", http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var payload struct {
			Stream bool `json:"stream"`
		}
		_ = json.Unmarshal(body, &payload)
		if strings.Contains(r.URL.Path, "chat/completions") && payload.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"id\":\"chatcmpl-1\",\"model\":\"gpt-4o\",\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n")
			fmt.Fprint(w, "data: {\"id\":\"chatcmpl-1\",\"model\":\"gpt-4o\",\"choices\":[{\"delta\":{\"content\":\"Hi from GPT\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2}}\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"chatcmpl-1","object":"chat.completion","model":"gpt-4o","choices":[{"message":{"role":"assistant","content":"Hello from GPT"},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}}`)
	}))
}

func TestMessagesDownstreamOnOpenAIChannel(t *testing.T) {
	upstream := mockOpenAIUpstream(t)
	defer upstream.Close()
	serverURL, token, _ := setupRelay(t, upstream.URL, "openai-compatible")

	// The gemini channel type is irrelevant here; use the same helper to spin
	// up the gateway, then re-point nothing — route "gemini-2.5-flash" already
	// exists but the channel speaks OpenAI (mock). Send a native Anthropic
	// Messages request through /v1/messages.
	req, _ := http.NewRequest(http.MethodPost, serverURL+"/v1/messages", strings.NewReader(`{
		"model": "gemini-2.5-flash",
		"max_tokens": 64,
		"messages": [{"role": "user", "content": "hi"}]
	}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d body %s", resp.StatusCode, body)
	}
	var outbound struct {
		Type       string `json:"type"`
		Role       string `json:"role"`
		Content    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
		Usage      struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &outbound); err != nil {
		t.Fatalf("decode: %v body %s", err, body)
	}
	if outbound.Type != "message" || outbound.Role != "assistant" {
		t.Fatalf("outbound = %+v", outbound)
	}
	if len(outbound.Content) == 0 || outbound.Content[0].Text != "Hello from GPT" {
		t.Fatalf("content = %+v", outbound.Content)
	}
	if outbound.Usage.InputTokens != 7 || outbound.Usage.OutputTokens != 3 {
		t.Fatalf("usage = %+v", outbound.Usage)
	}
}

func TestMessagesDownstreamStreamOnOpenAIChannel(t *testing.T) {
	upstream := mockOpenAIUpstream(t)
	defer upstream.Close()
	serverURL, token, _ := setupRelay(t, upstream.URL, "openai-compatible")

	req, _ := http.NewRequest(http.MethodPost, serverURL+"/v1/messages", strings.NewReader(`{
		"model": "gemini-2.5-flash",
		"max_tokens": 64,
		"stream": true,
		"messages": [{"role": "user", "content": "hi"}]
	}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d body %s", resp.StatusCode, body)
	}
	text := string(body)
	for _, want := range []string{
		"event: message_start",
		"event: content_block_delta",
		`"text":"Hi from GPT"`,
		"event: message_delta",
		`"stop_reason":"end_turn"`,
		`"input_tokens":3`,
		`"output_tokens":2`,
		"event: message_stop",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("stream missing %q in %s", want, text)
		}
	}
}

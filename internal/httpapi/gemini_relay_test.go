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
	"sync"
	"testing"
	"time"

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

// setupRelayPair is setupRelay with a second channel on the same route:
// requests first try channel A then fail over to channel B. Both channels
// use the shared site/credential from baseURL A's site; channel B points at
// baseURLB directly.
func setupRelayPair(t *testing.T, baseURLA, baseURLB string) (string, string, int64) {
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
	cfg := &config.Config{AdminToken: "admin-test", AdminTokens: []string{"admin-test"}, MetricsToken: "metrics-test", BackupDir: filepath.Join(dataDir, "backups"), MaxAdminBodyBytes: 1 << 20, AuditRetentionDays: 90, AuditRetentionRows: 100000, ExchangeAllowSecretExport: true, OutboundAllowCIDRs: []string{"127.0.0.1/32"}, RetryTimes: 2}
	server := httptest.NewServer(httpapi.New(cfg, db, enc))
	t.Cleanup(server.Close)

	var site struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/sites", map[string]any{
		"name": "relay-site", "base_url": baseURLA, "platform": "openai-compatible", "status": "enabled",
	}), &site)

	var cred struct{ ID int64 }
	json.Unmarshal(post(t, fmt.Sprintf("%s/admin/sites/%d/credentials", server.URL, site.ID), map[string]any{
		"kind": "api_key", "secret": "test-key-abcdef", "status": "enabled",
	}), &cred)

	var channelA struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/channels", map[string]any{
		"site_id": site.ID, "credential_id": cred.ID, "name": "pair-a",
		"base_url": baseURLA, "type_hint": "openai-compatible", "status": "enabled",
	}), &channelA)
	var channelB struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/channels", map[string]any{
		"site_id": site.ID, "credential_id": cred.ID, "name": "pair-b",
		"base_url": baseURLB, "type_hint": "openai-compatible", "status": "enabled",
	}), &channelB)

	var route struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/routes", map[string]any{
		"model_pattern": "gemini-2.5-flash", "enabled": true, "retry_times": 2,
	}), &route)
	post(t, fmt.Sprintf("%s/admin/routes/%d/members", server.URL, route.ID), map[string]any{
		"channel_id": channelA.ID, "priority": 2, "weight": 100, "enabled": true,
	})
	post(t, fmt.Sprintf("%s/admin/routes/%d/members", server.URL, route.ID), map[string]any{
		"channel_id": channelB.ID, "priority": 1, "weight": 100, "enabled": true,
	})

	var key struct{ Token string }
	json.Unmarshal(post(t, server.URL+"/admin/downstream-keys", map[string]any{
		"name": "test-key", "scopes": "relay",
	}), &key)
	return server.URL, key.Token, channelA.ID
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
	logs := adminLogsGET(t, serverURL)
	if len(logs) == 0 {
		t.Fatal("no proxy logs")
	}
	if got := intField(t, logs[0], "prompt_tokens"); got != 9 {
		t.Fatalf("proxy log prompt_tokens=%d want 9", got)
	}
	if got := intField(t, logs[0], "completion_tokens"); got != 5 {
		t.Fatalf("proxy log completion_tokens=%d want 5", got)
	}
	if got := intField(t, logs[0], "total_tokens"); got != 14 {
		t.Fatalf("proxy log total_tokens=%d want 14", got)
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
		Type    string `json:"type"`
		Role    string `json:"role"`
		Content []struct {
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

// mockEchoUpstream returns the request body and headers it received so tests
// can assert overrides/injections end to end.
func mockEchoUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		response := map[string]any{
			"echo_headers": map[string]string{
				"x-channel-header": r.Header.Get("X-Channel-Header"),
				"user-agent":       r.Header.Get("User-Agent"),
			},
			"echo_body": string(body),
		}
		encoded, _ := json.Marshal(response)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(encoded)
	}))
}

func TestChannelOverridesEndToEnd(t *testing.T) {
	upstream := mockEchoUpstream(t)
	defer upstream.Close()
	serverURL, token, _ := setupRelay(t, upstream.URL, "openai-compatible")

	// Set header override + system prompt on the channel via admin API.
	channels := get(t, serverURL+"/admin/channels")
	var rows []struct {
		ID int64 `json:"id"`
	}
	json.Unmarshal(channels, &rows)
	if len(rows) == 0 {
		t.Fatal("no channels")
	}
	channelID := rows[0].ID
	put(t, serverURL+"/admin/channels/"+itoa(channelID), map[string]any{
		"header_override": `{"X-Channel-Header": "mg-test", "User-Agent": "MetaGateway/1.0"}`,
		"system_prompt":   "You are the relay gateway.",
	})

	status, body := relayChat(t, serverURL, token, `{"model":"gemini-2.5-flash","messages":[{"role":"user","content":"hi"}]}`)
	if status != http.StatusOK {
		t.Fatalf("status %d body %s", status, body)
	}
	var echo struct {
		Headers map[string]string `json:"echo_headers"`
		Body    string            `json:"echo_body"`
	}
	if err := json.Unmarshal(body, &echo); err != nil {
		t.Fatalf("decode echo: %v body %s", err, body)
	}
	if echo.Headers["x-channel-header"] != "mg-test" {
		t.Fatalf("header override missing: %+v", echo.Headers)
	}
	if echo.Headers["user-agent"] != "MetaGateway/1.0" {
		t.Fatalf("user-agent override missing: %+v", echo.Headers)
	}
	if !strings.Contains(echo.Body, `"role":"system"`) || !strings.Contains(echo.Body, "You are the relay gateway.") {
		t.Fatalf("system prompt not injected: %s", echo.Body)
	}
}

func get(t *testing.T, url string) []byte {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer admin-test")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		t.Fatalf("GET %s: %d %s", url, resp.StatusCode, body)
	}
	return body
}

func put(t *testing.T, url string, payload any) {
	t.Helper()
	encoded, _ := json.Marshal(payload)
	req, _ := http.NewRequest(http.MethodPut, url, strings.NewReader(string(encoded)))
	req.Header.Set("Authorization", "Bearer admin-test")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		t.Fatalf("PUT %s: %d %s", url, resp.StatusCode, body)
	}
}

// TestMessagesDownstreamOnGeminiChannel verifies the composed conversion
// chain end to end: a native Anthropic /v1/messages client served by a Gemini
// channel (Anthropic → OpenAI pivot → generateContent, and back).
func TestMessagesDownstreamOnGeminiChannel(t *testing.T) {
	geminiUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-goog-api-key") == "" {
			http.Error(w, "no key", http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(r.URL.Path, ":generateContent") {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		var req struct {
			Contents []map[string]any `json:"contents"`
		}
		_ = json.Unmarshal(body, &req)
		if len(req.Contents) == 0 {
			t.Fatalf("gemini contents missing in %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"candidates":[{"content":{"parts":[{"text":"Hello from Gemini"}],"role":"model"},"finishReason":"STOP"}],
			"usageMetadata":{"promptTokenCount":9,"candidatesTokenCount":4,"totalTokenCount":13}
		}`)
	}))
	defer geminiUpstream.Close()

	serverURL, token, _ := setupRelay(t, geminiUpstream.URL, "gemini")

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
		Type    string `json:"type"`
		Role    string `json:"role"`
		Content []struct {
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
	if len(outbound.Content) == 0 || outbound.Content[0].Text != "Hello from Gemini" {
		t.Fatalf("content = %+v", outbound.Content)
	}
	if outbound.StopReason != "end_turn" {
		t.Fatalf("stop_reason = %q", outbound.StopReason)
	}
	if outbound.Usage.InputTokens != 9 || outbound.Usage.OutputTokens != 4 {
		t.Fatalf("usage = %+v", outbound.Usage)
	}
}

// adminLogsGET fetches proxy logs with the admin token.
func adminLogsGET(t *testing.T, serverURL string) []map[string]any {
	t.Helper()
	return adminLogsGETLimit(t, serverURL, 0)
}

// adminLogsGETLimit fetches proxy logs with an explicit limit (0 = server default).
func adminLogsGETLimit(t *testing.T, serverURL string, limit int) []map[string]any {
	t.Helper()
	url := serverURL + "/admin/proxy-logs"
	if limit > 0 {
		url += fmt.Sprintf("?limit=%d", limit)
	}
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer admin-test")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var logs []map[string]any
	if err := json.Unmarshal(raw, &logs); err != nil {
		t.Fatalf("decode logs: %v body %s", err, raw)
	}
	return logs
}

func intField(t *testing.T, entry map[string]any, key string) int {
	t.Helper()
	switch v := entry[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	default:
		t.Fatalf("field %s not an int: %#v", key, entry[key])
		return 0
	}
}

// TestGeminiStreamCachePersistsToProxyLog proves the Gemini stream usage
// pipeline end to end: streamGenerateContent usageMetadata (with
// cachedContentTokenCount) must land as cache_read_tokens in the ProxyLog row.
func TestGeminiStreamCachePersistsToProxyLog(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-goog-api-key") == "" {
			http.Error(w, "missing api key", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"Hi\"}]}}]}\n\n")
		// Final event: usage with cache detail.
		fmt.Fprint(w, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\" there\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":4,\"candidatesTokenCount\":2,\"totalTokenCount\":6,\"cachedContentTokenCount\":7}}\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}))
	defer upstream.Close()

	serverURL, token, _ := setupRelay(t, upstream.URL, "gemini")
	status, body := relayChat(t, serverURL, token, `{
		"model": "gemini-2.5-flash",
		"messages": [{"role": "user", "content": "hi"}],
		"stream": true
	}`)
	if status != http.StatusOK {
		t.Fatalf("status %d body %s", status, body)
	}

	logs := adminLogsGET(t, serverURL)
	if len(logs) == 0 {
		t.Fatal("no proxy logs")
	}
	entry := logs[0]
	if got := intField(t, entry, "prompt_tokens"); got != 4 {
		t.Fatalf("prompt_tokens=%d want 4", got)
	}
	if got := intField(t, entry, "completion_tokens"); got != 2 {
		t.Fatalf("completion_tokens=%d want 2", got)
	}
	// Gemini cachedContentTokenCount must ride the conversion as cache_read_tokens.
	if got := intField(t, entry, "cache_read_tokens"); got != 7 {
		t.Fatalf("cache_read_tokens=%d want 7; entry=%#v", got, entry)
	}
}

// TestAnthropicStreamCachePersistsToProxyLog proves an Anthropic-native
// upstream stream (message_delta.usage carrying cache_read/cache_creation)
// served through the /v1/chat/completions relay persists the cache detail
// into the ProxyLog row.
func TestAnthropicStreamCachePersistsToProxyLog(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") == "" {
			http.Error(w, "missing api key", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		fmt.Fprint(w, "event: message_start\n")
		fmt.Fprint(w, `data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-3-5-sonnet","content":[]}}`+"\n\n")
		fmt.Fprint(w, "event: content_block_delta\n")
		fmt.Fprint(w, `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`+"\n\n")
		fmt.Fprint(w, "event: message_delta\n")
		fmt.Fprint(w, `data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":50,"output_tokens":20,"cache_read_input_tokens":12,"cache_creation_input_tokens":8}}`+"\n\n")
		fmt.Fprint(w, "event: message_stop\n")
		fmt.Fprint(w, `data: {"type":"message_stop"}`+"\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}))
	defer upstream.Close()

	serverURL, token, _ := setupRelay(t, upstream.URL, "anthropic")
	status, body := relayChat(t, serverURL, token, `{
		"model": "gemini-2.5-flash",
		"messages": [{"role": "user", "content": "hi"}],
		"stream": true
	}`)
	if status != http.StatusOK {
		t.Fatalf("status %d body %s", status, body)
	}

	logs := adminLogsGET(t, serverURL)
	if len(logs) == 0 {
		t.Fatal("no proxy logs")
	}
	entry := logs[0]
	if got := intField(t, entry, "prompt_tokens"); got != 50 {
		t.Fatalf("prompt_tokens=%d want 50", got)
	}
	if got := intField(t, entry, "completion_tokens"); got != 20 {
		t.Fatalf("completion_tokens=%d want 20", got)
	}
	if got := intField(t, entry, "cache_read_tokens"); got != 12 {
		t.Fatalf("cache_read_tokens=%d want 12; entry=%#v", got, entry)
	}
	if got := intField(t, entry, "cache_creation_tokens"); got != 8 {
		t.Fatalf("cache_creation_tokens=%d want 8; entry=%#v", got, entry)
	}
}

// TestStableFirstGraySplitAndPromotion runs the full relay path against a mock
// upstream with one stable + one stable_first channel and asserts (a) the gray
// channel receives ~1/N of traffic (N=5 over 100 requests → ≈20%), (b) after
// the promote threshold is reached the gray mark is cleared, and (c) the
// promoted channel subsequently receives a full traffic share.
func TestStableFirstGraySplitAndPromotion(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"c1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}}`)
	}))
	defer upstream.Close()

	// Wire site / credential / two channels / route / members / key.
	dataDir := t.TempDir()
	db, err := store.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	enc, err := crypto.New("stable-first-master-key-at-least-32-characters!")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{AdminToken: "admin-test", AdminTokens: []string{"admin-test"}, MetricsToken: "metrics-test", BackupDir: filepath.Join(dataDir, "backups"), MaxAdminBodyBytes: 1 << 20, AuditRetentionDays: 90, AuditRetentionRows: 100000, ExchangeAllowSecretExport: true, OutboundAllowCIDRs: []string{"127.0.0.1/32"}, StableFirstDenominator: 25, StableFirstPromoteRequests: 100, RoutingConcurrencyLimit: 64, WebhookThrottleSeconds: 300}
	server := httptest.NewServer(httpapi.New(cfg, db, enc))
	t.Cleanup(server.Close)

	var site struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/sites", map[string]any{
		"name": "sf-site", "base_url": upstream.URL, "platform": "openai-compatible", "status": "enabled",
	}), &site)
	var cred struct{ ID int64 }
	json.Unmarshal(post(t, fmt.Sprintf("%s/admin/sites/%d/credentials", server.URL, site.ID), map[string]any{
		"kind": "api_key", "secret": "sk-sf", "status": "enabled",
	}), &cred)
	var stable, gray struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/channels", map[string]any{
		"site_id": site.ID, "credential_id": cred.ID, "name": "stable-ch",
		"base_url": upstream.URL, "type_hint": "openai", "status": "enabled", "models_csv": "sf-test",
	}), &stable)
	json.Unmarshal(post(t, server.URL+"/admin/channels", map[string]any{
		"site_id": site.ID, "credential_id": cred.ID, "name": "gray-ch",
		"base_url": upstream.URL, "type_hint": "openai", "status": "enabled", "models_csv": "sf-test",
		"stable_first": true,
	}), &gray)
	createdGray := adminGET(t, server.URL, fmt.Sprintf("/admin/channels/%d", gray.ID))
	rawCreated, _ := json.Marshal(createdGray)
	t.Logf("created gray channel: %s", rawCreated)
	var route struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/routes", map[string]any{
		"model_pattern": "sf-test", "enabled": true,
	}), &route)
	post(t, fmt.Sprintf("%s/admin/routes/%d/members", server.URL, route.ID), map[string]any{
		"channel_id": stable.ID, "priority": 0, "weight": 100, "enabled": true,
	})
	post(t, fmt.Sprintf("%s/admin/routes/%d/members", server.URL, route.ID), map[string]any{
		"channel_id": gray.ID, "priority": 0, "weight": 100, "enabled": true,
	})
	var key struct{ Token string }
	json.Unmarshal(post(t, server.URL+"/admin/downstream-keys", map[string]any{
		"name": "sf-key", "scopes": "relay",
	}), &key)

	// Runtime: enable the gray pool with 1/5 split and a high promote bar so
	// the split measurement is not polluted by mid-run promotion.
	settings := adminGET(t, server.URL, "/admin/runtime-settings")
	ed := settings.(map[string]any)["editable"].(map[string]any)
	ed["stable_first_enabled"] = true
	ed["stable_first_denominator"] = 5
	ed["stable_first_promote_requests"] = 1000
	adminPut(t, server.URL, "/admin/runtime-settings", ed)

	relay := func() {
		status, body := relayChat(t, server.URL, key.Token, `{
			"model": "sf-test",
			"messages": [{"role": "user", "content": "hi"}]
		}`)
		if status != http.StatusOK {
			t.Fatalf("relay status %d body %s", status, body)
		}
	}

	// (a) 100 requests: gray channel must receive ≈ 1/5.
	for i := 0; i < 100; i++ {
		relay()
	}
	logs := adminLogsGET(t, server.URL)
	var stableCount, grayCount int
	for _, entry := range logs {
		switch intField(t, entry, "channel_id") {
		case int(stable.ID):
			stableCount++
		case int(gray.ID):
			grayCount++
		}
	}
	total := stableCount + grayCount
	if total < 90 {
		t.Fatalf("expected ~100 relay rows, got %d (stable=%d gray=%d)", total, stableCount, grayCount)
	}
	share := float64(grayCount) / float64(total)
	if share < 0.08 || share > 0.35 {
		t.Fatalf("gray share = %.2f (stable=%d gray=%d) want ≈ 0.20", share, stableCount, grayCount)
	}

	// (b) Lower the promote bar and keep relaying until the draw hits the gray
	// channel enough times to promote (RecordGraySuccess on 2xx).
	ed["stable_first_promote_requests"] = 3
	adminPut(t, server.URL, "/admin/runtime-settings", ed)
	for i := 0; i < 30; i++ {
		relay()
		logs = adminLogsGET(t, server.URL)
		for _, entry := range logs {
			if intField(t, entry, "channel_id") == int(gray.ID) && entry["cache_read_tokens"] == nil && entry["status"] != nil {
				// any row proves traffic flowed; promotion state checked below
			}
		}
	}
	// The gray channel must now be promoted: the stable_first mark cleared
	// (omitempty removes the field from JSON when false).
	channels := adminGET(t, server.URL, "/admin/channels")
	rawChannels, _ := json.Marshal(channels)
	t.Logf("channels after promotion window: %s", rawChannels)
	stillGray := false
	if arr, ok := channels.([]any); ok {
		for _, raw := range arr {
			ch := raw.(map[string]any)
			if int64(ch["id"].(float64)) == gray.ID {
				if sf, ok := ch["stable_first"].(bool); ok && sf {
					stillGray = true
				}
			}
		}
	}
	if stillGray {
		t.Fatalf("gray channel was not promoted after hitting promote threshold")
	}

	// (c) After promotion the channel serves a full share: 20 more requests and
	// both channels must see traffic. Use a full log window so the before/after
	// counts are not truncated by the API default limit of 100 rows.
	before := adminLogsGETLimit(t, server.URL, 500)
	beforeStable, beforeGray := 0, 0
	for _, entry := range before {
		switch intField(t, entry, "channel_id") {
		case int(stable.ID):
			beforeStable++
		case int(gray.ID):
			beforeGray++
		}
	}
	for i := 0; i < 20; i++ {
		relay()
	}
	after := adminLogsGETLimit(t, server.URL, 500)
	afterStable, afterGray := 0, 0
	for _, entry := range after {
		switch intField(t, entry, "channel_id") {
		case int(stable.ID):
			afterStable++
		case int(gray.ID):
			afterGray++
		}
	}
	if afterGray == beforeGray {
		t.Fatalf("promoted channel received no traffic after promotion (stable %d→%d, gray %d→%d)", beforeStable, afterStable, beforeGray, afterGray)
	}
}

// adminGET fetches an admin endpoint and decodes the JSON body.
func adminGET(t *testing.T, serverURL, path string) any {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, serverURL+path, nil)
	req.Header.Set("Authorization", "Bearer admin-test")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		t.Fatalf("GET %s: status %d body %s", path, resp.StatusCode, raw)
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode %s: %v body %s", path, err, raw)
	}
	return out
}

// adminPut updates an admin endpoint with a JSON body.
func adminPut(t *testing.T, serverURL, path string, body any) {
	t.Helper()
	encoded, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPut, serverURL+path, bytes.NewReader(encoded))
	req.Header.Set("Authorization", "Bearer admin-test")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		t.Fatalf("PUT %s: status %d body %s", path, resp.StatusCode, raw)
	}
}

// TestConcurrencyGuardSpreadsBurst verifies the burst guard end to end: with
// a slow upstream and a per-channel ceiling of 5, a 20-way concurrent burst
// must spread across both channels instead of piling onto the first pick.
func TestConcurrencyGuardSpreadsBurst(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(250 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"c1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}}`)
	}))
	defer slow.Close()

	dataDir := t.TempDir()
	db, err := store.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	enc, err := crypto.New("concurrency-guard-master-key-at-least-32-characters!")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{AdminToken: "admin-test", AdminTokens: []string{"admin-test"}, MetricsToken: "metrics-test", BackupDir: filepath.Join(dataDir, "backups"), MaxAdminBodyBytes: 1 << 20, AuditRetentionDays: 90, AuditRetentionRows: 100000, ExchangeAllowSecretExport: true, OutboundAllowCIDRs: []string{"127.0.0.1/32"}, StableFirstDenominator: 25, StableFirstPromoteRequests: 100, WebhookThrottleSeconds: 300}
	server := httptest.NewServer(httpapi.New(cfg, db, enc))
	t.Cleanup(server.Close)

	var site struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/sites", map[string]any{
		"name": "cg-site", "base_url": slow.URL, "platform": "openai-compatible", "status": "enabled",
	}), &site)
	var cred struct{ ID int64 }
	json.Unmarshal(post(t, fmt.Sprintf("%s/admin/sites/%d/credentials", server.URL, site.ID), map[string]any{
		"kind": "api_key", "secret": "sk-cg", "status": "enabled",
	}), &cred)
	var chA, chB struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/channels", map[string]any{
		"site_id": site.ID, "credential_id": cred.ID, "name": "cg-a",
		"base_url": slow.URL, "type_hint": "openai", "status": "enabled", "models_csv": "cg-test",
	}), &chA)
	json.Unmarshal(post(t, server.URL+"/admin/channels", map[string]any{
		"site_id": site.ID, "credential_id": cred.ID, "name": "cg-b",
		"base_url": slow.URL, "type_hint": "openai", "status": "enabled", "models_csv": "cg-test",
	}), &chB)
	var route struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/routes", map[string]any{
		"model_pattern": "cg-test", "enabled": true,
	}), &route)
	post(t, fmt.Sprintf("%s/admin/routes/%d/members", server.URL, route.ID), map[string]any{
		"channel_id": chA.ID, "priority": 0, "weight": 100, "enabled": true,
	})
	post(t, fmt.Sprintf("%s/admin/routes/%d/members", server.URL, route.ID), map[string]any{
		"channel_id": chB.ID, "priority": 0, "weight": 100, "enabled": true,
	})
	var key struct{ Token string }
	json.Unmarshal(post(t, server.URL+"/admin/downstream-keys", map[string]any{
		"name": "cg-key", "scopes": "relay",
	}), &key)

	// Enable the guard with a ceiling of 5.
	settings := adminGET(t, server.URL, "/admin/runtime-settings")
	ed := settings.(map[string]any)["editable"].(map[string]any)
	ed["routing_concurrency_enabled"] = true
	ed["routing_concurrency_limit"] = 5
	adminPut(t, server.URL, "/admin/runtime-settings", ed)

	body := `{"model":"cg-test","messages":[{"role":"user","content":"hi"}]}`
	send := func(wg *sync.WaitGroup) {
		defer wg.Done()
		req, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/chat/completions", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+key.Token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Errorf("relay: %v", err)
			return
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("relay status %d", resp.StatusCode)
		}
	}

	// Fire a 20-way concurrent burst; each request holds its channel slot for
	// ~250ms, so the guard must route the excess to the second channel.
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go send(&wg)
	}
	wg.Wait()

	logs := adminLogsGET(t, server.URL)
	var aCount, bCount int
	for _, entry := range logs {
		switch intField(t, entry, "channel_id") {
		case int(chA.ID):
			aCount++
		case int(chB.ID):
			bCount++
		}
	}
	t.Logf("burst distribution: channelA=%d channelB=%d", aCount, bCount)
	if aCount == 0 || bCount == 0 {
		t.Fatalf("burst did not spread: a=%d b=%d (single point of overload)", aCount, bCount)
	}
}

// TestWebhookNotifiesDisableAndRecovery drives the full operational loop: a
// failing upstream auto-disables the channel (webhook channel_disabled), the
// passive-recovery probe later restores it (webhook channel_recovered).
func TestWebhookNotifiesDisableAndRecovery(t *testing.T) {
	var receivedMu sync.Mutex
	var received []map[string]any
	hookServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var event map[string]any
		if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		receivedMu.Lock()
		received = append(received, event)
		receivedMu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer hookServer.Close()

	// Upstream: chat completions always fails; /v1/models answers (probe path).
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/v1/models"):
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"object":"list","data":[{"id":"wh-test","object":"model"}]}`)
		default:
			http.Error(w, `{"error":"boom"}`, http.StatusInternalServerError)
		}
	}))
	defer upstream.Close()

	dataDir := t.TempDir()
	db, err := store.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	enc, err := crypto.New("webhook-master-key-at-least-32-characters!")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		AdminToken: "admin-test", AdminTokens: []string{"admin-test"}, MetricsToken: "metrics-test",
		BackupDir: filepath.Join(dataDir, "backups"), MaxAdminBodyBytes: 1 << 20,
		AuditRetentionDays: 90, AuditRetentionRows: 100000, ExchangeAllowSecretExport: true,
		OutboundAllowCIDRs:     []string{"127.0.0.1/32"},
		StableFirstDenominator: 25, StableFirstPromoteRequests: 100, RoutingConcurrencyLimit: 64,
		WebhookURL: hookServer.URL, WebhookThrottleSeconds: 1,
		ChannelAutoDisableThreshold: 1,
		RecoveryProbeEnabled:        true, RecoveryProbeIntervalSeconds: 10,
	}
	server := httptest.NewServer(httpapi.New(cfg, db, enc))
	t.Cleanup(server.Close)

	var site struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/sites", map[string]any{
		"name": "wh-site", "base_url": upstream.URL, "platform": "openai-compatible", "status": "enabled",
	}), &site)
	var cred struct{ ID int64 }
	json.Unmarshal(post(t, fmt.Sprintf("%s/admin/sites/%d/credentials", server.URL, site.ID), map[string]any{
		"kind": "api_key", "secret": "sk-wh", "status": "enabled",
	}), &cred)
	var channel struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/channels", map[string]any{
		"site_id": site.ID, "credential_id": cred.ID, "name": "wh-ch",
		"base_url": upstream.URL, "type_hint": "openai", "status": "enabled", "models_csv": "wh-test",
	}), &channel)
	var route struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/routes", map[string]any{
		"model_pattern": "wh-test", "enabled": true,
	}), &route)
	post(t, fmt.Sprintf("%s/admin/routes/%d/members", server.URL, route.ID), map[string]any{
		"channel_id": channel.ID, "priority": 0, "weight": 100, "enabled": true,
	})
	var key struct{ Token string }
	json.Unmarshal(post(t, server.URL+"/admin/downstream-keys", map[string]any{
		"name": "wh-key", "scopes": "relay",
	}), &key)

	// One failing relay → auto-disable → channel_disabled webhook.
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/chat/completions", strings.NewReader(`{"model":"wh-test","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer "+key.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	// Wait for the disabled event (async delivery).
	deadline := time.Now().Add(8 * time.Second)
	for {
		receivedMu.Lock()
		disabled := false
		for _, event := range received {
			if event["event"] == "channel_disabled" {
				disabled = true
			}
		}
		receivedMu.Unlock()
		if disabled {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("channel_disabled webhook not received; got %d events", len(received))
		}
		time.Sleep(200 * time.Millisecond)
	}

	// Passive-recovery loop (10s interval) probes, restores, and notifies.
	deadline = time.Now().Add(30 * time.Second)
	for {
		receivedMu.Lock()
		recovered := false
		for _, event := range received {
			if event["event"] == "channel_recovered" {
				recovered = true
			}
		}
		receivedMu.Unlock()
		if recovered {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("channel_recovered webhook not received; events so far: %v", received)
		}
		time.Sleep(500 * time.Millisecond)
	}

	// The channel must actually be enabled again.
	channels := adminGET(t, server.URL, "/admin/channels")
	if arr, ok := channels.([]any); ok {
		for _, raw := range arr {
			ch := raw.(map[string]any)
			if int64(ch["id"].(float64)) == channel.ID && ch["status"] != "enabled" {
				t.Fatalf("channel not restored: %v", ch["status"])
			}
		}
	}
}

func TestAnthropicNonStreamUsagePersistsToProxyLog(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") == "" {
			http.Error(w, "missing api key", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"msg_1","type":"message","role":"assistant","model":"claude-3-5-sonnet","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":{"input_tokens":11,"output_tokens":4}}`)
	}))
	defer upstream.Close()

	serverURL, token, _ := setupRelay(t, upstream.URL, "anthropic")
	status, body := relayChat(t, serverURL, token, `{
		"model": "gemini-2.5-flash",
		"messages": [{"role": "user", "content": "hi"}]
	}`)
	if status != http.StatusOK {
		t.Fatalf("status %d body %s", status, body)
	}
	var outbound struct {
		Object string `json:"object"`
		Usage  struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &outbound); err != nil {
		t.Fatalf("decode: %v body %s", err, body)
	}
	if outbound.Object != "chat.completion" || outbound.Usage.PromptTokens != 11 || outbound.Usage.CompletionTokens != 4 {
		t.Fatalf("outbound = %+v", outbound)
	}
	logs := adminLogsGET(t, serverURL)
	if len(logs) == 0 {
		t.Fatal("no proxy logs")
	}
	if got := intField(t, logs[0], "prompt_tokens"); got != 11 {
		t.Fatalf("proxy log prompt_tokens=%d want 11", got)
	}
	if got := intField(t, logs[0], "completion_tokens"); got != 4 {
		t.Fatalf("proxy log completion_tokens=%d want 4", got)
	}
	if got := intField(t, logs[0], "total_tokens"); got != 15 {
		t.Fatalf("proxy log total_tokens=%d want 15", got)
	}
}

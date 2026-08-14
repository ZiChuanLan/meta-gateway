package httpapi_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestPayloadRulesRewriteAndFilter exercises the channel-level body rewrite
// chain end to end: a rule matching the model rewrites max_tokens before the
// upstream sees it, a filter rule blocks the request with 403, and channels
// without rules pass the body through byte-identical.
func TestPayloadRulesRewriteAndFilter(t *testing.T) {
	var received string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		received = string(raw)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"c1","object":"chat.completion","model":"gemini-2.5-flash","choices":[]}`)
	}))
	defer upstream.Close()

	serverURL, token, _ := setupRelay(t, upstream.URL, "openai")
	post := func() string {
		req, _ := http.NewRequest(http.MethodPost, serverURL+"/v1/chat/completions",
			strings.NewReader(`{"model":"gemini-2.5-flash","max_tokens":100,"messages":[{"role":"user","content":"hi"}]}`))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return string(body)
	}
	listChannels := func() []map[string]any {
		req, _ := http.NewRequest(http.MethodGet, serverURL+"/admin/channels", nil)
		req.Header.Set("Authorization", "Bearer admin-test")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out []map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	channelID := func() float64 {
		for _, c := range listChannels() {
			if c["name"] == "relay-ch" {
				return c["id"].(float64)
			}
		}
		t.Fatal("no relay-ch channel")
		return 0
	}

	// No rules: body passes through byte-identical.
	body := post()
	if body == "" || !strings.Contains(body, "choices") {
		t.Fatalf("baseline request failed: %s", body)
	}
	if !strings.Contains(received, `"max_tokens":100`) {
		t.Fatalf("baseline body altered: %s", received)
	}

	// Attach a rewrite rule (model glob + payload condition → set + delete).
	id := int64(channelID())
	rules := `[{"name":"cap","match":{"model":"gemini-*","payload":{"max_tokens":{"exists":true}}},"actions":[{"op":"set","path":"max_tokens","value":{"num":8000}},{"op":"delete","path":"messages.0.content"}]}]`
	put(t, fmt.Sprintf("%s/admin/channels/%d", serverURL, id), map[string]any{"payload_rules": rules})
	body = post()
	if !strings.Contains(received, `"max_tokens":8000`) {
		t.Fatalf("rewrite not applied: %s", received)
	}
	if strings.Contains(received, `"content":"hi"`) {
		t.Fatalf("delete not applied: %s", received)
	}

	// Invalid rules JSON is rejected by the admin endpoint.
	req, _ := http.NewRequest(http.MethodPut, fmt.Sprintf("%s/admin/channels/%d", serverURL, id),
		bytes.NewReader([]byte(`{"payload_rules":"{broken"}`)))
	req.Header.Set("Authorization", "Bearer admin-test")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid rules accepted: %d", resp.StatusCode)
	}

	// Filter rule: a request whose messages contain an image block is refused
	// with 403 before reaching the upstream.
	filterRules := `[{"name":"no-images","match":{"payload":{"messages.#.content.#.image_url":{"exists":true}}},"actions":[{"op":"filter","reason":"images blocked"}]}]`
	put(t, fmt.Sprintf("%s/admin/channels/%d", serverURL, id), map[string]any{"payload_rules": filterRules})
	imgReq, _ := http.NewRequest(http.MethodPost, serverURL+"/v1/chat/completions",
		strings.NewReader(`{"model":"gemini-2.5-flash","messages":[{"role":"user","content":[{"type":"text","text":"hi"},{"type":"image_url","image_url":{"url":"data:image/png;base64,xxx"}}]}]}`))
	imgReq.Header.Set("Authorization", "Bearer "+token)
	imgReq.Header.Set("Content-Type", "application/json")
	imgResp, err := http.DefaultClient.Do(imgReq)
	if err != nil {
		t.Fatal(err)
	}
	imgBody, _ := io.ReadAll(imgResp.Body)
	imgResp.Body.Close()
	if imgResp.StatusCode != http.StatusForbidden {
		t.Fatalf("filter status = %d body=%s", imgResp.StatusCode, imgBody)
	}
	if !strings.Contains(string(imgBody), "images blocked") {
		t.Fatalf("filter reason missing: %s", imgBody)
	}
	// The same channel still serves text-only requests.
	body = post()
	if body == "" || !strings.Contains(body, "choices") {
		t.Fatalf("post-filter text request failed: %s", body)
	}

	// Header condition end-to-end: the rule fires only when the client sends
	// the matching header (plumbed via proxy.Request.Headers from r.Header).
	headerRules := `[{"name":"cc-max","match":{"model":"gemini-*","header":{"X-Meta-Client":"claude-code"}},"actions":[{"op":"set","path":"max_tokens","value":{"num":1234}}]}]`
	put(t, fmt.Sprintf("%s/admin/channels/%d", serverURL, id), map[string]any{"payload_rules": headerRules})
	headered := func(header string) string {
		req, _ := http.NewRequest(http.MethodPost, serverURL+"/v1/chat/completions",
			strings.NewReader(`{"model":"gemini-2.5-flash","max_tokens":100,"messages":[{"role":"user","content":"hi"}]}`))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		if header != "" {
			req.Header.Set("X-Meta-Client", header)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		out, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return string(out)
	}
	withHeader := headered("claude-code/2.0.1")
	if !strings.Contains(received, `"max_tokens":1234`) {
		t.Fatalf("header rule not fired: %s", received)
	}
	withoutHeader := headered("")
	if !strings.Contains(received, `"max_tokens":100`) {
		t.Fatalf("header rule fired without header: %s", received)
	}
	_ = withHeader
	_ = withoutHeader

	// Clear rules → passthrough restored.
	put(t, fmt.Sprintf("%s/admin/channels/%d", serverURL, id), map[string]any{"payload_rules": ""})
	body = post()
	if !strings.Contains(received, `"max_tokens":100`) {
		t.Fatalf("cleared rules still rewriting: %s", received)
	}
}

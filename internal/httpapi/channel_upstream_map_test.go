package httpapi_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The custom-endpoint mapping feature, exercised end to end through the public
// relay: a non-OpenAI upstream served behind /v1/chat/completions via an endpoint
// override plus request/response field maps, configured through the admin API.
func TestChannelUpstreamMapEndToEnd(t *testing.T) {
	var receivedPath, receivedBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		receivedBody = string(raw)
		w.Header().Set("Content-Type", "application/json")
		// A non-OpenAI answer: the gateway must reshape it via the response map.
		fmt.Fprint(w, `{"model":"jev-1.13.0","answers":{"urgent":{"type":"noul","noul":0.95}},"usage":{"input_tokens":12,"output_tokens":3}}`)
	}))
	defer upstream.Close()

	serverURL, token, _ := setupRelay(t, upstream.URL, "openai")

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
	channelID := func() int64 {
		for _, c := range listChannels() {
			if c["name"] == "relay-ch" {
				return int64(c["id"].(float64))
			}
		}
		t.Fatal("no relay-ch channel")
		return 0
	}
	id := channelID()

	// Configure the mapping through the admin API.
	put(t, fmt.Sprintf("%s/admin/channels/%d", serverURL, id), map[string]any{
		"upstream_path_override": "systemone",
		"upstream_request_map": `[
			{"from":"messages.0.content","to":"state"},
			{"to":"model","value":{"str":"jev-latest"}},
			{"to":"questions.urgent","value":{"str":"Does this convey urgency?"}}
		]`,
		"upstream_response_map": `[
			{"to":"choices.0.message.content","template":"{answers.urgent.noul}"},
			{"to":"choices.0.message.role","value":{"str":"assistant"}},
			{"to":"object","value":{"str":"chat.completion"}},
			{"from":"model","to":"upstream_model","move":true}
		]`,
	})

	// The admin list must return the stored mapping (the edit drawer's source).
	stored := false
	for _, c := range listChannels() {
		if int64(c["id"].(float64)) != id {
			continue
		}
		if c["upstream_path_override"] != "systemone" ||
			!strings.Contains(fmt.Sprint(c["upstream_request_map"]), "questions.urgent") {
			t.Fatalf("admin list lost the mapping: %v", c)
		}
		stored = true
	}
	if !stored {
		t.Fatal("channel missing from the admin list")
	}

	// Relay a normal OpenAI chat request against the route setupRelay created.
	req, _ := http.NewRequest(http.MethodPost, serverURL+"/v1/chat/completions",
		strings.NewReader(`{"model":"gemini-2.5-flash","messages":[{"role":"user","content":"payouts failed"}]}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("relay status = %d body=%s", resp.StatusCode, body)
	}

	// 1. Endpoint rewritten (bare name lands in the conventional /v1 slot).
	if !strings.HasSuffix(receivedPath, "/v1/systemone") {
		t.Fatalf("upstream path = %q, want a …/v1/systemone suffix", receivedPath)
	}
	// 2. Outbound body converted to the upstream's protocol.
	var sent map[string]any
	if err := json.Unmarshal([]byte(receivedBody), &sent); err != nil {
		t.Fatalf("outbound body is not JSON: %v (%s)", err, receivedBody)
	}
	if sent["state"] != "payouts failed" || sent["model"] != "jev-latest" {
		t.Fatalf("outbound body = %s", receivedBody)
	}

	// 3. Inbound body converted back to an OpenAI completion the client can read.
	// Note the two forms in play: a template renders the upstream's numeric noul
	// as text (a raw copy would put a JSON number in `content`, which OpenAI
	// clients reject), while move:true relocates `model` and deletes the source.
	var got struct {
		Object        string `json:"object"`
		Model         string `json:"model"`
		UpstreamModel string `json:"upstream_model"`
		Choices       []struct {
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("response is not JSON: %v (%s)", err, body)
	}
	if got.Object != "chat.completion" || len(got.Choices) != 1 {
		t.Fatalf("response shape = %s", body)
	}
	if got.Choices[0].Message.Role != "assistant" || got.Choices[0].Message.Content != "0.95" {
		t.Fatalf("response message = %+v (%s)", got.Choices[0].Message, body)
	}
	if got.Model != "" || got.UpstreamModel != "jev-1.13.0" {
		t.Fatalf("move:true did not relocate model: model=%q upstream_model=%q", got.Model, got.UpstreamModel)
	}

	// 4. Invalid mappings are rejected with 400 before they can be stored.
	bad := []map[string]any{
		{"upstream_path_map": `["not-an-object"]`},
		{"upstream_request_map": `[{"to":"state"}]`},
		{"upstream_request_map": `[{"from":"a..b","to":"c"}]`},
		{"upstream_request_map": `[{"to":"a","template":"{a..b}"}]`},
		{"upstream_response_map": `[{"from":"a","to":"b","value":{"str":"x"}}]`},
	}
	for _, patch := range bad {
		req, _ := http.NewRequest(http.MethodPut, fmt.Sprintf("%s/admin/channels/%d", serverURL, id),
			strings.NewReader(mustJSON(t, patch)))
		req.Header.Set("Authorization", "Bearer admin-test")
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("patch %v accepted with %d", patch, resp.StatusCode)
		}
	}

	// 5. Clearing the mapping returns the channel to plain passthrough.
	put(t, fmt.Sprintf("%s/admin/channels/%d", serverURL, id), map[string]any{
		"upstream_path_override": "",
		"upstream_request_map":   "[]",
		"upstream_response_map":  "[]",
	})
	req, _ = http.NewRequest(http.MethodPost, serverURL+"/v1/chat/completions",
		strings.NewReader(`{"model":"gemini-2.5-flash","messages":[{"role":"user","content":"payouts failed"}]}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if !strings.HasSuffix(receivedPath, "/v1/chat/completions") {
		t.Fatalf("cleared mapping still rewrites the path: %q", receivedPath)
	}
	// The body must be the OpenAI shape again: the upstream envelope is gone and
	// the client-facing fields are back.
	if !strings.Contains(receivedBody, `"messages"`) {
		t.Fatalf("cleared mapping still rewrites the body: %s", receivedBody)
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

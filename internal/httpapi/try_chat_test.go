package httpapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// chatUpstream records what the admin probe actually put on the wire and can
// answer in any of the three shapes a real provider uses: a buffered
// completion, an SSE stream, or a rejected stream (JSON error body).
type chatUpstream struct {
	path    string
	body    []byte
	streams bool
}

func newChatUpstream(t *testing.T, captured *chatUpstream, mode string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		captured.path = r.URL.Path
		captured.body = body
		switch mode {
		case "stream":
			captured.streams = true
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			flusher, _ := w.(http.Flusher)
			for _, chunk := range []string{
				`data: {"choices":[{"delta":{"content":"Hel"}}]}` + "\n\n",
				`data: {"choices":[{"delta":{"content":"lo!"}}]}` + "\n\n",
				"data: [DONE]\n\n",
			} {
				_, _ = io.WriteString(w, chunk)
				if flusher != nil {
					flusher.Flush()
				}
			}
		case "stream-rejected":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"message":"provider rejected the stream"}}`)
		case "json-for-stream":
			// Asked for a stream, answered with a document: some aggregators do
			// this silently and the console must still show something.
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"buffered anyway"}}]}`)
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"pong"}}],"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`)
		}
	}))
}

func postTryChat(t *testing.T, serverURL string, payload map[string]any) (int, []byte, http.Header) {
	t.Helper()
	encoded, _ := json.Marshal(payload)
	req, _ := http.NewRequest(http.MethodPost, serverURL+"/admin/try/chat", bytes.NewReader(encoded))
	req.Header.Set("Authorization", "Bearer admin-test")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	return response.StatusCode, raw, response.Header
}

type sseFrame struct {
	event string
	data  string
}

// parseFrames is deliberately dumb: the upstream's own frames are relayed
// verbatim, so anything smarter would hide what the console really receives.
func parseFrames(raw []byte) []sseFrame {
	frames := make([]sseFrame, 0, 4)
	for _, block := range strings.Split(string(raw), "\n\n") {
		if strings.TrimSpace(block) == "" {
			continue
		}
		frame := sseFrame{}
		var data []string
		for _, line := range strings.Split(block, "\n") {
			switch {
			case strings.HasPrefix(line, "event:"):
				frame.event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			case strings.HasPrefix(line, "data:"):
				data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			}
		}
		frame.data = strings.Join(data, "\n")
		frames = append(frames, frame)
	}
	return frames
}

func upstreamPayload(t *testing.T, captured *chatUpstream) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(captured.body, &payload); err != nil {
		t.Fatalf("upstream body is not JSON: %v (%s)", err, captured.body)
	}
	return payload
}

func TestTryChatStreamRelaysProviderFramesWithMetaFirst(t *testing.T) {
	captured := &chatUpstream{}
	upstream := newChatUpstream(t, captured, "stream")
	defer upstream.Close()
	serverURL, _, _ := setupImageRelay(t, upstream.URL, "gpt-oss-playground")

	status, raw, header := postTryChat(t, serverURL, map[string]any{
		"model":  "gpt-oss-playground",
		"system": "be terse",
		"messages": []map[string]any{
			{"role": "user", "content": "first"},
			{"role": "assistant", "content": "ok"},
			{"role": "user", "content": "second"},
		},
		"temperature": 9,   // clamped to 2
		"top_p":       0.4, // passed through
		"max_tokens":  4096,
		"stream":      true,
	})
	if status != http.StatusOK {
		t.Fatalf("status = %d body=%s", status, raw)
	}
	if !strings.HasPrefix(header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("content type = %q", header.Get("Content-Type"))
	}
	if header.Get("X-Accel-Buffering") != "no" {
		t.Errorf("streaming headers missing: %v", header)
	}

	frames := parseFrames(raw)
	if len(frames) < 3 {
		t.Fatalf("too few frames: %#v", frames)
	}
	if frames[0].event != "meta" {
		t.Fatalf("first frame = %#v, want the routing meta", frames[0])
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(frames[0].data), &meta); err != nil {
		t.Fatalf("meta frame is not JSON: %v", err)
	}
	if meta["status"] != float64(200) || meta["model"] != "gpt-oss-playground" {
		t.Errorf("meta frame = %v", meta)
	}
	if meta["channel_name"] != "img-ch" {
		t.Errorf("meta frame lost the channel: %v", meta)
	}
	// The provider's own frames survive byte for byte in between.
	if !strings.Contains(frames[1].data, `"content":"Hel"`) || !strings.Contains(frames[2].data, `"content":"lo!"`) {
		t.Fatalf("provider frames were altered: %#v", frames[:3])
	}
	if last := frames[len(frames)-1]; last.event != "done" {
		t.Fatalf("last frame = %#v, want done", last)
	}

	if captured.path != "/v1/chat/completions" {
		t.Fatalf("upstream path = %s", captured.path)
	}
	if !captured.streams {
		t.Error("upstream was never asked to stream")
	}
	payload := upstreamPayload(t, captured)
	if payload["stream"] != true {
		t.Errorf("upstream stream flag = %v", payload["stream"])
	}
	if payload["max_tokens"] != float64(4096) {
		t.Errorf("max_tokens = %v", payload["max_tokens"])
	}
	if payload["temperature"] != float64(2) {
		t.Errorf("temperature = %v, want it clamped to 2", payload["temperature"])
	}
	if payload["top_p"] != 0.4 {
		t.Errorf("top_p = %v", payload["top_p"])
	}
	messages, _ := payload["messages"].([]any)
	if len(messages) != 4 {
		t.Fatalf("messages = %v", messages)
	}
	first, _ := messages[0].(map[string]any)
	if first["role"] != "system" || first["content"] != "be terse" {
		t.Errorf("system prompt is not first: %v", first)
	}
	var transcript []string
	for _, message := range messages[1:] {
		entry, _ := message.(map[string]any)
		transcript = append(transcript, entry["role"].(string)+":"+entry["content"].(string))
	}
	if strings.Join(transcript, ",") != "user:first,assistant:ok,user:second" {
		t.Errorf("transcript = %v", transcript)
	}
}

func TestTryChatKeepsTheBufferedShortcut(t *testing.T) {
	captured := &chatUpstream{}
	upstream := newChatUpstream(t, captured, "json")
	defer upstream.Close()
	serverURL, _, _ := setupImageRelay(t, upstream.URL, "gpt-oss-buffered")

	// The Models page probe has always sent a bare prompt; that shape must keep
	// working byte for byte, including the 128-token default.
	status, raw, _ := postTryChat(t, serverURL, map[string]any{
		"model": "gpt-oss-buffered", "prompt": "hi",
	})
	if status != http.StatusOK {
		t.Fatalf("status = %d body=%s", status, raw)
	}
	var out struct {
		Status    int    `json:"status"`
		LatencyMs int    `json:"latency_ms"`
		Model     string `json:"model"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.Status != 200 || out.Model != "gpt-oss-buffered" {
		t.Fatalf("payload = %s", raw)
	}
	payload := upstreamPayload(t, captured)
	if payload["stream"] != false {
		t.Errorf("stream = %v", payload["stream"])
	}
	if payload["max_tokens"] != float64(128) {
		t.Errorf("max_tokens = %v", payload["max_tokens"])
	}
	if _, supplied := payload["temperature"]; supplied {
		t.Error("a temperature the operator never asked for reached the upstream")
	}
	messages, _ := payload["messages"].([]any)
	if len(messages) != 1 {
		t.Fatalf("messages = %v", messages)
	}
	entry, _ := messages[0].(map[string]any)
	if entry["role"] != "user" || entry["content"] != "hi" {
		t.Errorf("message = %v", entry)
	}
}

func TestTryChatStreamTurnsProviderRejectionIntoAFrame(t *testing.T) {
	captured := &chatUpstream{}
	upstream := newChatUpstream(t, captured, "stream-rejected")
	defer upstream.Close()
	serverURL, _, _ := setupImageRelay(t, upstream.URL, "gpt-oss-rejected")

	status, raw, header := postTryChat(t, serverURL, map[string]any{
		"model": "gpt-oss-rejected", "prompt": "hi", "stream": true,
	})
	// The transport succeeded, so the HTTP status stays 200 — the console reads
	// the provider's status out of the meta frame instead of a failed fetch.
	if status != http.StatusOK {
		t.Fatalf("status = %d body=%s", status, raw)
	}
	if !strings.HasPrefix(header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("content type = %q", header.Get("Content-Type"))
	}
	frames := parseFrames(raw)
	if frames[0].event != "meta" {
		t.Fatalf("first frame = %#v", frames[0])
	}
	var meta map[string]any
	_ = json.Unmarshal([]byte(frames[0].data), &meta)
	if meta["status"] != float64(400) {
		t.Errorf("meta status = %v, want 400", meta["status"])
	}
	if len(frames) < 3 || frames[1].event != "error" {
		t.Fatalf("frames = %#v", frames)
	}
	if !strings.Contains(frames[1].data, "provider rejected the stream") {
		t.Errorf("error frame lost the provider message: %q", frames[1].data)
	}
	if frames[len(frames)-1].event != "done" {
		t.Errorf("stream did not terminate: %#v", frames[len(frames)-1])
	}
}

// A provider that answers a stream request with a plain document is a silent
// failure mode for a console: the browser sees HTTP 200 and nothing arrives.
// The relay's silent-stream guard catches it, and the probe must forward that
// explanation instead of flattening it to a category.
func TestTryChatReportsASilentStreamInsteadOfAnEmptyReply(t *testing.T) {
	captured := &chatUpstream{}
	upstream := newChatUpstream(t, captured, "json-for-stream")
	defer upstream.Close()
	serverURL, _, _ := setupImageRelay(t, upstream.URL, "gpt-oss-json")

	status, raw, _ := postTryChat(t, serverURL, map[string]any{
		"model": "gpt-oss-json", "prompt": "hi", "stream": true,
	})
	if status != http.StatusBadGateway {
		t.Fatalf("status = %d body=%s", status, raw)
	}
	if !strings.Contains(string(raw), "stream ended silently") {
		t.Fatalf("the operator is not told why the reply was empty: %s", raw)
	}
}

func TestTryChatRejectsRolesItCannotForward(t *testing.T) {
	captured := &chatUpstream{}
	upstream := newChatUpstream(t, captured, "json")
	defer upstream.Close()
	serverURL, _, _ := setupImageRelay(t, upstream.URL, "gpt-oss-roles")

	status, raw, _ := postTryChat(t, serverURL, map[string]any{
		"model": "gpt-oss-roles",
		"messages": []map[string]any{
			{"role": "tool", "content": "{\"x\":1}"},
		},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", status, raw)
	}
	if captured.body != nil {
		t.Errorf("a rejected conversation still reached the upstream: %s", captured.body)
	}
}

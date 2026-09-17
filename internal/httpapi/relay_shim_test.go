package httpapi_test

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

const shimPNG = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8AAAwAB/AF+msuWAAAAAElFTkSuQmCC"

func postChat(t *testing.T, serverURL, token string, body map[string]any, stream bool) (int, []byte, http.Header) {
	t.Helper()
	encoded, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, serverURL+"/v1/chat/completions", strings.NewReader(string(encoded)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw, resp.Header
}

func chatWithImage(prompt string) map[string]any {
	return map[string]any{
		"model": "grok-imagine-image-edit",
		"messages": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{"type": "text", "text": prompt},
					{"type": "image_url", "image_url": map[string]any{"url": shimPNG}},
				},
			},
		},
	}
}

// Default behaviour: the flag is off, so a chat request to an image editor is
// forwarded untouched and the upstream's refusal comes back as-is.
func TestImageEditShimOffByDefault(t *testing.T) {
	captured := &imageUpstream{}
	upstream := newImageUpstream(t, captured)
	defer upstream.Close()
	serverURL, token, _ := setupImageRelay(t, upstream.URL, "grok-imagine-image-edit")

	status, _, _ := postChat(t, serverURL, token, chatWithImage("make it purple"), false)
	// The upstream stub answers 200 for every path, so we assert on the path it
	// was actually asked for: no rewrite means chat/completions.
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	if captured.path != "/v1/chat/completions" {
		t.Fatalf("shim fired with the flag off: upstream path = %s", captured.path)
	}
}

// With the flag on, the same request reaches /v1/images/edits and the image
// comes back as a chat completion the client can render.
func TestImageEditShimRewritesChatToEdits(t *testing.T) {
	captured := &imageUpstream{}
	upstream := newImageUpstream(t, captured)
	defer upstream.Close()
	serverURL, token, routeID := setupImageRelay(t, upstream.URL, "grok-imagine-image-edit")

	var route struct {
		Route struct {
			ModelPattern  string `json:"model_pattern"`
			ImageEditShim bool   `json:"image_edit_shim"`
		} `json:"route"`
	}
	_ = route
	put(t, serverURL+"/admin/routes/"+strconv.FormatInt(routeID, 10), map[string]any{
		"model_pattern":   "grok-imagine-image-edit",
		"enabled":         true,
		"image_edit_shim": true,
	})

	status, body, header := postChat(t, serverURL, token, chatWithImage("make it purple"), false)
	if status != http.StatusOK {
		t.Fatalf("status = %d body=%s", status, body)
	}
	if captured.path != "/v1/images/edits" {
		t.Fatalf("upstream path = %s, want /v1/images/edits", captured.path)
	}
	if header.Get("X-Meta-Image-Shim") == "" {
		t.Error("shim should announce itself on the response")
	}
	var out struct {
		Choices []struct {
			Message struct{ Content string } `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("response is not a chat completion: %v (%s)", err, body)
	}
	if len(out.Choices) != 1 || !strings.Contains(out.Choices[0].Message.Content, "data:image/png;base64,QUFBQQ==") {
		t.Fatalf("chat content = %q", out.Choices[0].Message.Content)
	}
}

// A streaming client still gets SSE, not a bare JSON object.
func TestImageEditShimStreaming(t *testing.T) {
	captured := &imageUpstream{}
	upstream := newImageUpstream(t, captured)
	defer upstream.Close()
	serverURL, token, routeID := setupImageRelay(t, upstream.URL, "grok-imagine-image-edit")
	put(t, serverURL+"/admin/routes/"+strconv.FormatInt(routeID, 10), map[string]any{
		"model_pattern":   "grok-imagine-image-edit",
		"enabled":         true,
		"image_edit_shim": true,
	})

	body := chatWithImage("make it purple")
	body["stream"] = true
	status, raw, header := postChat(t, serverURL, token, body, true)
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	if !strings.HasPrefix(header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("stream content type = %q", header.Get("Content-Type"))
	}
	text := string(raw)
	if !strings.HasPrefix(text, "data: ") || !strings.Contains(text, "data: [DONE]") {
		t.Fatalf("not SSE: %q", text)
	}
	if !strings.Contains(text, "QUFBQQ==") {
		t.Errorf("streamed chunk missing the image: %q", text)
	}
}

func TestImageEditShimPreservesResponseContract(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		payload    string
		stream     bool
		wantStatus int
		wantTokens int
	}{
		{"json usage", 200, `{"data":[{"b64_json":"QUFBQQ=="}],"usage":{"input_tokens":17,"output_tokens":5,"total_tokens":22}}`, false, 200, 22},
		{"stream usage", 200, `{"data":[{"b64_json":"QUFBQQ=="}],"usage":{"input_tokens":17,"output_tokens":5,"total_tokens":22}}`, true, 200, 22},
		{"usage absent", 200, `{"data":[{"url":"https://images.example/result.png"}]}`, false, 200, 0},
		{"stream usage absent", 200, `{"data":[{"url":"https://images.example/result.png"}]}`, true, 200, 0},
		{"json rejection", 400, `{"error":{"message":"unsupported image"}}`, false, 400, 0},
		{"stream rejection", 400, `{"error":{"message":"unsupported image"}}`, true, 400, 0},
		{"upstream unavailable", 503, `{"error":{"message":"unavailable"}}`, true, 503, 0},
		{"empty image response", 200, `{"data":[]}`, false, 502, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.payload)
			}))
			defer upstream.Close()
			serverURL, token, routeID := setupImageRelay(t, upstream.URL, "grok-imagine-image-edit")
			put(t, serverURL+"/admin/routes/"+strconv.FormatInt(routeID, 10), map[string]any{
				"model_pattern": "grok-imagine-image-edit", "enabled": true, "image_edit_shim": true,
			})
			request := chatWithImage("make it purple")
			request["stream"] = tc.stream
			status, body, headers := postChat(t, serverURL, token, request, tc.stream)
			if status != tc.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", status, tc.wantStatus, body)
			}
			if calls.Load() != 1 {
				t.Fatalf("image request was submitted %d times", calls.Load())
			}
			if tc.status >= 400 {
				if string(body) != tc.payload || !strings.HasPrefix(headers.Get("Content-Type"), "application/json") {
					t.Fatalf("upstream error changed: content-type=%s body=%s", headers.Get("Content-Type"), body)
				}
				return
			}
			if tc.wantStatus >= 400 {
				return
			}
			if tc.wantTokens == 0 {
				if strings.Contains(string(body), `"usage"`) {
					t.Fatalf("invented usage in %s", body)
				}
				return
			}
			payloads := [][]byte{body}
			if tc.stream {
				payloads = nil
				for _, line := range strings.Split(string(body), "\n") {
					if strings.HasPrefix(line, "data: {") {
						payloads = append(payloads, []byte(strings.TrimPrefix(line, "data: ")))
					}
				}
			}
			foundUsage := false
			for _, payload := range payloads {
				var response struct {
					Usage *struct {
						Prompt     int `json:"prompt_tokens"`
						Completion int `json:"completion_tokens"`
						Total      int `json:"total_tokens"`
					} `json:"usage"`
				}
				if err := json.Unmarshal(payload, &response); err != nil {
					t.Fatal(err)
				}
				if response.Usage != nil {
					foundUsage = true
					if response.Usage.Prompt != 17 || response.Usage.Completion != 5 || response.Usage.Total != tc.wantTokens {
						t.Fatalf("usage changed: %+v", response.Usage)
					}
				}
			}
			if !foundUsage {
				t.Fatal("upstream usage was lost")
			}
		})
	}
}

func TestImageEditShimUsesEnabledWildcardRoute(t *testing.T) {
	captured := &imageUpstream{}
	upstream := newImageUpstream(t, captured)
	defer upstream.Close()
	serverURL, token, routeID := setupImageRelay(t, upstream.URL, "grok-imagine-image-edit")
	put(t, serverURL+"/admin/routes/"+strconv.FormatInt(routeID, 10), map[string]any{
		"model_pattern": "grok-*", "enabled": true, "image_edit_shim": true,
	})
	status, body, _ := postChat(t, serverURL, token, chatWithImage("make it purple"), false)
	if status != http.StatusOK || captured.path != "/v1/images/edits" {
		t.Fatalf("wildcard shim: status=%d path=%s body=%s", status, captured.path, body)
	}
}

// A text-only chat must never be rewritten, even with the flag on.
func TestImageEditShimIgnoresTextOnlyChat(t *testing.T) {
	captured := &imageUpstream{}
	upstream := newImageUpstream(t, captured)
	defer upstream.Close()
	serverURL, token, routeID := setupImageRelay(t, upstream.URL, "grok-imagine-image-edit")
	put(t, serverURL+"/admin/routes/"+strconv.FormatInt(routeID, 10), map[string]any{
		"model_pattern":   "grok-imagine-image-edit",
		"enabled":         true,
		"image_edit_shim": true,
	})

	_, _, _ = postChat(t, serverURL, token, map[string]any{
		"model":    "grok-imagine-image-edit",
		"messages": []map[string]any{{"role": "user", "content": "hello"}},
	}, false)
	if captured.path != "/v1/chat/completions" {
		t.Fatalf("text-only chat was rewritten: %s", captured.path)
	}
}

// Gemini-style image models already accept images over chat: rewriting would
// break a working path, so the shim must stand down.
func TestImageEditShimLeavesChatProtocolModelsAlone(t *testing.T) {
	captured := &imageUpstream{}
	upstream := newImageUpstream(t, captured)
	defer upstream.Close()
	serverURL, token, routeID := setupImageRelay(t, upstream.URL, "gemini-2.5-flash-image")
	put(t, serverURL+"/admin/routes/"+strconv.FormatInt(routeID, 10), map[string]any{
		"model_pattern":   "gemini-2.5-flash-image",
		"enabled":         true,
		"image_edit_shim": true,
	})

	_, _, _ = postChat(t, serverURL, token, map[string]any{
		"model": "gemini-2.5-flash-image",
		"messages": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{"type": "text", "text": "make it green"},
					{"type": "image_url", "image_url": map[string]any{"url": shimPNG}},
				},
			},
		},
	}, false)
	if captured.path != "/v1/chat/completions" {
		t.Fatalf("chat-protocol model was rewritten: %s", captured.path)
	}
}

func shimPNGBytes(t *testing.T) []byte {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(shimPNG, "data:image/png;base64,"))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// An upstream that answers with a link on its own origin would hand the chat
// client a picture it cannot open: the link points at the provider, not at the
// gateway the client is configured for (and carries no credential). The shim
// therefore embeds the bytes, and falls back to the link when the fetch fails.
func TestImageEditShimInlinesUpstreamImageLink(t *testing.T) {
	raw := shimPNGBytes(t)
	for _, tc := range []struct {
		name       string
		mediaFound bool
		wantInline bool
	}{
		{"media reachable", true, true},
		{"media unreachable", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var originURL string
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/out.png" {
					if !tc.mediaFound {
						http.NotFound(w, r)
						return
					}
					w.Header().Set("Content-Type", "image/png")
					_, _ = w.Write(raw)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"data":[{"url":"`+originURL+`/out.png"}]}`)
			}))
			defer origin.Close()
			originURL = origin.URL

			serverURL, token, routeID := setupImageRelay(t, origin.URL, "grok-imagine-image-edit")
			put(t, serverURL+"/admin/routes/"+strconv.FormatInt(routeID, 10), map[string]any{
				"model_pattern": "grok-imagine-image-edit", "enabled": true, "image_edit_shim": true,
			})

			status, body, _ := postChat(t, serverURL, token, chatWithImage("make it purple"), false)
			if status != http.StatusOK {
				t.Fatalf("status=%d body=%s", status, body)
			}
			var out struct {
				Choices []struct {
					Message struct{ Content string } `json:"message"`
				} `json:"choices"`
			}
			if err := json.Unmarshal(body, &out); err != nil || len(out.Choices) != 1 {
				t.Fatalf("not a chat completion: %v (%s)", err, body)
			}
			content := out.Choices[0].Message.Content
			if tc.wantInline {
				want := "data:image/png;base64," + base64.StdEncoding.EncodeToString(raw)
				if !strings.Contains(content, want) {
					t.Fatalf("image was not embedded: %q", content)
				}
				if strings.Contains(content, origin.URL) {
					t.Fatalf("client is still asked to reach the provider origin: %q", content)
				}
				return
			}
			if !strings.Contains(content, origin.URL+"/out.png") {
				t.Fatalf("fetch failure must keep the upstream link: %q", content)
			}
		})
	}
}

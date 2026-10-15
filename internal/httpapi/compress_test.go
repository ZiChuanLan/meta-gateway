package httpapi_test

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// gzippedBody reads a response whose request carried an explicit
// Accept-Encoding: gzip. The header is set by hand so Go's transport neither
// adds it nor silently decompresses: the encoding on the wire is the thing
// under test.
func gzippedBody(t *testing.T, response *http.Response) []byte {
	t.Helper()
	defer response.Body.Close()
	if encoding := response.Header.Get("Content-Encoding"); encoding != "gzip" {
		t.Fatalf("content-encoding = %q, want gzip", encoding)
	}
	reader, err := gzip.NewReader(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// The console reads whole tables (one site-observation page load is ~1.2 MB,
// refetched every minute) and is reached over links far thinner than the
// gateway's own, so admin JSON is compressed.
func TestAdminJSONIsCompressed(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	}))
	defer upstream.Close()
	serverURL, _, _ := setupImageRelay(t, upstream.URL, "gzip-probe")

	request, _ := http.NewRequest(http.MethodGet, serverURL+"/admin/mode", nil)
	request.Header.Set("Authorization", "Bearer admin-test")
	request.Header.Set("Accept-Encoding", "gzip")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	raw := gzippedBody(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.StatusCode, raw)
	}
	if !json.Valid(raw) {
		t.Fatalf("body did not survive the round trip: %s", raw)
	}
}

// A compressed SSE stream arrives in one burst when the stream ends, so the
// workbench stream and the live-trace page would both stall. The admin
// subtree's content-type allowlist is what keeps them raw.
func TestAdminStreamsAreUncompressed(t *testing.T) {
	captured := &chatUpstream{}
	upstream := newChatUpstream(t, captured, "stream")
	defer upstream.Close()
	serverURL, _, _ := setupImageRelay(t, upstream.URL, "gzip-stream")

	body, _ := json.Marshal(map[string]any{
		"model": "gzip-stream", "stream": true,
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	request, _ := http.NewRequest(http.MethodPost, serverURL+"/admin/try/chat", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer admin-test")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept-Encoding", "gzip")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if encoding := response.Header.Get("Content-Encoding"); encoding != "" {
		t.Fatalf("content-encoding = %q, want none: a compressed stream is buffered", encoding)
	}
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"content":"Hel"`) {
		t.Fatalf("stream did not arrive as SSE: %s", raw)
	}
}

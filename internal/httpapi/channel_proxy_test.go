package httpapi_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// A channel-level proxy must route the upstream request through the proxy
// server (the proxy sees the request and answers it), while a channel
// without a proxy keeps dialing the upstream directly.
func TestChannelProxyRoutesThroughProxy(t *testing.T) {
	var proxied atomic.Int64
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxied.Add(1)
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "through-proxy") {
			t.Errorf("proxy body missing marker: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"id":"c1","object":"chat.completion","model":"gemini-2.5-flash","choices":[]}`)
	}))
	defer proxy.Close()

	var direct atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		direct.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"id":"c2","object":"chat.completion","model":"gemini-2.5-flash","choices":[]}`)
	}))
	defer upstream.Close()
	serverURL, token, channelID := setupRelay(t, upstream.URL, "openai")
	// Point the channel at the proxy server (per-channel proxy_url).
	put(t, fmt.Sprintf("%s/admin/channels/%d", serverURL, channelID), map[string]any{
		"proxy_url": proxy.URL,
	})
	var ch struct {
		ProxyURL string `json:"proxy_url"`
	}
	json.Unmarshal(get(t, fmt.Sprintf("%s/admin/channels/%d", serverURL, channelID)), &ch)
	if ch.ProxyURL != proxy.URL {
		t.Fatalf("proxy_url not persisted: %q", ch.ProxyURL)
	}

	send := func(marker string) int {
		req, _ := http.NewRequest(http.MethodPost, serverURL+"/v1/chat/completions",
			strings.NewReader(fmt.Sprintf(`{"model":"gemini-2.5-flash","messages":[{"role":"user","content":%q}]}`, marker)))
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}

	// With the proxy configured the upstream must never see the request.
	if status := send("through-proxy"); status != 200 {
		t.Fatalf("proxied status=%d", status)
	}
	if proxied.Load() != 1 {
		t.Fatalf("proxy hits = %d, want 1", proxied.Load())
	}
	if direct.Load() != 0 {
		t.Fatalf("direct hits = %d, want 0", direct.Load())
	}

	// Clearing the proxy persists the empty value. (Go's transport reuses
	// keep-alive connections for up to the idle timeout, so an immediate
	// request may still ride the old proxied connection; direct routing is
	// exercised by every other relay test, which never configures a proxy.)
	put(t, fmt.Sprintf("%s/admin/channels/%d", serverURL, channelID), map[string]any{"proxy_url": ""})
	var cleared struct {
		ProxyURL string `json:"proxy_url"`
	}
	json.Unmarshal(get(t, fmt.Sprintf("%s/admin/channels/%d", serverURL, channelID)), &cleared)
	if cleared.ProxyURL != "" {
		t.Fatalf("proxy_url not cleared: %q", cleared.ProxyURL)
	}

	// An invalid proxy URL is rejected at save time.
	payload, _ := json.Marshal(map[string]any{"proxy_url": "socks5://bad:1080"})
	req, _ := http.NewRequest(http.MethodPut, fmt.Sprintf("%s/admin/channels/%d", serverURL, channelID), strings.NewReader(string(payload)))
	req.Header.Set("Authorization", "Bearer admin-test")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid proxy accepted: %d", resp.StatusCode)
	}
}

package httpapi_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestChannelMaxConcurrentQueues verifies the hard concurrency ceiling
// end-to-end: with max_concurrent=1 on a slow upstream, two concurrent
// requests both succeed (the second queues FIFO instead of being dropped),
// and the upstream never sees more than one in-flight request.
func TestChannelMaxConcurrentQueues(t *testing.T) {
	var inFlight atomic.Int64
	var maxInFlight atomic.Int64
	var hits atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := inFlight.Add(1)
		for {
			m := maxInFlight.Load()
			if cur <= m || maxInFlight.CompareAndSwap(m, cur) {
				break
			}
		}
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		// Slow upstream: hold the request so the second one queues.
		fmt.Fprintf(w, `{"id":"c1","object":"chat.completion","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
		inFlight.Add(-1)
	}))
	defer upstream.Close()

	serverURL, token, channelID := setupRelay(t, upstream.URL, "openai-compatible")
	_ = channelID

	// Set max_concurrent=1 on the channel.
	payload, _ := json.Marshal(map[string]any{"max_concurrent": 1})
	req, _ := http.NewRequest(http.MethodPut, serverURL+"/admin/channels/"+fmt.Sprint(channelID), bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer admin-test")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("set max_concurrent status=%d", resp.StatusCode)
	}

	send := func() int {
		body := `{"model":"gemini-2.5-flash","messages":[{"role":"user","content":"hi"}]}`
		req, _ := http.NewRequest(http.MethodPost, serverURL+"/v1/chat/completions", bytes.NewReader([]byte(body)))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return resp.StatusCode
	}

	// Fire two concurrent requests; both must succeed (queued, not dropped).
	var wg sync.WaitGroup
	statuses := make([]int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			statuses[n] = send()
		}(i)
	}
	wg.Wait()
	for i, s := range statuses {
		if s != 200 {
			t.Fatalf("request %d status=%d, want 200 (queued request must not be dropped)", i, s)
		}
	}
	if hits.Load() != 2 {
		t.Fatalf("upstream hits = %d, want 2", hits.Load())
	}
	if maxInFlight.Load() > 1 {
		t.Fatalf("upstream max in-flight = %d, want 1 (hard ceiling enforced)", maxInFlight.Load())
	}

	// Reset to unlimited.
	payload, _ = json.Marshal(map[string]any{"max_concurrent": 0})
	req, _ = http.NewRequest(http.MethodPut, serverURL+"/admin/channels/"+fmt.Sprint(channelID), bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer admin-test")
	req.Header.Set("Content-Type", "application/json")
	resp, _ = http.DefaultClient.Do(req)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
}

// TestChannelMaxConcurrentStreamLifetime verifies the audit finding #2 fix:
// the hard ceiling must cover the full stream lifetime, not just the header
// phase. With max_concurrent=1, a second streaming request queues until the
// first stream's body is fully consumed (closed), and the upstream never sees
// two concurrent streams.
func TestChannelMaxConcurrentStreamLifetime(t *testing.T) {
	var inFlight atomic.Int64
	var maxInFlight atomic.Int64
	var streams atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		stream := bytes.Contains(raw, []byte(`"stream":true`))
		cur := inFlight.Add(1)
		for {
			m := maxInFlight.Load()
			if cur <= m || maxInFlight.CompareAndSwap(m, cur) {
				break
			}
		}
		defer inFlight.Add(-1)
		if !stream {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"id":"c1","object":"chat.completion","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}]}`)
			return
		}
		streams.Add(1)
		// SSE stream held open for 600ms so the second request queues until
		// the first stream's body is fully consumed.
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"id\":\"s1\"}\n\n")
		flusher.Flush()
		time.Sleep(600 * time.Millisecond)
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer upstream.Close()

	serverURL, token, channelID := setupRelay(t, upstream.URL, "openai-compatible")
	_ = channelID

	payload, _ := json.Marshal(map[string]any{"max_concurrent": 1})
	req, _ := http.NewRequest(http.MethodPut, serverURL+"/admin/channels/"+fmt.Sprint(channelID), bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer admin-test")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("set max_concurrent status=%d", resp.StatusCode)
	}

	sendStream := func() (int, error) {
		body := `{"model":"gemini-2.5-flash","stream":true,"messages":[{"role":"user","content":"hi"}]}`
		req, _ := http.NewRequest(http.MethodPost, serverURL+"/v1/chat/completions", bytes.NewReader([]byte(body)))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close()
		// Consume the full stream (the gate slot is held until this body is
		// closed, which happens here).
		_, err = io.Copy(io.Discard, resp.Body)
		return resp.StatusCode, err
	}

	var wg sync.WaitGroup
	statuses := make([]int, 2)
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			statuses[n], errs[n] = sendStream()
		}(i)
	}
	wg.Wait()
	for i := 0; i < 2; i++ {
		if errs[i] != nil {
			t.Fatalf("request %d err=%v", i, errs[i])
		}
		if statuses[i] != 200 {
			t.Fatalf("request %d status=%d, want 200 (queued stream must not be dropped)", i, statuses[i])
		}
	}
	if streams.Load() != 2 {
		t.Fatalf("upstream streams = %d, want 2", streams.Load())
	}
	if maxInFlight.Load() > 1 {
		t.Fatalf("upstream max concurrent streams = %d, want 1 (ceiling covers stream lifetime)", maxInFlight.Load())
	}

	// Reset to unlimited.
	payload, _ = json.Marshal(map[string]any{"max_concurrent": 0})
	req, _ = http.NewRequest(http.MethodPut, serverURL+"/admin/channels/"+fmt.Sprint(channelID), bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer admin-test")
	req.Header.Set("Content-Type", "application/json")
	resp, _ = http.DefaultClient.Do(req)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
}

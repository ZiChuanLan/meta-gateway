package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/adapters"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/relay"
)

// force_stream + a non-streaming client falls between the two stall guards:
// the upstream IS streaming (so the non-stream deadline used to be skipped)
// while the client is NOT (so the peek + idleTimeoutBody wrapper never
// applies, those only wrap client streams). aggregateChatStream then buffers
// the upstream whole with nothing bounding it.
//
// EVERY channel of the route points at the stalling server, so no failover
// path can mask a hang: if the budget is missing, the call cannot return.
func TestForceStreamNonStreamClientBoundsStalledUpstream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		// 200 plus one role frame, then stall until the client gives up.
		_, _ = w.Write([]byte("data: {\"id\":\"c1\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"}}]}\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)

	service, db, _, _ := setupProxy(t, relay.NewWithClient(server.Client()))
	service.SetAdapterRegistry(adapters.NewRegistry(nil))
	service.nonStreamTimeout = 400 * time.Millisecond

	// Point every channel at the stalling server with force_stream set.
	channels, err := db.Channel.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(channels) == 0 {
		t.Fatal("fixture has no channels")
	}
	for i := range channels {
		channels[i].BaseURL = server.URL
		channels[i].StreamPolicy = domain.StreamPolicyForceStream
		if err := db.Channel.Update(&channels[i]); err != nil {
			t.Fatal(err)
		}
	}

	type outcome struct {
		elapsed time.Duration
		err     error
	}
	done := make(chan outcome, 1)
	go func() {
		start := time.Now()
		// Non-streaming client; the channel policy forces a streaming upstream,
		// so the reply is aggregated before the client can be answered.
		result := service.ChatCompletions(context.Background(), Request{
			RequestID:  "req-force-stream-stall",
			OpenAIPath: "chat/completions",
			Model:      "model",
			Body:       []byte(`{"model":"model","messages":[{"role":"user","content":"hi"}]}`),
		})
		done <- outcome{time.Since(start), result.Err}
	}()

	select {
	case got := <-done:
		if got.err == nil {
			t.Fatal("stalled upstream reported success")
		}
		// Two channels × the retry budget, each bounded by nonStreamTimeout.
		if got.elapsed > 20*time.Second {
			t.Fatalf("force_stream aggregation took %v: the budget is not bounding it", got.elapsed)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("force_stream aggregation never returned: a stalled upstream pins the relay goroutine")
	}
}

// Sanity anchor: force_stream still aggregates a healthy stream into a normal
// completion, so the bound above did not simply break the feature.
func TestForceStreamNonStreamClientAggregatesHealthyStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			"data: {\"id\":\"c1\",\"created\":1,\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"}}]}\n\n" +
				"data: {\"id\":\"c1\",\"created\":1,\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"},\"finish_reason\":\"stop\"}]}\n\n" +
				"data: [DONE]\n\n"))
	}))
	t.Cleanup(server.Close)

	service, db, _, _ := setupProxy(t, relay.NewWithClient(server.Client()))
	service.SetAdapterRegistry(adapters.NewRegistry(nil))
	service.nonStreamTimeout = 10 * time.Second

	channels, err := db.Channel.List()
	if err != nil {
		t.Fatal(err)
	}
	for i := range channels {
		channels[i].BaseURL = server.URL
		channels[i].StreamPolicy = domain.StreamPolicyForceStream
		if err := db.Channel.Update(&channels[i]); err != nil {
			t.Fatal(err)
		}
	}

	result := service.ChatCompletions(context.Background(), Request{
		RequestID:  "req-force-stream-ok",
		OpenAIPath: "chat/completions",
		Model:      "model",
		Body:       []byte(`{"model":"model","messages":[{"role":"user","content":"hi"}]}`),
	})
	if result.Err != nil {
		t.Fatalf("healthy force_stream aggregation failed: %v", result.Err)
	}
	body, err := readResponseBody(result.Body, preserveBodyReadLimit)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"hi"`) {
		t.Fatalf("aggregated completion lost content: %s", body)
	}
	if !strings.Contains(string(body), `"chat.completion"`) {
		t.Fatalf("client did not receive a non-streaming completion: %s", body)
	}
}

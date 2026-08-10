package proxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/adapters"
	"github.com/lan/meta-gateway/internal/relay"
)

// TestNonStreamRequestTimeoutBoundsStalledUpstream verifies a non-streaming
// attempt cannot be pinned by an upstream that answers headers and then never
// delivers a body: the per-attempt context deadline fires and the request
// fails promptly instead of hanging until the client disconnects.
func TestNonStreamRequestTimeoutBoundsStalledUpstream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)

	service, db, highMember, _ := setupProxy(t, relay.NewWithClient(server.Client()))
	service.SetAdapterRegistry(adapters.NewRegistry(nil))
	service.nonStreamTimeout = 300 * time.Millisecond

	member, err := db.RouteMember.GetByID(highMember)
	if err != nil {
		t.Fatal(err)
	}
	channel, err := db.Channel.GetByID(member.ChannelID)
	if err != nil {
		t.Fatal(err)
	}
	channel.BaseURL = server.URL
	if err := db.Channel.Update(channel); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	result := service.ChatCompletions(context.Background(), Request{
		RequestID: "req-nonstream-timeout",
		Model:     "model",
		Body:      []byte(`{"model":"model","messages":[{"role":"user","content":"hi"}]}`),
	})
	elapsed := time.Since(start)
	if result.Err == nil {
		t.Fatalf("expected timeout error, got result=%+v", result)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("stalled upstream held the goroutine for %v, want ~300ms", elapsed)
	}
}

// TestNonStreamRequestTimeoutDoesNotAffectHealthyUpstream verifies the timeout
// wrapper is transparent for normal fast responses.
func TestNonStreamRequestTimeoutDoesNotAffectHealthyUpstream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"hi"}}]}`)
	}))
	t.Cleanup(server.Close)

	service, db, highMember, _ := setupProxy(t, relay.NewWithClient(server.Client()))
	service.SetAdapterRegistry(adapters.NewRegistry(nil))
	service.nonStreamTimeout = 5 * time.Second

	member, err := db.RouteMember.GetByID(highMember)
	if err != nil {
		t.Fatal(err)
	}
	channel, err := db.Channel.GetByID(member.ChannelID)
	if err != nil {
		t.Fatal(err)
	}
	channel.BaseURL = server.URL
	if err := db.Channel.Update(channel); err != nil {
		t.Fatal(err)
	}

	result := service.ChatCompletions(context.Background(), Request{
		RequestID: "req-nonstream-healthy",
		Model:     "model",
		Body:      []byte(`{"model":"model","messages":[{"role":"user","content":"hi"}]}`),
	})
	if result.Err != nil {
		t.Fatalf("healthy upstream failed: %v", result.Err)
	}
	if result.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", result.StatusCode)
	}
	body, err := io.ReadAll(result.Body)
	if err != nil || !strings.Contains(string(body), "hi") {
		t.Fatalf("body=%q err=%v", body, err)
	}
}

package httpapi

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/lan/meta-gateway/internal/livetrace"
)

// lockedRecorder is an http.ResponseWriter + http.Flusher whose body is safe
// for the handler goroutine to keep writing while the test reads a snapshot.
type lockedRecorder struct {
	mu      sync.Mutex
	body    bytes.Buffer
	code    int
	headers http.Header
}

func (l *lockedRecorder) Header() http.Header { return l.headers }

func (l *lockedRecorder) WriteHeader(statusCode int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.code == 0 {
		l.code = statusCode
	}
}

func (l *lockedRecorder) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.code == 0 {
		l.code = http.StatusOK
	}
	return l.body.Write(p)
}

// Flush satisfies http.Flusher; the SSE handler requires it to stream.
func (l *lockedRecorder) Flush() {}

func (l *lockedRecorder) snapshot() (int, string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.code, l.body.String()
}

func newLockedRecorder() *lockedRecorder {
	return &lockedRecorder{headers: make(http.Header)}
}

func TestLiveTraceSSEAndInterrupt(t *testing.T) {
	registry := livetrace.New()
	handler := newLiveTraceHandler(registry)
	router := chi.NewRouter()
	handler.Register(router)

	// A request enters the registry (simulating a relay in flight).
	ctx, release, ok := registry.Begin(context.Background(), "req-live", "openai", "model")
	if !ok {
		t.Fatal("Begin failed")
	}
	defer release()

	// Interrupt endpoint cancels the watch context.
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/relay/live/req-live/interrupt", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("interrupt status=%d body=%s", rec.Code, rec.Body.String())
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("interrupt endpoint did not cancel the request context")
	}

	// SSE stream replays the (now terminal) state. The handler keeps writing
	// to the stream until the context is cancelled, so the recorder must be
	// safe for concurrent Write/Read.
	streamReq := httptest.NewRequest(http.MethodGet, "/relay/live", nil)
	streamCtx, streamCancel := context.WithCancel(streamReq.Context())
	defer streamCancel()
	streamReq = streamReq.WithContext(streamCtx)

	streamRec := newLockedRecorder()
	done := make(chan struct{})
	go func() {
		router.ServeHTTP(streamRec, streamReq)
		close(done)
	}()
	deadline := time.Now().Add(2 * time.Second)
	seen := false
	for time.Now().Before(deadline) {
		_, bodyNow := streamRec.snapshot()
		if strings.Contains(bodyNow, "req-live") {
			seen = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !seen {
		t.Fatal("SSE stream never replayed the request state")
	}
	streamCancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}
	code, body := streamRec.snapshot()
	if code != http.StatusOK {
		t.Fatalf("SSE stream status=%d", code)
	}
	if !strings.Contains(body, "event: request") || !strings.Contains(body, `"request_id":"req-live"`) {
		t.Fatalf("SSE stream missing request state:\n%s", body)
	}
	if !strings.Contains(body, `"status":"interrupted"`) {
		t.Fatalf("SSE stream missing interrupted state:\n%s", body)
	}
}

func TestLiveTraceInterruptMissing(t *testing.T) {
	registry := livetrace.New()
	handler := newLiveTraceHandler(registry)
	router := chi.NewRouter()
	handler.Register(router)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/relay/live/none/interrupt", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing interrupt status=%d", rec.Code)
	}
}

func TestLiveTraceHandlerNilRegistry(t *testing.T) {
	router := chi.NewRouter()
	newLiveTraceHandler(nil).Register(router)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/relay/live", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil registry stream status=%d", rec.Code)
	}
}

package httpapi

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/lan/meta-gateway/internal/livetrace"
)

// liveQueueSize mirrors livetrace's unexported per-subscriber queue capacity
// (streamBuffer). It is the threshold a retained snapshot must exceed to hit
// the outage: replay sends the snapshot while holding the registry mutex, so a
// queue smaller than the snapshot wedges the relay path.
const liveQueueSize = 32

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

// TestLiveTraceStreamSurvivesSnapshotLargerThanLiveQueue is the end-to-end
// guard for the 2026-09-11 production outage: opening the console's live tab
// after more than streamBuffer relay requests had been retained deadlocked the
// registry mutex on snapshot replay, which stalled every relay request so no
// model could answer. The SSE stream must deliver the whole snapshot, and the
// registry must stay usable afterwards.
func TestLiveTraceStreamSurvivesSnapshotLargerThanLiveQueue(t *testing.T) {
	registry := livetrace.New()
	// Retention keeps far more than the live queue, so the snapshot is the big one.
	const total = 60
	for i := 0; i < total; i++ {
		id := "req-" + strconv.Itoa(i)
		_, release, ok := registry.Begin(context.Background(), id, "openai", "m")
		if !ok {
			t.Fatalf("Begin(%s) failed", id)
		}
		registry.Finish(id, livetrace.StatusSuccess, "")
		release()
	}
	// pruneLocked caps retained history, so replay exactly what is retained.
	want := len(registry.Snapshot())
	if want <= liveQueueSize {
		t.Fatalf("snapshot %d must exceed the live queue %d to exercise the bug", want, liveQueueSize)
	}

	router := chi.NewRouter()
	newLiveTraceHandler(registry).Register(router)
	streamReq := httptest.NewRequest(http.MethodGet, "/relay/live", nil)
	streamCtx, streamCancel := context.WithCancel(streamReq.Context())
	defer streamCancel()

	rec := newLockedRecorder()
	done := make(chan struct{})
	go func() {
		router.ServeHTTP(rec, streamReq.WithContext(streamCtx))
		close(done)
	}()

	deadline := time.Now().Add(10 * time.Second)
	served := false
	for time.Now().Before(deadline) {
		_, body := rec.snapshot()
		if strings.Count(body, "event: request") >= want {
			served = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !served {
		_, body := rec.snapshot()
		t.Fatalf("SSE replayed %d/%d snapshot frames; relay path is wedged:\n%s",
			strings.Count(body, "event: request"), want, body)
	}

	// The relay path must still make progress: this is the call that stalled in
	// production (livetrace.Registry.Attempt from proxy ForwardWithMeta).
	relay := make(chan struct{})
	go func() {
		registry.Attempt("req-0", 2, "channel-x", "openai", "key")
		close(relay)
	}()
	select {
	case <-relay:
	case <-time.After(2 * time.Second):
		t.Fatal("relay Attempt blocked behind a live-trace subscriber")
	}

	streamCancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("SSE stream did not stop on client disconnect")
	}
}

// gatedRecorder blocks the SSE handler inside a write until released, so the
// subscriber's queue can be overflowed (which drops and closes the stream).
// The handler must then return instead of spinning on a closed channel
// delivering zero-value frames forever.
type gatedRecorder struct {
	*lockedRecorder
	gate    chan struct{}
	blocked chan struct{}
	once    sync.Once
	helper  *testing.T
}

func (g *gatedRecorder) Write(p []byte) (int, error) {
	g.once.Do(func() { close(g.blocked) })
	select {
	case <-g.gate:
	case <-time.After(10 * time.Second):
		g.helper.Error("gated write never released")
	}
	return g.lockedRecorder.Write(p)
}

func TestLiveTraceStreamEndsWhenSubscriberOverflows(t *testing.T) {
	registry := livetrace.New()
	router := chi.NewRouter()
	newLiveTraceHandler(registry).Register(router)

	// One in-flight request so the handler has a snapshot frame to emit, which
	// parks it inside the gated write below.
	_, release, ok := registry.Begin(context.Background(), "req-x", "openai", "m")
	if !ok {
		t.Fatal("Begin failed")
	}
	defer release()

	rec := &gatedRecorder{
		lockedRecorder: newLockedRecorder(),
		gate:           make(chan struct{}),
		blocked:        make(chan struct{}),
		helper:         t,
	}
	streamCtx, streamCancel := context.WithCancel(context.Background())
	defer streamCancel()

	done := make(chan struct{})
	go func() {
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/relay/live", nil).WithContext(streamCtx))
		close(done)
	}()
	select {
	case <-rec.blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("SSE handler never wrote a frame")
	}

	// Overflow the stalled subscriber's queue so the registry drops and closes
	// it. The handler must notice the close and return instead of replaying
	// zero-value frames forever.
	for i := 0; i < liveQueueSize*4; i++ {
		registry.Attempt("req-x", i, "channel", "openai", "key")
	}
	close(rec.gate)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("SSE handler spun on its closed subscriber channel instead of returning")
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

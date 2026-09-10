package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/lan/meta-gateway/internal/livetrace"
)

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

	// SSE stream replays the (now terminal) state.
	streamReq := httptest.NewRequest(http.MethodGet, "/relay/live", nil)
	streamCtx, streamCancel := context.WithCancel(streamReq.Context())
	defer streamCancel()
	streamReq = streamReq.WithContext(streamCtx)
	rec = httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		router.ServeHTTP(rec, streamReq)
		close(done)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(rec.Body.String(), "req-live") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	streamCancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}
	body := rec.Body.String()
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

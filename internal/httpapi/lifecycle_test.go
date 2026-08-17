package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestStopBackgroundInvokesRegisteredStoppers(t *testing.T) {
	var calls atomic.Int32
	RegisterStopper(func() { calls.Add(1) })
	RegisterStopper(func() { calls.Add(10) })
	StopBackground(context.Background())
	if got := calls.Load(); got != 11 {
		t.Fatalf("stoppers called %d times, want 11", got)
	}
	// Registry cleared: a second stop is a no-op.
	StopBackground(context.Background())
	if got := calls.Load(); got != 11 {
		t.Fatalf("second stop must be a no-op, calls=%d", got)
	}
}

func TestStopBackgroundBoundedByContext(t *testing.T) {
	var calls atomic.Int32
	blocker := make(chan struct{})
	RegisterStopper(func() { <-blocker }) // never returns
	RegisterStopper(func() { calls.Add(1) })
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled: the first (non-blocking) stopper still ran, the blocker does not hang
	StopBackground(ctx)
	if got := calls.Load(); got != 1 {
		t.Fatalf("calls=%d want 1", got)
	}
	close(blocker)
}

func TestStopBackgroundSignalsLaterStoppersWhenEarlierBlocks(t *testing.T) {
	var calls atomic.Int32
	blocker := make(chan struct{})
	RegisterStopper(func() { calls.Add(1) })
	// Reverse-order shutdown starts this blocker first; the fast stopper must
	// still be launched before the bounded wait returns.
	RegisterStopper(func() { <-blocker })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	StopBackground(ctx)
	if got := calls.Load(); got != 1 {
		t.Fatalf("later stopper calls=%d want 1", got)
	}
	close(blocker)
}

func TestSecurityHeaders(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/console/", nil)
	securityHeaders(inner).ServeHTTP(rec, req)
	for _, header := range []string{"X-Frame-Options", "X-Content-Type-Options", "Referrer-Policy", "Cache-Control"} {
		if rec.Header().Get(header) == "" {
			t.Fatalf("missing security header %s", header)
		}
	}
	if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Fatalf("X-Frame-Options=%q want DENY", got)
	}
}

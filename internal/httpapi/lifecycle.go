package httpapi

import (
	"context"
	"net/http"
	"sync"
	"time"
)

// Background lifecycle registry: router-owned goroutines (alert sweep, daily
// summary, health sweep, recovery loop) register a stop callback here so the
// server's shutdown path can halt them all before the database closes.
// Single-process model: one app instance per process, so a package-level
// registry is safe.
var (
	lifecycleMu   sync.Mutex
	lifecycleStop []func()
)

// RegisterStopper registers a stop callback invoked (in reverse order) by
// StopBackground.
func RegisterStopper(stop func()) {
	if stop == nil {
		return
	}
	lifecycleMu.Lock()
	lifecycleStop = append(lifecycleStop, stop)
	lifecycleMu.Unlock()
}

// StopBackground invokes every registered stop callback (once each, reverse
// order) and clears the registry. Idempotent. When ctx is already cancelled,
// fast stoppers (plain channel closes) still get a short grace window to
// finish; only genuinely stuck stoppers abort the drain.
func StopBackground(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	lifecycleMu.Lock()
	stops := lifecycleStop
	lifecycleStop = nil
	lifecycleMu.Unlock()
	if len(stops) == 0 {
		return
	}
	// Start every stopper before waiting. A single stuck component must not
	// prevent later components (which may own the database or HTTP server) from
	// receiving their shutdown signal.
	done := make(chan struct{})
	var wg sync.WaitGroup
	for i := len(stops) - 1; i >= 0; i-- {
		stop := stops[i]
		wg.Add(1)
		go func(callback func()) {
			defer wg.Done()
			callback()
		}(stop)
	}
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return
	case <-ctx.Done():
		// Give fast stoppers a short grace period even when the parent shutdown
		// context is already cancelled, while still bounding a broken stopper.
		timer := time.NewTimer(100 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-done:
		case <-timer.C:
		}
	}
}

// securityHeaders guards console/admin responses: no framing (clickjack),
// no MIME sniffing, strict referrer, and no caching of any page that could
// hold session data in the DOM.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

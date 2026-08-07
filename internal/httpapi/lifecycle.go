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
	lifecycleMu.Lock()
	stops := lifecycleStop
	lifecycleStop = nil
	lifecycleMu.Unlock()
	for i := len(stops) - 1; i >= 0; i-- {
		done := make(chan struct{})
		go func(stop func()) {
			defer close(done)
			stop()
		}(stops[i])
		select {
		case <-done:
		case <-ctx.Done():
			// Grace window: immediate stoppers finish even on a cancelled ctx.
			select {
			case <-done:
			case <-time.After(100 * time.Millisecond):
				return
			}
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

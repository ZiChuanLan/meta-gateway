// Admin live-trace surfaces: an SSE stream of in-flight relay requests and a
// manual interrupt endpoint. State lives only in memory (livetrace registry);
// durable audit stays in proxy_logs / decision snapshots.
package httpapi

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/lan/meta-gateway/internal/livetrace"
)

// liveTraceHandler serves GET /admin/relay/live (SSE) and
// POST /admin/relay/live/{request_id}/interrupt.
type liveTraceHandler struct {
	registry *livetrace.Registry
}

func newLiveTraceHandler(registry *livetrace.Registry) *liveTraceHandler {
	return &liveTraceHandler{registry: registry}
}

func (h *liveTraceHandler) Register(r chi.Router) {
	r.Get("/relay/live", h.stream)
	r.Post("/relay/live/{request_id}/interrupt", h.interrupt)
}

func (h *liveTraceHandler) stream(w http.ResponseWriter, r *http.Request) {
	if h.registry == nil {
		writeError(w, http.StatusServiceUnavailable, "live trace unavailable")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	updates := h.registry.Subscribe()
	defer h.registry.Unsubscribe(updates)

	// Heartbeat keeps proxies from idling the connection out during quiet
	// stretches; client disconnect ends the loop via the request context.
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	writeEvent := func(event string, payload any) bool {
		if _, err := w.Write([]byte("event: " + event + "\n")); err != nil {
			return false
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			return false
		}
		if _, err := w.Write([]byte("data: ")); err != nil {
			return false
		}
		if _, err := w.Write(encoded); err != nil {
			return false
		}
		if _, err := w.Write([]byte("\n\n")); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case update, ok := <-updates:
			if !ok {
				// Overflow dropped this subscriber and closed the stream (see
				// Registry.publishLocked). Returning lets the client reconnect
				// and re-receive the snapshot instead of spinning on a closed
				// channel delivering zero-value frames.
				return
			}
			if !writeEvent("request", update) {
				return
			}
		case <-heartbeat.C:
			if _, err := w.Write([]byte(": keepalive\n\n")); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (h *liveTraceHandler) interrupt(w http.ResponseWriter, r *http.Request) {
	requestID := chi.URLParam(r, "request_id")
	if requestID == "" {
		writeError(w, http.StatusBadRequest, "request_id is required")
		return
	}
	if h.registry == nil || !h.registry.Interrupt(requestID) {
		writeError(w, http.StatusNotFound, "request not in flight")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "interrupted", "request_id": requestID})
}

var _ = time.Second

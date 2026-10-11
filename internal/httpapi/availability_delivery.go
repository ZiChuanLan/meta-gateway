package httpapi

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"sync/atomic"

	"github.com/lan/meta-gateway/internal/proxy"
	"github.com/lan/meta-gateway/internal/relay"
	"github.com/lan/meta-gateway/internal/store"
	"github.com/lan/meta-gateway/internal/usage"
)

type observedWriter struct {
	http.ResponseWriter
	status   int
	writeErr error
}

func (w *observedWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *observedWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(data)
	if err != nil {
		w.writeErr = err
	}
	return n, err
}
func (w *observedWriter) Flush()                      { _ = http.NewResponseController(w.ResponseWriter).Flush() }
func (w *observedWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

type observedBody struct {
	io.ReadCloser
	readFailed atomic.Bool
}

func (b *observedBody) Read(data []byte) (int, error) {
	n, err := b.ReadCloser.Read(data)
	if err != nil && !errors.Is(err, io.EOF) {
		b.readFailed.Store(true)
	}
	return n, err
}

// Record final delivery separately from attempts: a recovered failover is one
// successful request, while a stream broken after HTTP 200 is not a success.
func (h *RelayHandler) writeObservedUpstreamResult(w http.ResponseWriter, ctx context.Context,
	req proxy.Request, meta *proxy.AttemptMeta, result *relay.Result, stream bool,
	onUsage func(usage.Tokens, int, int, int64), onProgress func(int64), onStreamError func(), estimate ...bool) {
	tracked := &observedWriter{ResponseWriter: w}
	var body *observedBody
	if result != nil && result.Body != nil {
		body = &observedBody{ReadCloser: result.Body}
		result.Body = body
	}
	defer func() {
		if h.db == nil || req.Probe || req.Diagnostic {
			return
		}
		outcome := "failed"
		if ctx != nil && errors.Is(ctx.Err(), context.Canceled) || tracked.writeErr != nil {
			outcome = "canceled"
		} else if tracked.status >= 200 && tracked.status < 300 && result != nil && result.Err == nil && (body != nil && !body.readFailed.Load()) {
			outcome = "ok"
		}
		routeID, memberID, channelID := int64(0), int64(0), int64(0)
		upstream := ""
		actualGroup := store.NormalizeMemberGroup(req.RouteGroup)
		if meta != nil {
			if meta.ResolvedModel != "" {
				req.Model = meta.ResolvedModel
			}
			routeID, memberID, channelID, upstream = meta.RouteID, meta.MemberID, meta.ChannelID, meta.UpstreamModel
			if meta.RouteGroup != "" {
				actualGroup = store.NormalizeMemberGroup(meta.RouteGroup)
			}

		}
		if routeID == 0 {
			if route, err := h.db.Route.GetByModel(req.Model); err == nil && route != nil {
				routeID = route.ID
			}
		}
		if err := h.db.RecordRouteRequest(store.RouteRequestOutcome{RequestID: req.RequestID,
			RouteID: routeID, MemberID: memberID, ChannelID: channelID, Model: req.Model,
			UpstreamModel: upstream, GroupName: actualGroup, Path: req.OpenAIPath, Outcome: outcome, Status: tracked.status}); err != nil {
			log.Printf("availability: final request: %v", err)
		}
		if meta != nil && meta.AttemptLogID > 0 {
			_, err := h.db.Exec(`UPDATE proxy_logs SET completion_outcome = ? WHERE id = ?`, outcome, meta.AttemptLogID)
			if err != nil {
				log.Printf("availability: attempt completion: %v", err)
			}
		}
	}()
	writeUpstreamResult(tracked, ctx, req.RequestID, result, stream, onUsage, onProgress, onStreamError, estimate...)
}

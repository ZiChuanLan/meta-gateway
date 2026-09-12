// Package livetrace keeps an in-memory, process-local view of relay requests
// in flight and streams state changes to the admin console over SSE. It is
// deliberately small: the durable audit surface stays in proxy_logs /
// decision snapshots; this package exists only for the live view and manual
// interrupt of a running upstream attempt.
package livetrace

import (
	"context"
	"sort"
	"sync"
	"time"
)

// Status is the lifecycle state of one relay request.
type Status string

const (
	// StatusRunning covers routing, credential resolution, and upstream wait.
	StatusRunning Status = "running"
	// StatusSuccess marks a fully copied response (stream included).
	StatusSuccess Status = "success"
	// StatusFailed marks a request that ended with an error.
	StatusFailed Status = "failed"
	// StatusCanceled marks a client disconnect.
	StatusCanceled Status = "canceled"
	// StatusInterrupted marks an operator interrupt.
	StatusInterrupted Status = "interrupted"
)

// Terminal reports whether the status is a final state.
func (s Status) Terminal() bool {
	switch s {
	case StatusSuccess, StatusFailed, StatusCanceled, StatusInterrupted:
		return true
	}
	return false
}

// Request is the observable view of one relay request.
type Request struct {
	RequestID     string    `json:"request_id"`
	Status        Status    `json:"status"`
	Protocol      string    `json:"protocol"`
	Model         string    `json:"model"`
	StartedAt     time.Time `json:"started_at"`
	DurationMs    int64     `json:"duration_ms"`
	Round         int       `json:"round"`
	TargetChannel string    `json:"target_channel,omitempty"`
	KeyName       string    `json:"key_name,omitempty"`
	Error         string    `json:"error,omitempty"`
	// Stream marks a streaming (SSE) transfer; ClientKey is the downstream
	// key name the request came in on. FirstByteMs/BytesWritten and the
	// token counts track the client-facing transfer as it progresses.
	Stream           bool   `json:"stream,omitempty"`
	ClientKey        string `json:"client_key,omitempty"`
	FirstByteMs      int64  `json:"first_byte_ms,omitempty"`
	BytesWritten     int64  `json:"bytes_written,omitempty"`
	PromptTokens     int    `json:"prompt_tokens,omitempty"`
	CompletionTokens int    `json:"completion_tokens,omitempty"`
	// RetryOf links a request to the operator-interrupted request it most
	// likely retries: same downstream key and model within the correlation
	// window. The gateway cannot stop clients from auto-retrying, but the
	// live view can at least say so.
	RetryOf string  `json:"retry_of,omitempty"`
	History []Round `json:"history,omitempty"`
}

// Round is one failover attempt in a request's channel chain.
type Round struct {
	Round   int    `json:"round"`
	Channel string `json:"channel"`
	Status  string `json:"status"` // running | ok | failed
	Error   string `json:"error,omitempty"`
}

const (
	maxFinished  = 50 // cap on retained finished requests.
	streamBuffer = 32 // per-subscriber non-blocking queue.
	// retryCorrelationWindow is how long after an interrupt a matching new
	// request (same client key + model) is labeled a probable retry.
	retryCorrelationWindow = 15 * time.Second
	// maxRecentInterrupts bounds the correlation table; interrupts are rare.
	maxRecentInterrupts = 64
)

// BeginMeta carries what the relay handler knows at request admission.
type BeginMeta struct {
	// Stream marks the request as a streaming (SSE) transfer.
	Stream bool
	// ClientKey is the authenticated downstream key name ("" if unknown).
	ClientKey string
}

// Registry is the process-local live state hub. The zero value is not usable;
// use New.
type Registry struct {
	mu      sync.Mutex
	reqs    map[string]*Request
	order   []string // insertion order (request IDs), newest last.
	wchs    map[chan Request]struct{}
	cancels map[string]context.CancelFunc
	// recentInterrupts feeds the client-retry correlation; capped and
	// time-windowed (see retryCorrelationWindow).
	recentInterrupts []interruptRecord
}

type interruptRecord struct {
	requestID string
	clientKey string
	model     string
	at        time.Time
}

// New builds an empty registry.
func New() *Registry {
	return &Registry{
		reqs:    make(map[string]*Request),
		wchs:    make(map[chan Request]struct{}),
		cancels: make(map[string]context.CancelFunc),
	}
}

// Begin registers a running request. The returned context is canceled when
// the client goes away or the operator interrupts, and the returned release
// must be called exactly once when the request settles (it also cancels the
// watch goroutine so it cannot race a later re-use of the request ID).
func (r *Registry) Begin(ctx context.Context, requestID, protocol, model string, meta BeginMeta) (watchCtx context.Context, release func(), ok bool) {
	r.mu.Lock()
	if _, exists := r.reqs[requestID]; exists {
		r.mu.Unlock()
		return ctx, func() {}, false
	}
	req := &Request{
		RequestID: requestID,
		Status:    StatusRunning,
		Protocol:  protocol,
		Model:     model,
		StartedAt: time.Now(),
		Stream:    meta.Stream,
		ClientKey: meta.ClientKey,
		RetryOf:   r.correlateRetryLocked(meta.ClientKey, model),
	}
	r.reqs[requestID] = req
	r.order = append(r.order, requestID)
	watchCtx, rawCancel := context.WithCancel(ctx)
	finishCancel := rawCancel
	r.cancels[requestID] = rawCancel
	// Publish immediately: a new request must appear the moment it enters the
	// gateway, not after routing finishes its first round.
	r.publishLocked(*req)
	r.mu.Unlock()

	done := make(chan struct{})
	var once sync.Once
	go func() {
		<-done
		r.mu.Lock()
		delete(r.cancels, requestID)
		finishCancel()
		r.mu.Unlock()
	}()
	go func() {
		select {
		case <-done:
			return
		case <-watchCtx.Done():
			r.finish(requestID, StatusCanceled, "client disconnected")
		}
	}()
	return watchCtx, func() {
		once.Do(func() {
			close(done)
			// Safety net: the handler returns only after the transfer path has
			// finalized the request (success with metrics, failure, cancel, or
			// interrupt). A row still running here means an unhandled exit —
			// settle it so the live view never shows a ghost.
			r.mu.Lock()
			if req := r.reqs[requestID]; req != nil && !req.Status.Terminal() {
				req.Status = StatusCanceled
				req.Error = "handler exited"
				req.DurationMs = time.Since(req.StartedAt).Milliseconds()
				r.publishLocked(*req)
				r.pruneLocked()
			}
			r.mu.Unlock()
		})
	}, true
}

// correlateRetryLocked matches a fresh request against recently interrupted
// ones (same client key and model) so the console can tell "the interrupt
// failed" apart from "the client auto-retried". Caller holds r.mu.
func (r *Registry) correlateRetryLocked(clientKey, model string) string {
	if clientKey == "" || len(r.recentInterrupts) == 0 {
		return ""
	}
	now := time.Now()
	retryOf := ""
	kept := r.recentInterrupts[:0]
	for _, rec := range r.recentInterrupts {
		if now.Sub(rec.at) > retryCorrelationWindow {
			continue
		}
		kept = append(kept, rec)
		if retryOf == "" && rec.clientKey == clientKey && rec.model == model {
			retryOf = rec.requestID
		}
	}
	r.recentInterrupts = kept
	return retryOf
}

// Attempt records the latest routing round for a request.
func (r *Registry) Attempt(requestID string, round int, channel, protocol, keyName string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	req := r.reqs[requestID]
	if req == nil || req.Status.Terminal() {
		return
	}
	req.Round = round
	req.TargetChannel = channel
	if protocol != "" {
		req.Protocol = protocol
	}
	req.KeyName = keyName
	req.DurationMs = time.Since(req.StartedAt).Milliseconds()
	if entry := r.roundEntryLocked(req, round); entry != nil {
		entry.Channel = channel
		entry.Status = "running"
		entry.Error = ""
	} else {
		req.History = append(req.History, Round{Round: round, Channel: channel, Status: "running"})
	}
	r.publishLocked(*req)
}

// RoundOutcome finalizes one chain entry when the relay moves off a channel
// (failed + brief category) or commits to it (ok). Unknown rounds are
// ignored: the chain is best-effort, never worth blocking on.
func (r *Registry) RoundOutcome(requestID string, round int, channel string, ok bool, category string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	req := r.reqs[requestID]
	if req == nil || req.Status.Terminal() {
		return
	}
	if entry := r.roundEntryLocked(req, round); entry != nil && entry.Status == "running" {
		if ok {
			entry.Status = "ok"
		} else {
			entry.Status = "failed"
			entry.Error = category
		}
		r.publishLocked(*req)
	}
}

func (r *Registry) roundEntryLocked(req *Request, round int) *Round {
	for i := range req.History {
		if req.History[i].Round == round {
			return &req.History[i]
		}
	}
	return nil
}

// Progress records client-facing transfer metrics for a streaming response.
// The caller throttles; every call publishes so the console watches the
// stream move instead of a frozen row.
func (r *Registry) Progress(requestID string, firstByteMs, bytesWritten int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	req := r.reqs[requestID]
	if req == nil || req.Status.Terminal() {
		return
	}
	if firstByteMs > 0 && req.FirstByteMs == 0 {
		req.FirstByteMs = firstByteMs
	}
	if bytesWritten > req.BytesWritten {
		req.BytesWritten = bytesWritten
	}
	req.DurationMs = time.Since(req.StartedAt).Milliseconds()
	r.publishLocked(*req)
}

// FinishSuccess closes a fully copied response with its transfer metrics.
func (r *Registry) FinishSuccess(requestID string, firstByteMs, bytesWritten int64, promptTokens, completionTokens int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	req := r.reqs[requestID]
	if req == nil || req.Status.Terminal() {
		return
	}
	if firstByteMs > 0 {
		req.FirstByteMs = firstByteMs
	}
	req.BytesWritten = bytesWritten
	req.PromptTokens = promptTokens
	req.CompletionTokens = completionTokens
	req.Status = StatusSuccess
	req.DurationMs = time.Since(req.StartedAt).Milliseconds()
	r.publishLocked(*req)
	r.pruneLocked()
}

// Finish records the final state.
func (r *Registry) Finish(requestID string, status Status, errText string) {
	r.finish(requestID, status, errText)
}

// Interrupt cancels an in-flight request, if one is registered. The
// terminal state is published before the cancel fires so the watcher (which
// races the cancellation) never mislabels it as a client disconnect.
func (r *Registry) Interrupt(requestID string) bool {
	r.mu.Lock()
	cancel, exists := r.cancels[requestID]
	req := r.reqs[requestID]
	if !exists || req == nil || req.Status.Terminal() {
		r.mu.Unlock()
		return false
	}
	req.Status = StatusInterrupted
	req.Error = "interrupted by operator"
	req.DurationMs = time.Since(req.StartedAt).Milliseconds()
	r.publishLocked(*req)
	// Remember the interrupt for client-retry correlation. Interrupts are
	// rare; the table stays tiny and entries expire with the window.
	r.recentInterrupts = append(r.recentInterrupts, interruptRecord{
		requestID: requestID,
		clientKey: req.ClientKey,
		model:     req.Model,
		at:        time.Now(),
	})
	if len(r.recentInterrupts) > maxRecentInterrupts {
		r.recentInterrupts = r.recentInterrupts[len(r.recentInterrupts)-maxRecentInterrupts:]
	}
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return true
}

func (r *Registry) finish(requestID string, status Status, errText string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	req := r.reqs[requestID]
	if req == nil || req.Status.Terminal() {
		return
	}
	req.Status = status
	req.Error = errText
	req.DurationMs = time.Since(req.StartedAt).Milliseconds()
	r.publishLocked(*req)
	r.pruneLocked()
}

// Snapshot lists requests newest-first.
func (r *Registry) Snapshot() []Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Request, 0, len(r.order))
	for _, id := range r.order {
		if req := r.reqs[id]; req != nil {
			out = append(out, *req)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].StartedAt.After(out[j].StartedAt)
	})
	return out
}

// Subscribe registers a state stream. The returned channel receives the full
// snapshot first (as individual updates), then live updates until the
// subscriber unsubscribes or overflows (overflow closes it).
//
// The queue is sized for the entire snapshot plus the same live headroom
// publishLocked tolerates, so replaying the snapshot can never block. The send
// below runs while r.mu is held, and the retained request list grows to
// maxFinished: a queue smaller than the snapshot would block the sender
// forever, hold r.mu forever, and with it stall every Attempt/Begin/Finish on
// the relay path.
func (r *Registry) Subscribe() chan Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	ch := make(chan Request, len(r.order)+streamBuffer)
	r.wchs[ch] = struct{}{}
	for _, id := range r.order {
		if req := r.reqs[id]; req != nil {
			ch <- *req
		}
	}
	return ch
}

// Unsubscribe removes and closes a stream.
func (r *Registry) Unsubscribe(ch chan Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.wchs[ch]; ok {
		delete(r.wchs, ch)
		close(ch)
	}
}

func (r *Registry) publishLocked(req Request) {
	for ch := range r.wchs {
		select {
		case ch <- req:
		default:
			// Subscriber too slow: drop it; it may resubscribe and re-receive
			// the snapshot.
			delete(r.wchs, ch)
			close(ch)
		}
	}
}

// pruneLocked drops finished requests beyond the retention cap.
func (r *Registry) pruneLocked() {
	finished := 0
	for _, id := range r.order {
		if req := r.reqs[id]; req != nil && req.Status.Terminal() {
			finished++
		}
	}
	for finished > maxFinished {
		for i, id := range r.order {
			if req := r.reqs[id]; req != nil && req.Status.Terminal() {
				delete(r.reqs, id)
				r.order = append(r.order[:i], r.order[i+1:]...)
				finished--
				break
			}
		}
	}
}

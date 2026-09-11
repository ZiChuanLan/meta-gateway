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
}

const (
	maxFinished  = 50 // cap on retained finished requests.
	streamBuffer = 32 // per-subscriber non-blocking queue.
)

// Registry is the process-local live state hub. The zero value is not usable;
// use New.
type Registry struct {
	mu      sync.Mutex
	reqs    map[string]*Request
	order   []string // insertion order (request IDs), newest last.
	wchs    map[chan Request]struct{}
	cancels map[string]context.CancelFunc
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
func (r *Registry) Begin(ctx context.Context, requestID, protocol, model string) (watchCtx context.Context, release func(), ok bool) {
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
	}
	r.reqs[requestID] = req
	r.order = append(r.order, requestID)
	watchCtx, rawCancel := context.WithCancel(ctx)
	finishCancel := rawCancel
	r.cancels[requestID] = rawCancel
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
	return watchCtx, func() { once.Do(func() { close(done) }) }, true
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
	r.publishLocked(*req)
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

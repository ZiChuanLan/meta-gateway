// Package probe runs real availability checks against (channel, model) pairs.
//
// A probe is a genuine chat completion — max_tokens is tiny, but it is a real
// upstream call, because only a real call proves a model actually answers.
// That cost is why probes are explicit, cancellable tasks rather than
// background traffic: the operator picks the scope, watches the progress, and
// can stop at any time.
//
// Probes travel the same routing/proxy path as /v1 so every upstream protocol
// (OpenAI, Anthropic, Gemini) is covered for free, but they are marked as
// probes so they never pollute health bookkeeping or the proxy log. Their
// outcome lands in probe_results and model_health; enough consecutive failures
// on a pair disable its route member, enough successes re-enable it.
package probe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/lan/meta-gateway/internal/proxy"
	"github.com/lan/meta-gateway/internal/relay"
	"github.com/lan/meta-gateway/internal/store"
)

// Defaults. Probes cost upstream tokens, so both are deliberately small.
const (
	DefaultMaxTokens   = 1
	DefaultConcurrency = 4
	DefaultTimeout     = 30 * time.Second

	MaxTokensCeiling   = 256
	ConcurrencyCeiling = 16

	// DefaultPrompt is the user message sent when the operator leaves the
	// prompt blank. Two characters are enough to prove the upstream answers.
	DefaultPrompt = "hi"

	// ErrorDetailLimit caps how much of an upstream error body we keep.
	ErrorDetailLimit = 256
)

// Pair is one probe target: a model name as clients see it, pinned to a channel.
type Pair struct {
	ChannelID int64  `json:"channel_id"`
	Model     string `json:"model"`
}

// Options is one probe run's configuration. A struct rather than positional
// parameters because scheduled runs and on-demand runs share it, and because
// Start would otherwise grow a parameter every time a knob is added.
type Options struct {
	// MaxTokens caps the reply. Small keeps the run cheap; raise it for
	// reasoning models, whose thinking pass needs room to finish.
	MaxTokens int
	// Concurrency is how many probes run at once.
	Concurrency int
	// Prompt is the user message sent upstream. Empty falls back to
	// DefaultPrompt.
	Prompt string
	// AutoDisableAfter is the consecutive-failure count at which the member is
	// disabled; 0 turns automatic disabling off. A successful probe re-enables
	// members this mechanism disabled earlier, and never one an operator
	// disabled by hand.
	AutoDisableAfter int
}

func (o Options) withDefaults() Options {
	if o.MaxTokens <= 0 {
		o.MaxTokens = DefaultMaxTokens
	}
	if o.MaxTokens > MaxTokensCeiling {
		o.MaxTokens = MaxTokensCeiling
	}
	if o.Concurrency <= 0 {
		o.Concurrency = DefaultConcurrency
	}
	if o.Concurrency > ConcurrencyCeiling {
		o.Concurrency = ConcurrencyCeiling
	}
	if o.Prompt == "" {
		o.Prompt = DefaultPrompt
	}
	return o
}

// Relay is the slice of the proxy a prober needs. Satisfied by *proxy.Service.
type Relay interface {
	ChatCompletionsWithMeta(ctx context.Context, req proxy.Request) (*relay.Result, *proxy.AttemptMeta)
}

// Service runs probe tasks in the background.
type Service struct {
	db     *store.DB
	relay  Relay
	logger *slog.Logger

	mu      sync.Mutex
	cancels map[int64]context.CancelFunc
}

func NewService(db *store.DB, relay Relay, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Service{db: db, relay: relay, logger: logger, cancels: make(map[int64]context.CancelFunc)}
}

// Start creates a task for the given pairs and runs it in the background.
// It returns as soon as the task is persisted; poll the task for progress.
//
// ctx owns the run: cancelling it stops new probes from being dispatched.
// Callers that outlive the current request (scheduled runs) pass a long-lived
// context; a request handler must not pass r.Context(), which is cancelled as
// soon as the response is written.
func (s *Service) Start(ctx context.Context, pairs []Pair, opts Options) (*store.ProbeTask, error) {
	if len(pairs) == 0 {
		return nil, errors.New("probe: nothing to probe")
	}
	opts = opts.withDefaults()
	task := &store.ProbeTask{
		Status:      store.ProbeTaskRunning,
		Total:       len(pairs),
		MaxTokens:   opts.MaxTokens,
		Concurrency: opts.Concurrency,
		Prompt:      opts.Prompt,
	}
	id, err := s.db.CreateProbeTask(task)
	if err != nil {
		return nil, err
	}
	// Re-read so the caller gets the persisted row: started_at is filled by
	// SQLite, and handing back the struct we just sent would report a zero time.
	stored, err := s.db.GetProbeTask(id)
	if err != nil {
		return nil, err
	}
	if stored == nil {
		return nil, fmt.Errorf("probe: task %d vanished after insert", id)
	}

	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.cancels[id] = cancel
	s.mu.Unlock()

	go s.run(ctx, stored, pairs, opts)
	return stored, nil
}

// Cancel stops issuing new probes for a running task. In-flight probes finish
// because aborting mid-request would leave the upstream call unaccounted for.
func (s *Service) Cancel(id int64) error {
	if err := s.db.CancelProbeTask(id); err != nil {
		return err
	}
	s.mu.Lock()
	cancel, ok := s.cancels[id]
	s.mu.Unlock()
	if ok {
		cancel()
	}
	return nil
}

func (s *Service) run(ctx context.Context, task *store.ProbeTask, pairs []Pair, opts Options) {
	defer func() {
		s.mu.Lock()
		delete(s.cancels, task.ID)
		s.mu.Unlock()
	}()

	sem := make(chan struct{}, task.Concurrency)
	var mu sync.Mutex
	var completed, okCount, failCount int
	stopped := false

	for _, pair := range pairs {
		// Check before each dispatch: a cancelled task must not start new work.
		select {
		case <-ctx.Done():
			stopped = true
		default:
		}
		if stopped {
			break
		}
		sem <- struct{}{}
		go func(pair Pair) {
			defer func() { <-sem }()

			result := s.probeOne(ctx, task.ID, pair, task.MaxTokens, task.Prompt)
			if err := s.db.InsertProbeResult(&result); err != nil {
				s.logger.Warn("probe: record result", "task", task.ID, "error", err)
			}
			failures, err := s.db.UpsertModelHealth(&store.ModelHealth{
				ChannelID: result.ChannelID,
				Model:     result.Model,
				OK:        result.OK,
				LatencyMS: result.LatencyMS,
				Source:    "probe",
			})
			if err != nil {
				s.logger.Warn("probe: record health", "task", task.ID, "error", err)
			} else if opts.AutoDisableAfter > 0 {
				s.applyHealthAction(pair, result.OK, failures, opts.AutoDisableAfter)
			}

			mu.Lock()
			completed++
			if result.OK {
				okCount++
			} else {
				failCount++
			}
			c, o, f := completed, okCount, failCount
			mu.Unlock()
			if err := s.db.UpdateProbeProgress(task.ID, c, o, f); err != nil {
				s.logger.Warn("probe: update progress", "task", task.ID, "error", err)
			}
		}(pair)
	}

	// Drain the semaphore to wait for in-flight probes.
	for i := 0; i < cap(sem); i++ {
		sem <- struct{}{}
	}

	status := store.ProbeTaskDone
	if stopped || ctx.Err() != nil {
		status = store.ProbeTaskCancelled
	}
	if err := s.db.FinishProbeTask(task.ID, status); err != nil {
		s.logger.Warn("probe: finish task", "task", task.ID, "error", err)
	}
}

// applyHealthAction turns a probe outcome into a routing decision: disable a
// member that keeps failing, re-enable one that has recovered.
//
// Only members this mechanism disabled are ever re-enabled. The guard lives in
// AutoRecoverByPair (auto_disabled = 1), so a member an operator switched off
// stays off no matter how green the probes come back.
func (s *Service) applyHealthAction(pair Pair, ok bool, failures, threshold int) {
	if ok {
		restored, err := s.db.RouteMember.AutoRecoverByPair(pair.ChannelID, pair.Model)
		if err != nil {
			s.logger.Warn("probe: recover member", "channel", pair.ChannelID, "model", pair.Model, "error", err)
			return
		}
		if restored > 0 {
			s.logger.Info("probe: re-enabled recovered member",
				"channel", pair.ChannelID, "model", pair.Model, "members", restored)
		}
		return
	}
	if failures < threshold {
		return
	}
	reason := fmt.Sprintf("probe failed %d times in a row", failures)
	disabled, err := s.db.RouteMember.AutoDisableByPair(pair.ChannelID, pair.Model, reason)
	if err != nil {
		s.logger.Warn("probe: disable member", "channel", pair.ChannelID, "model", pair.Model, "error", err)
		return
	}
	if disabled > 0 {
		s.logger.Info("probe: disabled failing member",
			"channel", pair.ChannelID, "model", pair.Model, "members", disabled, "failures", failures)
	}
}

// probeOne sends a single minimal chat completion to one channel.
func (s *Service) probeOne(ctx context.Context, taskID int64, pair Pair, maxTokens int, prompt string) store.ProbeResult {
	result := store.ProbeResult{TaskID: taskID, ChannelID: pair.ChannelID, Model: pair.Model}

	if prompt == "" {
		prompt = DefaultPrompt
	}
	body, err := json.Marshal(map[string]any{
		"model": pair.Model,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"stream":     false,
		"max_tokens": maxTokens,
	})
	if err != nil {
		result.Error = "build request: " + err.Error()
		return result
	}

	ctx, cancel := context.WithTimeout(ctx, DefaultTimeout)
	defer cancel()

	started := time.Now()
	res, _ := s.relay.ChatCompletionsWithMeta(ctx, proxy.Request{
		RequestID:       fmt.Sprintf("probe-%d-%d", taskID, pair.ChannelID),
		Model:           pair.Model,
		Body:            body,
		Stream:          false,
		PreferChannelID: pair.ChannelID,
		// The flag that keeps probe traffic out of the health bookkeeping.
		Probe: true,
	})
	latency := int(time.Since(started).Milliseconds())
	if latency < 0 {
		latency = 0
	}
	result.LatencyMS = latency

	if res == nil {
		result.Error = "empty result from proxy"
		return result
	}
	result.StatusCode = res.StatusCode

	// Always drain and close: the upstream connection is pooled.
	detail := ""
	if res.Body != nil {
		snippet, _ := io.ReadAll(io.LimitReader(res.Body, ErrorDetailLimit))
		_ = res.Body.Close()
		detail = string(snippet)
	}

	switch {
	case res.Err != nil:
		result.Error = res.Err.Error()
	case res.StatusCode >= 200 && res.StatusCode < 300:
		result.OK = true
	default:
		result.Error = fmt.Sprintf("upstream status %d", res.StatusCode)
		if detail != "" {
			result.Error += ": " + detail
		}
	}
	return result
}

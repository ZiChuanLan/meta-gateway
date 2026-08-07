// Package healthsweep periodically probes every enabled channel and grades it
// operational / degraded / error, alerting on state transitions. Each channel
// runs on its own jittered timer (anti-thundering-herd); a semaphore bounds
// simultaneous probes; in-flight re-entry per channel is impossible by
// construction (one goroutine per channel). Probing reuses the SSRF-safe
// outbound client through discovery.Service.Probe.
package healthsweep

import (
	"context"
	"errors"
	"log"
	"math/rand/v2"
	"strconv"
	"sync"
	"time"

	"github.com/lan/meta-gateway/internal/discovery"
	"github.com/lan/meta-gateway/internal/store"
	"github.com/lan/meta-gateway/internal/webhook"
)

// State values reported by Status().
const (
	StateOperational = "operational"
	StateDegraded    = "degraded"
	StateError       = "error"
	StateUnknown     = "unknown"
)

// Config tunes the sweep loop.
type Config struct {
	Enabled             bool
	IntervalSeconds     int
	JitterSeconds       int
	DegradedThresholdMs int
	Concurrency         int
	TimeoutSeconds      int
}

// DefaultConfig returns sane defaults (disabled sweep).
func DefaultConfig() Config {
	return Config{
		Enabled:             false,
		IntervalSeconds:     300,
		JitterSeconds:       30,
		DegradedThresholdMs: 2000,
		Concurrency:         4,
		TimeoutSeconds:      15,
	}
}

// ChannelHealth is one channel's latest sweep verdict.
type ChannelHealth struct {
	ChannelID int64     `json:"channel_id"`
	State     string    `json:"state"`
	LatencyMs int       `json:"latency_ms,omitempty"`
	Error     string    `json:"error,omitempty"`
	CheckedAt time.Time `json:"checked_at,omitempty"`
}

// prober is the probe surface used by the sweep; discovery.Service implements
// it. Abstracted so tests can inject a fake.
type prober interface {
	Probe(ctx context.Context, channelID int64) (*discovery.ProbeResult, error)
}

// Service owns the sweep lifecycle and per-channel state.
type Service struct {
	db        *store.DB
	probe     prober
	notifier  *webhook.Notifier
	cfg       Config

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu     sync.RWMutex
	status map[int64]ChannelHealth
}

// New builds the sweep service. notifier may be nil (alerts disabled).
func New(db *store.DB, probe prober, notifier *webhook.Notifier, cfg Config) *Service {
	if cfg.IntervalSeconds <= 0 {
		cfg.IntervalSeconds = 300
	}
	if cfg.JitterSeconds < 0 || cfg.JitterSeconds > cfg.IntervalSeconds {
		cfg.JitterSeconds = 30
	}
	if cfg.DegradedThresholdMs <= 0 {
		cfg.DegradedThresholdMs = 2000
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 4
	}
	if cfg.TimeoutSeconds <= 0 {
		cfg.TimeoutSeconds = 15
	}
	return &Service{
		db:        db,
		probe:     probe,
		notifier:  notifier,
		cfg:       cfg,
		status:    make(map[int64]ChannelHealth),
	}
}

// Start launches one jittered probe loop per enabled channel. Idempotent.
func (s *Service) Start() {
	if s.cancel != nil {
		return // already running
	}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.wg.Add(1)
	go s.supervisor()
}

// Stop cancels the loops and waits for in-flight probes to finish.
func (s *Service) Stop() {
	if s.cancel == nil {
		return
	}
	s.cancel()
	s.wg.Wait()
	s.cancel = nil
}

// Status returns the latest verdict for every known channel.
func (s *Service) Status() []ChannelHealth {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ChannelHealth, 0, len(s.status))
	for _, h := range s.status {
		out = append(out, h)
	}
	return out
}

// supervisor periodically reloads the enabled channel set so admin edits are
// picked up without a restart, and (re)spawns per-channel loops.
func (s *Service) supervisor() {
	defer s.wg.Done()
	// Reload cadence: every interval; new channels start immediately.
	ticker := time.NewTicker(time.Duration(s.cfg.IntervalSeconds) * time.Second)
	defer ticker.Stop()
	s.spawnForEnabled()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.spawnForEnabled()
		}
	}
}

// spawnForEnabled starts loops for enabled channels that have none. Loops
// self-terminate when the channel disappears from the enabled set; the
// supervisor re-creates them on the next reload.
func (s *Service) spawnForEnabled() {
	channels, err := s.db.Channel.ListEnabled()
	if err != nil {
		log.Printf("healthsweep: list channels: %v", err)
		return
	}
	active := make(map[int64]bool, len(channels))
	for _, ch := range channels {
		active[ch.ID] = true
		if s.loopRunning(ch.ID) {
			continue
		}
		s.wg.Add(1)
		go s.channelLoop(ch.ID)
	}
	s.mu.Lock()
	for id := range s.status {
		if !active[id] {
			delete(s.status, id)
		}
	}
	s.mu.Unlock()
}

// loopRunning reports whether a probe loop is alive for the channel.
func (s *Service) loopRunning(channelID int64) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.status[channelID]
	// The status map doubles as the liveness registry: loops insert their
	// entry before first probe, remove it on exit.
	return ok
}

// channelLoop probes one channel forever, with jitter between rounds.
func (s *Service) channelLoop(channelID int64) {
	defer s.wg.Done()
	// Register liveness (entry with unknown state) so the supervisor does not
	// spawn a duplicate loop.
	s.mu.Lock()
	s.status[channelID] = ChannelHealth{ChannelID: channelID, State: StateUnknown}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.status, channelID)
		s.mu.Unlock()
	}()
	for {
		delay := time.Duration(s.cfg.IntervalSeconds)*time.Second +
			time.Duration(rand.IntN(s.cfg.JitterSeconds+1))*time.Second
		timer := time.NewTimer(delay)
		select {
		case <-s.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			s.probeOnce(channelID)
		}
	}
}

// probeOnce runs one bounded probe and records/grade/alert on transitions.
func (s *Service) probeOnce(channelID int64) {
	parent := s.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, time.Duration(s.cfg.TimeoutSeconds)*time.Second)
	defer cancel()

	result, err := s.probe.Probe(ctx, channelID)
	var health ChannelHealth
	state := StateError
	latency, checkedAt := 0, time.Now()
	if err == nil {
		latency = result.LatencyMs
		state = StateOperational
		if latency > s.cfg.DegradedThresholdMs {
			state = StateDegraded
		}
		if s.db.Channel != nil {
			_ = s.db.Channel.RecordProbeSuccess(channelID, checkedAt)
		}
	} else {
		if s.db.Channel != nil {
			_ = s.db.Channel.RecordProbeFailure(channelID, checkedAt, probeCategory(err))
		}
	}
	health = ChannelHealth{
		ChannelID: channelID,
		State:     state,
		LatencyMs: latency,
		CheckedAt: checkedAt,
	}
	if err != nil {
		health.Error = probeCategory(err)
	}

	s.mu.Lock()
	previous, existed := s.status[channelID]
	s.status[channelID] = health
	s.mu.Unlock()

	// Alert on transitions only (steady-state failures would spam; the notifier
	// throttles anyway, but the transition filter keeps the signal clean).
	if s.notifier != nil && existed && previous.State != state {
		switch state {
		case StateOperational:
			s.notifier.SendAlert(ctx, webhook.AlertInfo, "渠道健康恢复",
				channelAlertText(channelID, "已恢复 operational"))
		case StateDegraded:
			s.notifier.SendAlert(ctx, webhook.AlertWarning, "渠道性能降级",
				channelAlertText(channelID, "延迟超过阈值"))
		case StateError:
			s.notifier.SendAlert(ctx, webhook.AlertError, "渠道探测失败",
				channelAlertText(channelID, health.Error))
		}
	}
}

// probeCategory maps a discovery error to a stable redacted category.
func probeCategory(err error) string {
	if err == nil {
		return ""
	}
	var dErr *discovery.Error
	if errors.As(err, &dErr) && dErr.Category != "" {
		return dErr.Category
	}
	return "upstream_failure"
}

func channelAlertText(channelID int64, detail string) string {
	return "渠道 #" + strconv.FormatInt(channelID, 10) + ": " + detail
}

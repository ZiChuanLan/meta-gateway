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
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/lan/meta-gateway/internal/discovery"
	"github.com/lan/meta-gateway/internal/domain"
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
// cfg is guarded by cfgMu so SetConfig can hot-swap the sweep policy while
// the loops run; loops read the current config at the start of every round.
type Service struct {
	db       *store.DB
	probe    prober
	notifier *webhook.Notifier

	cfgMu sync.RWMutex
	cfg   Config

	// probeMu gates the actual upstream probes. Channel loops remain separate
	// so each channel keeps its own jitter, while this gate makes the exposed
	// Concurrency setting a real global ceiling and supports hot changes.
	probeMu     sync.Mutex
	probeActive int
	probeWake   chan struct{}

	lifecycleMu sync.Mutex
	ctx         context.Context
	cancel      context.CancelFunc
	stopping    bool
	wg          sync.WaitGroup

	mu     sync.RWMutex
	status map[int64]ChannelHealth
}

// SetConfig hot-applies a new sweep policy while the service runs. Loops pick
// it up at the next round; disabling stops spawning (existing loops drain on
// their next round boundary). Enabling while running spawns immediately so a
// toggle from Admin takes effect without waiting for the supervisor tick.
func (s *Service) SetConfig(cfg Config) {
	s.cfgMu.Lock()
	s.cfg = sanitizeConfig(cfg)
	enabled := s.cfg.Enabled
	s.cfgMu.Unlock()
	s.wakeProbeWaiters()
	if enabled {
		if ctx, ok := s.runningContext(); ok {
			s.spawnForEnabled(ctx)
		}
	}
}

// loadCfg returns a copy of the current sweep policy.
func (s *Service) loadCfg() Config {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return s.cfg
}

// sanitizeConfig clamps a sweep policy into its safe domain (same rules as New).
func sanitizeConfig(cfg Config) Config {
	if cfg.IntervalSeconds <= 0 {
		cfg.IntervalSeconds = 300
	}
	if cfg.JitterSeconds < 0 {
		cfg.JitterSeconds = 0
	}
	if cfg.JitterSeconds > cfg.IntervalSeconds {
		cfg.JitterSeconds = cfg.IntervalSeconds
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
	return cfg
}

// New builds the sweep service. notifier may be nil (alerts disabled).
func New(db *store.DB, probe prober, notifier *webhook.Notifier, cfg Config) *Service {
	return &Service{
		db:        db,
		probe:     probe,
		notifier:  notifier,
		cfg:       sanitizeConfig(cfg),
		status:    make(map[int64]ChannelHealth),
		probeWake: make(chan struct{}),
	}
}

// Start launches one jittered probe loop per enabled channel. Idempotent.
func (s *Service) Start() {
	s.lifecycleMu.Lock()
	if s.cancel != nil || s.stopping {
		s.lifecycleMu.Unlock()
		return // already running
	}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	ctx := s.ctx
	s.wg.Add(1)
	s.lifecycleMu.Unlock()
	go s.supervisor(ctx)
}

// Stop cancels the loops and waits for in-flight probes to finish.
func (s *Service) Stop() {
	s.lifecycleMu.Lock()
	if s.cancel == nil {
		s.lifecycleMu.Unlock()
		return
	}
	s.stopping = true
	cancel := s.cancel
	s.lifecycleMu.Unlock()
	cancel()
	s.wg.Wait()
	s.lifecycleMu.Lock()
	s.cancel = nil
	s.ctx = nil
	s.stopping = false
	s.lifecycleMu.Unlock()
}

func (s *Service) runningContext() (context.Context, bool) {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.cancel == nil || s.stopping || s.ctx == nil {
		return nil, false
	}
	return s.ctx, true
}

// Status returns the latest verdict for every known channel.
func (s *Service) Status() []ChannelHealth {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ChannelHealth, 0, len(s.status))
	for _, h := range s.status {
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ChannelID < out[j].ChannelID })
	return out
}

// supervisor periodically reloads the enabled channel set so admin edits are
// picked up without a restart, and (re)spawns per-channel loops. The tick is
// fixed at 15s (independent of the sweep interval) so both channel-set changes
// and a runtime enable/disable toggle take effect quickly.
func (s *Service) supervisor(ctx context.Context) {
	defer s.wg.Done()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	s.spawnForEnabled(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.spawnForEnabled(ctx)
		}
	}
}

// spawnForEnabled starts loops for enabled channels that have none. Loops
// self-terminate when the channel disappears from the enabled set or the sweep
// is switched off; the supervisor re-creates them on the next reload.
func (s *Service) spawnForEnabled(ctx context.Context) {
	// Coordinate WaitGroup additions with Stop. Once stopping begins no new
	// channel loop may be added while Stop is waiting for the existing set.
	s.lifecycleMu.Lock()
	if s.cancel == nil || s.stopping {
		s.lifecycleMu.Unlock()
		return
	}
	s.lifecycleMu.Unlock()
	if !s.loadCfg().Enabled {
		// Sweep disabled: do not spawn; live loops drain on their next round.
		return
	}
	if s.db == nil || s.db.Channel == nil {
		return
	}
	channels, err := s.db.Channel.ListEnabled()
	if err != nil {
		log.Printf("healthsweep: list channels: %v", err)
		return
	}
	active := make(map[int64]bool, len(channels))
	for _, ch := range channels {
		active[ch.ID] = true
		if !s.reserveLoop(ch.ID) {
			continue
		}
		s.lifecycleMu.Lock()
		if s.cancel == nil || s.stopping {
			s.lifecycleMu.Unlock()
			s.mu.Lock()
			delete(s.status, ch.ID)
			s.mu.Unlock()
			break
		}
		s.wg.Add(1)
		s.lifecycleMu.Unlock()
		go s.channelLoop(ctx, ch.ID)
	}
	s.mu.Lock()
	for id := range s.status {
		if !active[id] {
			delete(s.status, id)
		}
	}
	s.mu.Unlock()
}

// reserveLoop atomically marks a channel as live before the goroutine is
// launched. SetConfig and the supervisor can call spawnForEnabled concurrently;
// reserving under the status lock prevents both callers from starting a loop.
func (s *Service) reserveLoop(channelID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.status[channelID]; exists {
		return false
	}
	s.status[channelID] = ChannelHealth{ChannelID: channelID, State: StateUnknown}
	return true
}

// channelLoop probes one channel forever, with jitter between rounds. It
// re-reads the sweep config at every round so interval/jitter changes are
// picked up live, and exits when the sweep is disabled at runtime.
func (s *Service) channelLoop(ctx context.Context, channelID int64) {
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
		cfg := s.loadCfg()
		if !cfg.Enabled {
			return // sweep switched off at runtime
		}
		delay := time.Duration(cfg.IntervalSeconds)*time.Second +
			time.Duration(rand.IntN(cfg.JitterSeconds+1))*time.Second
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			s.probeOnceContext(ctx, channelID)
		}
	}
}

// probeOnce keeps the synchronous test/maintenance hook using the service's
// current lifecycle context when one exists.
func (s *Service) probeOnce(channelID int64) {
	parent, ok := s.runningContext()
	if !ok {
		parent = context.Background()
	}
	s.probeOnceContext(parent, channelID)
}

// probeOnceContext runs one bounded probe and records/grades/alerts on
// transitions.
func (s *Service) probeOnceContext(parent context.Context, channelID int64) {
	if parent == nil {
		parent = context.Background()
	}
	cfg := s.loadCfg()
	if !s.acquireProbeSlot(parent, cfg.Concurrency) {
		return
	}
	defer s.releaseProbeSlot()
	cfg = s.loadCfg()
	ctx, cancel := context.WithTimeout(parent, time.Duration(cfg.TimeoutSeconds)*time.Second)
	defer cancel()

	var result *discovery.ProbeResult
	var err error
	if s.probe == nil {
		err = errors.New("health probe unavailable")
	} else {
		result, err = s.probe.Probe(discovery.WithoutProbePersistence(ctx), channelID)
		if err == nil && result == nil {
			err = errors.New("health probe returned no result")
		}
	}
	var health ChannelHealth
	state := StateError
	latency, checkedAt := 0, time.Now()
	if err == nil {
		latency = result.LatencyMs
		state = StateOperational
		verdict := ""
		if latency > cfg.DegradedThresholdMs {
			state = StateDegraded
			verdict = domain.CategoryProbeSlow
		}
		if s.db != nil && s.db.Channel != nil {
			_ = s.db.Channel.RecordProbeSuccessWithVerdict(channelID, checkedAt, verdict)
			// A successful business probe proves network reachability; refresh
			// the connectivity verdict with the same latency so the ping badge
			// stays current without a separate network probe.
			_ = s.db.Channel.RecordPingSuccess(channelID, checkedAt, latency)
		}
		if s.db != nil && s.db.HealthHistory != nil {
			_ = s.db.HealthHistory.Append(channelID, domain.ProbeKindProbe, true, latency, verdict, checkedAt)
		}
	} else {
		if s.db != nil && s.db.Channel != nil {
			_ = s.db.Channel.RecordProbeFailure(channelID, checkedAt, probeCategory(err))
		}
		if s.db != nil && s.db.HealthHistory != nil {
			_ = s.db.HealthHistory.Append(channelID, domain.ProbeKindProbe, false, 0, probeCategory(err), checkedAt)
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

func (s *Service) acquireProbeSlot(ctx context.Context, limit int) bool {
	if limit < 1 {
		limit = 1
	}
	for {
		if current := s.loadCfg().Concurrency; current > 0 {
			limit = current
		}
		s.probeMu.Lock()
		if s.probeWake == nil {
			s.probeWake = make(chan struct{})
		}
		if s.probeActive < limit {
			s.probeActive++
			s.probeMu.Unlock()
			return true
		}
		wake := s.probeWake
		s.probeMu.Unlock()
		select {
		case <-ctx.Done():
			return false
		case <-wake:
		}
	}
}

func (s *Service) releaseProbeSlot() {
	s.probeMu.Lock()
	if s.probeActive > 0 {
		s.probeActive--
	}
	s.wakeProbeWaitersLocked()
	s.probeMu.Unlock()
}

func (s *Service) wakeProbeWaiters() {
	s.probeMu.Lock()
	s.wakeProbeWaitersLocked()
	s.probeMu.Unlock()
}

func (s *Service) wakeProbeWaitersLocked() {
	if s.probeWake != nil {
		close(s.probeWake)
	}
	s.probeWake = make(chan struct{})
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

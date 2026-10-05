package siteprobe

import (
	"context"
	"io"
	"log/slog"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"
)

// Scheduler collects every enabled site's probe source on a configurable
// cadence.
//
// Unlike the model probe scheduler there is nothing to configure per run: the
// loop only reads public pages, so it starts with the process and stops with it
// (RegisterStopper). The cadence is hot-reloadable (SetSchedule) because sites
// publish their own probe data at very different rates; the jitter exists so a
// fleet of gateways does not hit the same status page at the same second.
type Scheduler struct {
	service *Service
	logger  *slog.Logger

	// Cadence in nanoseconds, read at the top of every round so a settings
	// change lands on the next one without restarting the loop.
	intervalNanos atomic.Int64
	jitterNanos   atomic.Int64
	// catalogURL is the third-party directory snapshot; empty means the package
	// default.
	catalogURL string

	startOnce sync.Once
	stopOnce  sync.Once
	stop      chan struct{}
	done      chan struct{}
	busy      sync.Mutex
}

// NewScheduler builds the loop; Start begins firing. interval/jitter are the
// bootstrap cadence, normally the env values resolved by runtime settings.
func NewScheduler(service *Service, interval, jitter time.Duration, logger *slog.Logger) *Scheduler {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	scheduler := &Scheduler{
		service: service,
		logger:  logger,
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
	scheduler.SetSchedule(interval, jitter)
	return scheduler
}

// SetSchedule hot-applies the collection cadence. A non-positive interval falls
// back to the shipped default rather than disabling collection: the loop has no
// off switch, and "0" from an unset settings row must not stop every site from
// being probed.
func (s *Scheduler) SetSchedule(interval, jitter time.Duration) {
	if interval <= 0 {
		interval = DefaultInterval
	}
	if jitter < 0 {
		jitter = 0
	}
	// Jitter wider than the interval would make rounds overlap for no reason.
	if jitter > interval {
		jitter = interval
	}
	s.intervalNanos.Store(int64(interval))
	s.jitterNanos.Store(int64(jitter))
}

// Cadence returns the currently configured interval and jitter.
func (s *Scheduler) Cadence() (time.Duration, time.Duration) {
	interval := time.Duration(s.intervalNanos.Load())
	if interval <= 0 {
		interval = DefaultInterval
	}
	return interval, time.Duration(s.jitterNanos.Load())
}

// Start begins the collection loop in the background.
func (s *Scheduler) Start() {
	s.startOnce.Do(func() {
		go s.loop()
	})
}

// Stop halts the loop and waits for it to return. Safe to call more than once,
// which is what the router's stopper list expects.
func (s *Scheduler) Stop() {
	s.stopOnce.Do(func() {
		close(s.stop)
		<-s.done
	})
}

// RoundResult is what one collection pass did: how many sites answered, how many
// failed, and which routing changes the auto-apply sites asked for.
type RoundResult struct {
	Collected int      `json:"collected"`
	Failed    int      `json:"failed"`
	Actions   []Action `json:"actions"`
}

// CollectNow runs one round synchronously: collect every enabled site, let the
// sites configured for it act on the verdicts, and refresh the third-party
// snapshot on its own slower cadence. This is exactly what the background loop
// does, so a manual "collect now" cannot behave differently from a scheduled
// pass.
func (s *Scheduler) CollectNow(ctx context.Context) (RoundResult, error) {
	// The third-party snapshot goes first: it is one request for every site the
	// directory monitors, and putting it after the site collection would lose it
	// whenever a slow or dead site eats the round's context budget.
	if count, synced, syncErr := s.service.SyncExternalIfDue(ctx, s.catalogURL, s.service.now()); syncErr != nil {
		s.logger.Warn("site probe: third-party snapshot", "error", syncErr)
	} else if synced {
		s.logger.Info("site probe: third-party snapshot stored", "readings", count)
	}
	ok, failed, err := s.service.CollectEnabledSites(ctx)
	result := RoundResult{Collected: ok, Failed: failed}
	if err != nil {
		return result, err
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	actions, err := s.service.AutoApplyRound(ctx)
	if err != nil {
		return result, err
	}
	// AutoApplyRound logs each change; this only records the round's total.
	result.Actions = actions
	return result, nil
}

func (s *Scheduler) loop() {
	defer close(s.done)
	// The first round waits one interval: a gateway restart must not stampede
	// every status page at boot.
	for {
		timer := time.NewTimer(s.nextDelay())
		select {
		case <-s.stop:
			timer.Stop()
			return
		case <-timer.C:
		}
		s.fire()
	}
}

func (s *Scheduler) nextDelay() time.Duration {
	interval, jitter := s.Cadence()
	delay := interval
	if jitter > 0 {
		delay += time.Duration(rand.Int63n(int64(jitter)))
	}
	return delay
}

func (s *Scheduler) fire() {
	// A round that overruns its interval must not stack: collecting the same
	// pages twice concurrently produces duplicate rounds and duplicate verdicts.
	if !s.busy.TryLock() {
		s.logger.Info("site probe: skipped, a collection round is still running")
		return
	}
	defer s.busy.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	result, err := s.CollectNow(ctx)
	if err != nil {
		s.logger.Warn("site probe: collection round", "error", err)
	}
	if result.Collected > 0 || result.Failed > 0 {
		s.logger.Info("site probe: collection round finished",
			"ok", result.Collected, "failed", result.Failed, "actions", len(result.Actions))
	}
}

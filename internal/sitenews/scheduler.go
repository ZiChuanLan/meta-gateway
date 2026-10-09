package sitenews

import (
	"context"
	"io"
	"log/slog"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"
)

// Scheduler re-reads every site's notice board on a fixed cadence.
//
// There is nothing to configure per round and nothing to apply: the loop only
// reads public pages, so it starts with the process and stops with it
// (RegisterStopper). The jitter keeps a fleet of gateways from hitting the same
// boards in the same second.
type Scheduler struct {
	service *Service
	logger  *slog.Logger

	intervalNanos atomic.Int64
	jitterNanos   atomic.Int64

	startOnce sync.Once
	stopOnce  sync.Once
	stop      chan struct{}
	done      chan struct{}
	busy      sync.Mutex
}

// NewScheduler builds the loop; Start begins firing. interval/jitter are the
// bootstrap cadence; a non-positive interval falls back to DefaultInterval.
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

// SetSchedule hot-applies the cadence.
func (s *Scheduler) SetSchedule(interval, jitter time.Duration) {
	if interval <= 0 {
		interval = DefaultInterval
	}
	if jitter < 0 {
		jitter = 0
	}
	if jitter > interval {
		jitter = interval
	}
	s.intervalNanos.Store(int64(interval))
	s.jitterNanos.Store(int64(jitter))
}

// Cadence returns the configured interval and jitter.
func (s *Scheduler) Cadence() (time.Duration, time.Duration) {
	interval := time.Duration(s.intervalNanos.Load())
	if interval <= 0 {
		interval = DefaultInterval
	}
	return interval, time.Duration(s.jitterNanos.Load())
}

// Start begins the loop in the background.
func (s *Scheduler) Start() {
	s.startOnce.Do(func() {
		go s.loop()
	})
}

// Stop halts the loop and waits for it to return; safe to call more than once,
// which is what the router's stopper list expects.
func (s *Scheduler) Stop() {
	s.stopOnce.Do(func() {
		close(s.stop)
		<-s.done
	})
}

// RefreshNow runs one round synchronously. The manual button and the background
// loop take the same path, so they cannot disagree about what a refresh does.
func (s *Scheduler) RefreshNow(ctx context.Context) (RefreshResult, error) {
	if !s.busy.TryLock() {
		return RefreshResult{}, ErrBusy
	}
	defer s.busy.Unlock()
	result, err := s.service.Refresh(ctx)
	if err != nil {
		return result, err
	}
	if result.Fetched > 0 || result.Failed > 0 {
		s.logger.Info("site news: refresh finished",
			"sites", result.Sites, "fetched", result.Fetched, "failed", result.Failed, "new", result.Added)
	}
	return result, nil
}

func (s *Scheduler) loop() {
	defer close(s.done)
	// The first round waits one interval: a gateway restart must not stampede
	// every site's status endpoint.
	for {
		timer := time.NewTimer(s.nextDelay())
		select {
		case <-s.stop:
			timer.Stop()
			return
		case <-timer.C:
		}
		ctx, cancel := context.WithTimeout(context.Background(), roundTimeout)
		if _, err := s.RefreshNow(ctx); err != nil && err != ErrBusy {
			s.logger.Warn("site news: refresh", "error", err)
		}
		cancel()
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

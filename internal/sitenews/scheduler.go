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

// Scheduler re-reads every site's notice board on a configurable cadence.
//
// The loop only reads public pages, so it starts with the process and stops with
// it (RegisterStopper) and has nothing to apply round by round. The cadence is
// hot-reloadable (SetSchedule), and a non-positive interval turns the loop OFF:
// a notice board is background information, so "no schedule" is a legitimate
// choice, and the console's refresh button stays the reader in that case.
// (The site probe next door deliberately has no off switch — an unset settings
// row must not read as "stop probing" there. Here 0 is a value the operator
// picked, while NULL/-1 still means "not overridden".)
type Scheduler struct {
	service *Service
	logger  *slog.Logger

	// Cadence in nanoseconds. Zero means off; it is read at the top of every
	// wait, so a settings change never lands a round after a restart.
	intervalNanos atomic.Int64
	jitterNanos   atomic.Int64

	// wake re-arms the pending wait when the cadence changes, so turning the
	// loop off (or speeding it up) lands now instead of after the interval that
	// is already running down.
	wake chan struct{}

	startOnce sync.Once
	stopOnce  sync.Once
	stop      chan struct{}
	done      chan struct{}
	busy      sync.Mutex
}

// NewScheduler builds the loop; Start begins firing. interval/jitter are the
// bootstrap cadence — normally the env values resolved by runtime settings —
// and an interval of zero starts the loop disabled.
func NewScheduler(service *Service, interval, jitter time.Duration, logger *slog.Logger) *Scheduler {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	scheduler := &Scheduler{
		service: service,
		logger:  logger,
		wake:    make(chan struct{}, 1),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
	interval, jitter = normalizeSchedule(interval, jitter)
	scheduler.intervalNanos.Store(int64(interval))
	scheduler.jitterNanos.Store(int64(jitter))
	return scheduler
}

// SetSchedule hot-applies the cadence. A non-positive interval disables the
// loop; the wait in flight is re-armed, so the change takes effect now rather
// than one old interval later. Values that do not change the interval leave the
// pending wait alone, so saving an unrelated setting cannot keep pushing the
// next round away.
func (s *Scheduler) SetSchedule(interval, jitter time.Duration) {
	interval, jitter = normalizeSchedule(interval, jitter)
	previous := s.intervalNanos.Swap(int64(interval))
	s.jitterNanos.Store(int64(jitter))
	if previous != int64(interval) {
		s.wakeLoop()
	}
}

// Cadence returns the configured interval and jitter. A zero interval means the
// loop is off.
func (s *Scheduler) Cadence() (time.Duration, time.Duration) {
	return time.Duration(s.intervalNanos.Load()), time.Duration(s.jitterNanos.Load())
}

// normalizeSchedule clamps one cadence pair: a negative interval becomes zero
// (off), and jitter never exceeds the interval it decorates.
func normalizeSchedule(interval, jitter time.Duration) (time.Duration, time.Duration) {
	if interval < 0 {
		interval = 0
	}
	if jitter < 0 {
		jitter = 0
	}
	if jitter > interval {
		jitter = interval
	}
	return interval, jitter
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
// loop take the same path, so they cannot disagree about what a refresh does —
// and the button keeps working while the loop is off.
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
		interval, jitter := s.Cadence()
		if interval <= 0 {
			// Off: wait for a schedule change (or the process stopping). The
			// manual refresh does not come through here.
			select {
			case <-s.stop:
				return
			case <-s.wake:
			}
			continue
		}
		timer := time.NewTimer(delayFor(interval, jitter))
		select {
		case <-s.stop:
			timer.Stop()
			return
		case <-s.wake:
			// A new cadence: restart the wait against it.
			timer.Stop()
			continue
		case <-timer.C:
		}
		ctx, cancel := context.WithTimeout(context.Background(), roundTimeout)
		if _, err := s.RefreshNow(ctx); err != nil && err != ErrBusy {
			s.logger.Warn("site news: refresh", "error", err)
		}
		cancel()
	}
}

// delayFor is one wait: the interval plus up to jitter of slack, so a fleet of
// gateways does not read the same boards in the same second.
func delayFor(interval, jitter time.Duration) time.Duration {
	delay := interval
	if jitter > 0 {
		delay += time.Duration(rand.Int63n(int64(jitter)))
	}
	return delay
}

func (s *Scheduler) wakeLoop() {
	select {
	case s.wake <- struct{}{}:
	default:
		// A wake is already queued; one is enough to re-read the cadence.
	}
}

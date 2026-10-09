// Package keepalive makes one deliberately small call to a channel that has gone
// quiet, before the site's own "no call for N days" rule turns into a ban.
//
// Why it is idle-driven and not a timer: the ban counts days without a call, and
// N is the site's rule (15 days here, 30 there). A fixed daily ping would waste
// calls on a 30-day window and still arrive late on a 15-day one; firing on the
// remaining days means a channel that already has traffic is never touched.
//
// Why the policy decides the shape: a site that bans probing treats a one-token
// `hi` as the violation itself, so the same "keep this account alive" intention
// has to be expressed as a real small request there. That choice lives in
// internal/callplan, shared with the model probe, so the two automated paths
// cannot drift into sending different things for the same policy.
package keepalive

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/lan/meta-gateway/internal/callplan"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/proxy"
	"github.com/lan/meta-gateway/internal/store"
	"github.com/lan/meta-gateway/internal/webhook"
)

// DefaultPrompt is what a keepalive asks on a site that permits probing. The
// site has said the cheap shape is fine, so the honest cheap shape is used —
// the real form exists for the sites that said otherwise, not as a default.
const DefaultPrompt = "hi"

// ErrorDetailLimit caps how much of an upstream error body is kept.
const ErrorDetailLimit = 256

// Caller sends one prepared chat body straight to one channel, whatever the
// routes say. Satisfied by *proxy.Service (DirectChat).
//
// A keepalive is not a client: it exists for one account, not for a model the
// gateway offers. Going through routing would make a quiet account reachable
// only by publishing a route for the model it is called on, so keeping twenty
// accounts alive would mean offering twenty models.
type Caller interface {
	DirectChat(ctx context.Context, channelID int64, model string, body []byte) proxy.DirectTestResult
}

// Config is the global layer of keepalive configuration. The per-site window and
// switches live in the database; this is the kill switch, the cadence, and the
// fallback window for a site that was never given one.
type Config struct {
	// Enabled is the kill switch. Off means no round sends anything, whatever
	// the sites say.
	Enabled bool
	// Interval is how often a round runs. Rounds are cheap when nothing is due:
	// they read one table and return.
	Interval time.Duration
	// DefaultIdleDays is the window used by a site that has none configured.
	DefaultIdleDays int
}

// Round is the outcome of one check: what was due, what was sent.
type Round struct {
	Checked  int
	Sent     int
	Failed   int
	Skipped  int
	NotReady int
}

// Service decides and sends keepalive calls.
type Service struct {
	db      *store.DB
	caller  Caller
	logger  *slog.Logger
	now     func() time.Time
	mutex   sync.RWMutex
	config  Config
	running sync.Mutex

	notifier *webhook.Notifier
}

// NewService builds the keepalive service. notifier may be nil (alerts off).
func NewService(db *store.DB, caller Caller, notifier *webhook.Notifier, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{db: db, caller: caller, notifier: notifier, logger: logger, now: time.Now}
}

// SetConfig hot-applies the runtime settings.
func (s *Service) SetConfig(cfg Config) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.config = cfg
}

func (s *Service) configSnapshot() Config {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return s.config
}

// Targets returns every channel's keepalive state as the console sees it: the
// window, how long the account has really been idle, and what would happen if a
// round ran now. It is the same resolution the runner uses, so what the page
// shows and what the next round does cannot disagree.
func (s *Service) Targets() ([]domain.KeepaliveTarget, error) {
	cfg := s.configSnapshot()
	return s.db.Channel.KeepaliveTargets(cfg.DefaultIdleDays)
}

// RunOnce runs one round and reports what it did.
//
// Nothing here is a "force": a target that is not yet due is left alone, because
// the point is to call as little as possible.
func (s *Service) RunOnce(ctx context.Context) (Round, error) {
	cfg := s.configSnapshot()
	if !cfg.Enabled {
		return Round{}, nil
	}
	// One round at a time: a slow round must not overlap the next tick, or the
	// daily cap would be checked against a day that is already spent.
	if !s.running.TryLock() {
		s.logger.Info("keepalive: previous round still running, skipping this tick")
		return Round{}, nil
	}
	defer s.running.Unlock()

	targets, err := s.db.Channel.KeepaliveTargets(cfg.DefaultIdleDays)
	if err != nil {
		return Round{}, fmt.Errorf("keepalive targets: %w", err)
	}
	now := s.now()
	round := Round{Checked: len(targets)}
	for _, target := range targets {
		if ctx.Err() != nil {
			return round, ctx.Err()
		}
		switch {
		case target.SkipReason != "":
			round.Skipped++
		case !target.Config.Enabled || target.Config.IdleDays <= 0:
			// Off by an explicit decision, or no window anywhere in the chain.
			round.Skipped++
		case target.Config.DailyCap > 0 && target.SendsToday >= target.Config.DailyCap:
			round.Skipped++
		case domain.InQuietHours(target.Config.QuietHours, now):
			// Deferred, not cancelled: the next round after the window re-checks
			// the same target, and the remaining-days math still holds.
			round.Skipped++
		case !target.Config.Ready(target.IdleDays(now)):
			round.NotReady++
		default:
			event := s.send(ctx, target, target.Config.TriggerReason(target.IdleDays(now)), now)
			if event.OK {
				round.Sent++
			} else {
				round.Failed++
			}
		}
	}
	if round.Sent > 0 || round.Failed > 0 {
		s.logger.Info("keepalive round finished",
			"checked", round.Checked, "sent", round.Sent, "failed", round.Failed,
			"not_ready", round.NotReady, "skipped", round.Skipped)
	}
	return round, nil
}

// SendNow sends one keepalive for one channel regardless of the schedule. It is
// the console's "立即保活": an operator who has just changed a window should be
// able to prove it works without waiting a day.
//
// The policy still applies — this is a call, and it is shaped like every other
// automatic one — but the daily cap and the idle check are the operator's to
// override, because they clicked the button.
func (s *Service) SendNow(ctx context.Context, channelID int64) (domain.KeepaliveEvent, error) {
	cfg := s.configSnapshot()
	targets, err := s.db.Channel.KeepaliveTargets(cfg.DefaultIdleDays)
	if err != nil {
		return domain.KeepaliveEvent{}, err
	}
	now := s.now()
	for _, target := range targets {
		if target.ChannelID != channelID {
			continue
		}
		if target.SkipReason != "" {
			return domain.KeepaliveEvent{}, fmt.Errorf("cannot keepalive channel %d: %s", channelID, target.SkipReason)
		}
		reason := "manual"
		if target.LastCallAt != nil {
			reason = "manual (" + target.Config.TriggerReason(target.IdleDays(now)) + ")"
		}
		return s.send(ctx, target, reason, now), nil
	}
	return domain.KeepaliveEvent{}, fmt.Errorf("channel %d is not in the keepalive scope", channelID)
}

// send makes the call and records it. It never returns an error: a keepalive
// that failed is a fact to record, not a reason to stop the round.
func (s *Service) send(ctx context.Context, target domain.KeepaliveTarget, reason string, now time.Time) domain.KeepaliveEvent {
	event := domain.KeepaliveEvent{
		CredentialID: target.CredentialID,
		SiteID:       target.SiteID,
		SiteName:     target.SiteName,
		ChannelID:    target.ChannelID,
		ChannelName:  target.ChannelName,
		Model:        target.Model,
		Reason:       reason,
	}

	plan, err := callplan.Request(target.Policy, domain.PurposeKeepalive, callplan.Spec{
		Model:     target.Model,
		Prompt:    target.Config.Prompt,
		MaxTokens: target.Config.MaxTokens,
		ChannelID: target.ChannelID,
		At:        now,
	}, DefaultPrompt)
	if err != nil {
		event.Error = err.Error()
		s.record(event)
		return event
	}
	event.Form = plan.Form

	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	// Route-free on purpose (see Caller): the call belongs to this account, not
	// to a model the gateway serves.
	result := s.caller.DirectChat(ctx, target.ChannelID, target.Model, plan.Body)
	event.StatusCode = result.StatusCode
	switch {
	case result.OK:
		event.OK = true
	case result.Error != "":
		event.Error = result.Error
	default:
		event.Error = fmt.Sprintf("upstream status %d", result.StatusCode)
	}
	s.record(event)
	return event
}

// sendTimeout bounds one keepalive. It is generous because a site that bans
// probing may answer slowly, and this call has no client waiting on it.
const sendTimeout = 30 * time.Second

func (s *Service) record(event domain.KeepaliveEvent) {
	if err := s.db.Channel.RecordKeepaliveEvent(event); err != nil {
		s.logger.Warn("keepalive: record event", "channel", event.ChannelID, "error", err)
	}
	if event.OK {
		s.logger.Info("keepalive sent", "channel", event.ChannelID, "site", event.SiteName,
			"model", event.Model, "form", event.Form, "reason", event.Reason)
		if s.notifier != nil {
			s.notifier.SendAlert(context.Background(), webhook.AlertInfo,
				"保活调用成功",
				fmt.Sprintf("%s / %s：%s（%s）", event.SiteName, event.ChannelName, event.Model, event.Reason))
		}
		return
	}
	s.logger.Warn("keepalive failed", "channel", event.ChannelID, "site", event.SiteName,
		"model", event.Model, "form", event.Form, "reason", event.Reason, "error", event.Error)
	if s.notifier != nil {
		s.notifier.SendAlert(context.Background(), webhook.AlertWarning,
			"保活调用失败",
			fmt.Sprintf("%s / %s：%s（%s）— %s", event.SiteName, event.ChannelName, event.Model, event.Reason, event.Error))
	}
}

// Run drives rounds until the context is cancelled.
//
// The interval is re-read every tick rather than fixed in a ticker, because it
// is a runtime setting the console can change while this loop is running, and a
// loop that kept the interval it started with would quietly ignore the operator.
func (s *Service) Run(ctx context.Context) {
	// A short grace period before the first round: a restart should not fire a
	// burst of keepalives before the rest of the process has settled.
	timer := time.NewTimer(firstRoundDelay)
	defer timer.Stop()
	var last time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		cfg := s.configSnapshot()
		interval := cfg.Interval
		if interval <= 0 {
			interval = defaultInterval
		}
		if cfg.Enabled && time.Since(last) >= interval {
			last = time.Now()
			if _, err := s.RunOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
				s.logger.Warn("keepalive round", "error", err)
			}
		}
		// Waking every 30 seconds (rather than sleeping for the configured
		// interval) is what makes a shortened interval take effect promptly.
		timer.Reset(wakeInterval)
	}
}

const (
	// firstRoundDelay keeps a restart from firing keepalives before the process
	// has settled.
	firstRoundDelay = 2 * time.Minute
	// wakeInterval is how often the loop re-reads its configuration.
	wakeInterval = 30 * time.Second
	// defaultInterval is the round cadence when the runtime setting is unset.
	defaultInterval = time.Hour
)

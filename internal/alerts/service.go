// Package alerts evaluates configurable alert rules (metric/operator/
// threshold/window/sustained) on a fixed tick and delivers fired alerts
// through the webhook notifier (webhook/bark/serverchan/telegram/smtp).
package alerts

import (
	"context"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
	"github.com/lan/meta-gateway/internal/webhook"
)

// TickInterval is the rule evaluation cadence.
const TickInterval = 60 * time.Second

// Supported metrics (all computed from tables the gateway already maintains).
const (
	MetricChannelAvailability = "channel_availability" // min per-channel availability (0..1) over window
	MetricRequestFailRate     = "request_fail_rate"    // failed requests / total over window (0..1)
	MetricChannelError        = "channel_error"        // 1 when any channel's latest probe failed
	MetricErrorRate           = "error_rate"           // alias of request_fail_rate
)

// MetricDescriptions documents each metric for the admin UI.
var MetricDescriptions = map[string]string{
	MetricChannelAvailability: "lowest channel availability over the window (0..1, from health probes)",
	MetricRequestFailRate:     "share of relay requests that failed over the window (0..1, from proxy logs)",
	MetricChannelError:        "1 when any enabled channel's latest probe failed, else 0",
	MetricErrorRate:           "alias of request_fail_rate",
}

// Operator semantics.
var validOperators = map[string]bool{
	"gt": true, "gte": true, "lt": true, "lte": true, "eq": true, "neq": true,
}

// delivery is the subset of the webhook notifier the evaluator needs.
type delivery interface {
	SendAlert(ctx context.Context, level webhook.AlertLevel, title, message string) bool
}

// Service evaluates alert rules on a tick.
type Service struct {
	db       *store.DB
	notifier delivery
	now      func() time.Time

	mu sync.Mutex
	// sustainedSince tracks per-rule first-tick time of the firing condition.
	sustainedSince map[int64]time.Time
	// lastFiredAt tracks per-rule cooldown expiry.
	lastFiredAt map[int64]time.Time
}

// New creates the evaluator. notifier may be nil (rules still evaluate, but
// alerts are dropped).
func New(db *store.DB, notifier delivery) *Service {
	return &Service{
		db:             db,
		notifier:       notifier,
		now:            time.Now,
		sustainedSince: make(map[int64]time.Time),
		lastFiredAt:    make(map[int64]time.Time),
	}
}

// SetNotifier swaps the delivery backend (used when the notifier is wired
// after construction).
func (s *Service) SetNotifier(n delivery) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notifier = n
}

// Tick evaluates all enabled rules once. Callers run it on a fixed cadence.
func (s *Service) Tick(ctx context.Context) {
	rules, err := s.db.AlertRule.ListEnabled()
	if err != nil {
		log.Printf("alerts: list rules: %v", err)
		return
	}
	if len(rules) == 0 {
		return
	}
	metrics := s.computeMetrics(ctx, rules)
	now := s.now()

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rule := range rules {
		value, ok := metrics[rule.Metric]
		if !ok {
			continue
		}
		if !evalOperator(rule.Operator, value, rule.Threshold) {
			delete(s.sustainedSince, rule.ID)
			continue
		}
		// Condition holds this tick: track its first-tick for sustained gating.
		start, exists := s.sustainedSince[rule.ID]
		if !exists {
			s.sustainedSince[rule.ID] = now
			start = now
		}
		sustained := time.Duration(rule.SustainedSeconds) * time.Second
		if now.Sub(start) < sustained {
			continue
		}
		// Sustained reached: respect the per-rule cooldown.
		if last, fired := s.lastFiredAt[rule.ID]; fired && now.Sub(last) < time.Duration(rule.CooldownSeconds)*time.Second {
			continue
		}
		s.lastFiredAt[rule.ID] = now
		if s.notifier != nil {
			s.notifier.SendAlert(ctx, webhook.AlertLevel(rule.Level), fmt.Sprintf("告警规则: %s", rule.Name), fmt.Sprintf("规则 %q 触发: %s = %.3f (阈值 %s %.3f)", rule.Name, rule.Metric, value, rule.Operator, rule.Threshold))
		}
		log.Printf("alerts: rule %q fired metric=%s value=%.3f threshold=%s %.3f", rule.Name, rule.Metric, value, rule.Operator, rule.Threshold)
	}
}

// Run loops Tick every TickInterval until ctx is cancelled.
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(TickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.Tick(ctx)
		}
	}
}

// computeMetrics gathers every metric referenced by the enabled rules.
func (s *Service) computeMetrics(ctx context.Context, rules []store.AlertRule) map[string]float64 {
	out := make(map[string]float64, 4)
	want := func(name string) bool {
		for _, r := range rules {
			if r.Metric == name {
				return true
			}
		}
		return false
	}
	if want(MetricChannelAvailability) {
		out[MetricChannelAvailability] = s.computeMinAvailability(rules)
	}
	if want(MetricRequestFailRate) || want(MetricErrorRate) {
		out[MetricRequestFailRate] = s.computeRequestFailRate(rules)
		out[MetricErrorRate] = out[MetricRequestFailRate]
	}
	if want(MetricChannelError) {
		out[MetricChannelError] = s.computeChannelError()
	}
	return out
}

// computeMinAvailability returns the lowest per-channel availability over the
// rule's window (default 24h); 1 when no probes exist.
func (s *Service) computeMinAvailability(rules []store.AlertRule) float64 {
	window := 24 * time.Hour
	for _, r := range rules {
		if r.Metric == MetricChannelAvailability && r.WindowSeconds > 0 {
			window = time.Duration(r.WindowSeconds) * time.Second
			break
		}
	}
	summaries, err := s.db.HealthHistory.Summaries(s.now().Add(-window))
	if err != nil || len(summaries) == 0 {
		return 1
	}
	min := 1.0
	for _, sum := range summaries {
		if sum.Availability < min {
			min = sum.Availability
		}
	}
	return min
}

// computeRequestFailRate returns failed/total relay requests over the window.
func (s *Service) computeRequestFailRate(rules []store.AlertRule) float64 {
	window := time.Hour
	for _, r := range rules {
		if (r.Metric == MetricRequestFailRate || r.Metric == MetricErrorRate) && r.WindowSeconds > 0 {
			window = time.Duration(r.WindowSeconds) * time.Second
			break
		}
	}
	since := s.now().Add(-window)
	total, failed := s.db.ProxyLog.FailRate(since)
	if total == 0 {
		return 0
	}
	return float64(failed) / float64(total)
}

// computeChannelError reports 1 when any enabled channel's latest probe
// failed (probe error present and not the probe_slow verdict).
func (s *Service) computeChannelError() float64 {
	rows, err := s.db.Query(`SELECT last_probe_error FROM channels WHERE status = 'enabled' AND last_probe_error <> ''`)
	if err != nil {
		return 0
	}
	defer rows.Close()
	for rows.Next() {
		var verdict string
		if err := rows.Scan(&verdict); err != nil {
			continue
		}
		if verdict != domain.CategoryProbeSlow {
			return 1
		}
	}
	return 0
}

// evalOperator compares value to threshold.
func evalOperator(op string, value, threshold float64) bool {
	switch op {
	case "gt":
		return value > threshold
	case "gte":
		return value >= threshold
	case "lt":
		return value < threshold
	case "lte":
		return value <= threshold
	case "eq":
		return value == threshold
	case "neq":
		return value != threshold
	}
	return false
}

// ValidateRule checks a rule's fields and normalizes them (mirrors the admin
// endpoint validation so the UI gets the same errors).
func ValidateRule(r *store.AlertRule) error {
	if r.Name == "" {
		return fmt.Errorf("name is required")
	}
	if _, ok := MetricDescriptions[r.Metric]; !ok {
		return fmt.Errorf("unknown metric %q (supported: %s)", r.Metric, sortedMetricNames())
	}
	if !validOperators[r.Operator] {
		return fmt.Errorf("unknown operator %q (supported: gt/gte/lt/lte/eq/neq)", r.Operator)
	}
	if r.WindowSeconds <= 0 {
		r.WindowSeconds = 3600
	}
	if r.WindowSeconds > 24*3600*30 {
		return fmt.Errorf("window_seconds too large (max 30 days)")
	}
	return nil
}

func sortedMetricNames() string {
	names := make([]string, 0, len(MetricDescriptions))
	for name := range MetricDescriptions {
		names = append(names, name)
	}
	sort.Strings(names)
	out := ""
	for i, name := range names {
		if i > 0 {
			out += ", "
		}
		out += name
	}
	return out
}

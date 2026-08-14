package alerts

import (
	"context"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
	"github.com/lan/meta-gateway/internal/webhook"
)

// recordingNotifier captures SendAlert invocations.
type recordingNotifier struct {
	alerts []string
}

func (n *recordingNotifier) SendAlert(_ context.Context, _ webhook.AlertLevel, _ string, _ string) bool {
	n.alerts = append(n.alerts, "alert")
	return true
}

// deliverable is the subset of the notifier the evaluator needs.
type deliverable interface {
	SendAlert(ctx context.Context, level, title, message string) bool
}

func TestOperatorEvaluation(t *testing.T) {
	cases := []struct {
		op        string
		value     float64
		threshold float64
		want      bool
	}{
		{"gt", 0.9, 0.8, true},
		{"gt", 0.8, 0.8, false},
		{"gte", 0.8, 0.8, true},
		{"lt", 0.7, 0.8, true},
		{"lte", 0.8, 0.8, true},
		{"eq", 0.5, 0.5, true},
		{"neq", 0.5, 0.6, true},
		{"gt", 0.5, 0.8, false},
	}
	for _, c := range cases {
		if got := evalOperator(c.op, c.value, c.threshold); got != c.want {
			t.Fatalf("evalOperator(%s, %v, %v) = %v, want %v", c.op, c.value, c.threshold, got, c.want)
		}
	}
}

func TestValidateRule(t *testing.T) {
	ok := &store.AlertRule{Name: "r", Metric: MetricRequestFailRate, Operator: "gt", Threshold: 0.1}
	if err := ValidateRule(ok); err != nil {
		t.Fatalf("valid rule rejected: %v", err)
	}
	if err := ValidateRule(&store.AlertRule{Name: "", Metric: MetricRequestFailRate, Operator: "gt"}); err == nil {
		t.Fatal("empty name accepted")
	}
	if err := ValidateRule(&store.AlertRule{Name: "r", Metric: "no_such_metric", Operator: "gt"}); err == nil {
		t.Fatal("unknown metric accepted")
	}
	if err := ValidateRule(&store.AlertRule{Name: "r", Metric: MetricRequestFailRate, Operator: "~"}); err == nil {
		t.Fatal("unknown operator accepted")
	}
}

// TestTickFiresAfterSustained: rule fires only after sustained ticks and
// respects the cooldown.
func TestTickFiresAfterSustained(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Seed the fail-rate metric: one failed + one ok request inside the window.
	fail := &domain.ProxyLog{RequestID: "a-fail", ChannelID: 1, Status: 500, ErrorBrief: "upstream_status_500"}
	ok := &domain.ProxyLog{RequestID: "a-ok", ChannelID: 1, Status: 200}
	if _, err := db.ProxyLog.Insert(fail); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ProxyLog.Insert(ok); err != nil {
		t.Fatal(err)
	}
	if err := db.AlertRule.Upsert(&store.AlertRule{
		Name: "fail rate", Metric: MetricRequestFailRate, Operator: "gt",
		Threshold: 0.1, WindowSeconds: 3600, SustainedSeconds: 120, CooldownSeconds: 900,
		Level: "warning", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	rec := &recordingNotifier{}
	s := New(db, nil)
	s.notifier = rec
	base := time.Now()
	s.now = func() time.Time { return base }
	ctx := context.Background()
	// First tick: condition true but not sustained yet.
	s.Tick(ctx)
	if len(rec.alerts) != 0 {
		t.Fatalf("alert fired before sustained: %d", len(rec.alerts))
	}
	// Second tick still before sustained (60s < 120s).
	s.now = func() time.Time { return base.Add(60 * time.Second) }
	s.Tick(ctx)
	if len(rec.alerts) != 0 {
		t.Fatalf("alert fired before sustained (2nd tick): %d", len(rec.alerts))
	}
	// Third tick crosses the sustained window.
	s.now = func() time.Time { return base.Add(120 * time.Second) }
	s.Tick(ctx)
	if len(rec.alerts) != 1 {
		t.Fatalf("alert did not fire after sustained: %d", len(rec.alerts))
	}
	// Condition still true but cooldown (900s) not elapsed: no repeat.
	s.now = func() time.Time { return base.Add(180 * time.Second) }
	s.Tick(ctx)
	if len(rec.alerts) != 1 {
		t.Fatalf("alert repeated inside cooldown: %d", len(rec.alerts))
	}
	// After the cooldown, it may fire again.
	s.now = func() time.Time { return base.Add(1800 * time.Second) }
	s.Tick(ctx)
	if len(rec.alerts) != 2 {
		t.Fatalf("alert did not re-fire after cooldown: %d", len(rec.alerts))
	}
	// Condition clears → sustained resets.
	if _, err := db.Exec(`DELETE FROM proxy_logs WHERE request_id = 'a-fail'`); err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return base.Add(2000 * time.Second) }
	s.Tick(ctx)
	if len(rec.alerts) != 2 {
		t.Fatalf("alert fired after condition cleared: %d", len(rec.alerts))
	}
}

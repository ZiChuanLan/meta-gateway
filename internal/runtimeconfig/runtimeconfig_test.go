package runtimeconfig

import (
	"context"
	"errors"
	"io"
	"log"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/checkin"
	"github.com/lan/meta-gateway/internal/config"
	"github.com/lan/meta-gateway/internal/store"
)

// stubRunner satisfies checkin.BatchRunner so a real scheduler can be driven
// from a test without touching any upstream.
type stubRunner struct{ calls int }

func (r *stubRunner) RunAll(context.Context, string) (*checkin.RunSummary, error) {
	r.calls++
	return &checkin.RunSummary{}, nil
}

func TestValidateBoundsAndCron(t *testing.T) {
	if err := Validate(Editable{RetryTimes: 1, CooldownSeconds: 1, CheckinCron: "0 8 * * *", StableFirstDenominator: 25, StableFirstPromoteRequests: 100, RoutingConcurrencyLimit: 64, WebhookThrottleSeconds: 300, DefaultModelSyncMode: "manual"}); err != nil {
		t.Fatal(err)
	}
	if err := Validate(Editable{RetryTimes: -1, CheckinCron: "0 8 * * *", DefaultModelSyncMode: "manual"}); err == nil {
		t.Fatal("expected retry bounds error")
	}
	if err := Validate(Editable{RetryTimes: 1, CheckinCron: "not a cron", DefaultModelSyncMode: "manual"}); err == nil {
		t.Fatal("expected cron error")
	}
	// Discovery cron: empty = disabled (valid); malformed = rejected.
	if err := Validate(Editable{RetryTimes: 1, CheckinCron: "0 8 * * *", DiscoveryCron: "0 3 * * *", StableFirstDenominator: 25, StableFirstPromoteRequests: 100, RoutingConcurrencyLimit: 64, WebhookThrottleSeconds: 300, DefaultModelSyncMode: "auto"}); err != nil {
		t.Fatalf("valid discovery cron rejected: %v", err)
	}
	if err := Validate(Editable{RetryTimes: 1, CheckinCron: "0 8 * * *", DiscoveryCron: "nope", StableFirstDenominator: 25, StableFirstPromoteRequests: 100, RoutingConcurrencyLimit: 64, WebhookThrottleSeconds: 300, DefaultModelSyncMode: "manual"}); err == nil {
		t.Fatal("expected discovery cron error")
	}
	// New-channel default sync mode: only auto|manual accepted.
	if err := Validate(Editable{RetryTimes: 1, CheckinCron: "0 8 * * *", DefaultModelSyncMode: "sometimes"}); err == nil {
		t.Fatal("expected default_model_sync_mode error")
	}
}

func TestBootstrapUsesEnvironmentWithoutOverride(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cfg := &config.Config{
		HTTPAddr:           ":4100",
		DataDir:            "./data",
		RetryTimes:         3,
		Cooldown:           15 * time.Second,
		CheckinEnabled:     false,
		CheckinCron:        "0 9 * * *",
		RelayRatePerMinute: 10,
		RelayRateBurst:     2,
		AdminRatePerMinute: 5,
		AdminRateBurst:     1,
		AuditRetentionDays: 30,
		AuditRetentionRows: 1000,
	}
	controller := New(cfg, db.RuntimeSettings, Appliers{})
	if err := controller.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	snap := controller.Snapshot()
	if snap.Source != "environment" || snap.Editable.RetryTimes != 3 || snap.HasOverride {
		t.Fatalf("snapshot=%+v", snap)
	}
}

func TestBootstrapRestoresProxyURLOverride(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cfg := &config.Config{
		HTTPAddr:                   ":4100",
		DataDir:                    "./data",
		RetryTimes:                 3,
		Cooldown:                   15 * time.Second,
		CheckinEnabled:             false,
		CheckinCron:                "0 9 * * *",
		RelayRatePerMinute:         10,
		RelayRateBurst:             2,
		AdminRatePerMinute:         5,
		AdminRateBurst:             1,
		AuditRetentionDays:         30,
		AuditRetentionRows:         1000,
		StableFirstDenominator:     25,
		StableFirstPromoteRequests: 100,
		RoutingConcurrencyLimit:    10,
		WebhookThrottleSeconds:     60,
	}
	// Persist an override row with a proxy URL, then simulate a restart: a
	// fresh controller must restore it from the store.
	controller := New(cfg, db.RuntimeSettings, Appliers{})
	if err := controller.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	editable := controller.Snapshot().Editable
	editable.ProxyURL = "http://127.0.0.1:7897"
	if _, err := controller.Update(editable); err != nil {
		t.Fatal(err)
	}
	restarted := New(cfg, db.RuntimeSettings, Appliers{})
	if err := restarted.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	snap := restarted.Snapshot()
	if snap.Editable.ProxyURL != "http://127.0.0.1:7897" {
		t.Fatalf("proxy_url lost across restart: %q", snap.Editable.ProxyURL)
	}
	if snap.Source != "admin_override" {
		t.Fatalf("source = %s", snap.Source)
	}
}

func TestUpdateRollsBackDurableAndRuntimeStateWhenApplyFails(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cfg := &config.Config{
		HTTPAddr:                    ":4100",
		DataDir:                     "./data",
		RetryTimes:                  2,
		CrossChannelFailoverEnabled: true,
		Cooldown:                    30 * time.Second,
		CheckinCron:                 "0 8 * * *",
		StableFirstDenominator:      25,
		StableFirstPromoteRequests:  100,
		RoutingConcurrencyLimit:     64,
		WebhookThrottleSeconds:      300,
	}
	var applied []string
	controller := New(cfg, db.RuntimeSettings, Appliers{
		SetGlobalProxy: func(raw string) error {
			applied = append(applied, raw)
			if raw == "http://reject.example" {
				return errors.New("rejected proxy")
			}
			return nil
		},
	})
	if err := controller.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	next := controller.Snapshot().Editable
	next.RetryTimes = 5
	next.ProxyURL = "http://reject.example"
	if _, err := controller.Update(next); err == nil {
		t.Fatal("expected runtime applier failure")
	}
	snapshot := controller.Snapshot()
	if snapshot.Source != "environment" || snapshot.HasOverride || snapshot.Editable.RetryTimes != 2 || snapshot.Editable.ProxyURL != "" {
		t.Fatalf("runtime state was not rolled back: %+v", snapshot)
	}
	persisted, err := db.RuntimeSettings.Get()
	if err != nil {
		t.Fatal(err)
	}
	if persisted.HasOverride {
		t.Fatalf("durable override was not rolled back: %+v", persisted)
	}
	if len(applied) != 3 || applied[0] != "" || applied[1] != "http://reject.example" || applied[2] != "" {
		t.Fatalf("unexpected apply/rollback sequence: %#v", applied)
	}
}

func TestUpdateAndClearOverride(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cfg := &config.Config{
		HTTPAddr:                    ":4100",
		DataDir:                     "./data",
		RetryTimes:                  2,
		CrossChannelFailoverEnabled: true,
		Cooldown:                    30 * time.Second,
		CheckinEnabled:              false,
		CheckinCron:                 "0 8 * * *",
		RelayRatePerMinute:          600,
		RelayRateBurst:              100,
		AdminRatePerMinute:          300,
		AdminRateBurst:              50,
		AuditRetentionDays:          90,
		AuditRetentionRows:          100000,
	}
	var auditDays, auditRows int
	controller := New(cfg, db.RuntimeSettings, Appliers{
		SetAudit: func(days, rows int) {
			auditDays, auditRows = days, rows
		},
	})
	if err := controller.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	next := Editable{
		RetryTimes:                  5,
		CrossChannelFailoverEnabled: false,
		CooldownSeconds:             60,
		CheckinEnabled:              true,
		CheckinCron:                 "15 7 * * 1-5",
		RelayRatePerMinute:          100,
		RelayRateBurst:              10,
		AdminRatePerMinute:          50,
		AdminRateBurst:              5,
		AuditRetentionDays:          7,
		AuditRetentionRows:          500,
		StableFirstDenominator:      25,
		StableFirstPromoteRequests:  100,
		RoutingConcurrencyLimit:     64,
		WebhookThrottleSeconds:      300,
		StickyEnabled:               true,
		StickyTTLMinutes:            60,
		HealthSweepEnabled:          true,
		HealthSweepIntervalSeconds:  120,
		HealthSweepJitterSeconds:    5,
		HealthSweepDegradedMs:       1000,
		HealthSweepConcurrency:      2,
		HealthSweepTimeoutSeconds:   10,
		ChannelRetryTimes:           3,
		KeyPoolRotation:             false,
		DefaultModelSyncMode:        "auto",
	}
	snap, err := controller.Update(next)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Source != "admin_override" || !snap.HasOverride || snap.Editable.RetryTimes != 5 || snap.Editable.CrossChannelFailoverEnabled {
		t.Fatalf("after update=%+v", snap)
	}
	if auditDays != 7 || auditRows != 500 {
		t.Fatalf("audit not applied days=%d rows=%d", auditDays, auditRows)
	}
	persisted, err := db.RuntimeSettings.Get()
	if err != nil {
		t.Fatal(err)
	}
	if persisted.StickyEnabled != 1 || persisted.StickyTTLMinutes != 60 ||
		persisted.HealthSweepEnabled != 1 || persisted.HealthSweepIntervalSeconds != 120 ||
		persisted.HealthSweepJitterSeconds != 5 || persisted.HealthSweepDegradedMs != 1000 ||
		persisted.HealthSweepConcurrency != 2 || persisted.HealthSweepTimeoutSeconds != 10 ||
		persisted.ChannelRetryTimes != 3 || persisted.KeyPoolRotation != 0 ||
		persisted.DefaultModelSyncMode != "auto" {
		t.Fatalf("runtime policy persistence mismatch: %+v", persisted)
	}
	cleared, err := controller.ClearOverride()
	if err != nil {
		t.Fatal(err)
	}
	if cleared.Source != "environment" || cleared.HasOverride || cleared.Editable.RetryTimes != 2 || !cleared.Editable.CrossChannelFailoverEnabled {
		t.Fatalf("after clear=%+v", cleared)
	}
	if cleared.Editable.DefaultModelSyncMode != "manual" {
		t.Fatalf("cleared default_model_sync_mode = %q, want manual", cleared.Editable.DefaultModelSyncMode)
	}
}

// The check-in surface is built in, so the only thing standing between the
// settings checkbox and a live schedule is the applier: it must arm and disarm
// the real scheduler as the flag moves.
func TestCheckinScheduleFollowsEditableFlag(t *testing.T) {
	cfg := &config.Config{CheckinCron: "0 8 * * *", HTTPAddr: ":0", DataDir: "."}
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	scheduler, err := checkin.NewScheduler(&stubRunner{}, "0 8 * * *", log.New(io.Discard, "", 0), time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = scheduler.Stop(context.Background()) })

	controller := New(cfg, db.RuntimeSettings, Appliers{CheckinSched: scheduler})
	if err := controller.applyWithError(Editable{CheckinEnabled: true, CheckinCron: "0 9 * * *"}); err != nil {
		t.Fatal(err)
	}
	if !scheduler.Started() {
		t.Fatal("enabling check-in must arm the scheduler")
	}
	if got := scheduler.Expression(); got != "0 9 * * *" {
		t.Fatalf("scheduler expression = %q, want the edited cron", got)
	}
	if err := controller.applyWithError(Editable{CheckinEnabled: false, CheckinCron: "0 9 * * *"}); err != nil {
		t.Fatal(err)
	}
	if scheduler.Started() {
		t.Fatal("clearing the check-in flag must disarm the scheduler")
	}
}

// The console's schedule write must outlive the process that made it: an
// update is exactly a container swap, and compose defaults CHECKIN_ENABLED to
// false, so a schedule that only lived in memory (or only in the environment)
// would come back off. Pinning a restart here is what separates "the update
// erased my check-in" from a schedule that was never stored in the first place.
func TestCheckinScheduleSurvivesRestart(t *testing.T) {
	cfg := &config.Config{
		HTTPAddr:                    ":4100",
		DataDir:                     "./data",
		RetryTimes:                  2,
		CrossChannelFailoverEnabled: true,
		Cooldown:                    30 * time.Second,
		CheckinEnabled:              false,
		CheckinCron:                 "0 8 * * *",
		StableFirstDenominator:      25,
		StableFirstPromoteRequests:  100,
		RoutingConcurrencyLimit:     64,
		WebhookThrottleSeconds:      300,
	}
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	newScheduler := func() *checkin.Scheduler {
		t.Helper()
		scheduler, err := checkin.NewScheduler(&stubRunner{}, cfg.CheckinCron, log.New(io.Discard, "", 0), time.UTC)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = scheduler.Stop(context.Background()) })
		return scheduler
	}

	first := newScheduler()
	controller := New(cfg, db.RuntimeSettings, Appliers{CheckinSched: first})
	if err := controller.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	if first.Started() {
		t.Fatal("the environment default must not arm the scheduler")
	}
	next := controller.Snapshot().Editable
	next.CheckinEnabled = true
	next.CheckinCron = "0 9 * * *"
	if _, err := controller.Update(next); err != nil {
		t.Fatal(err)
	}

	// The process stops here — a container swap replaces it with a new one that
	// reads the same database.
	second := newScheduler()
	restarted := New(cfg, db.RuntimeSettings, Appliers{CheckinSched: second})
	if err := restarted.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	if !second.Started() || second.Expression() != "0 9 * * *" {
		t.Fatalf("schedule lost across restart: started=%v cron=%q", second.Started(), second.Expression())
	}
	snapshot := restarted.Snapshot()
	if snapshot.Source != "admin_override" || !snapshot.HasOverride || !snapshot.Editable.CheckinEnabled || snapshot.Editable.CheckinCron != "0 9 * * *" {
		t.Fatalf("restart did not restore the override: %+v", snapshot)
	}
}

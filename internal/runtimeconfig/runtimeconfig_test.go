package runtimeconfig

import (
	"errors"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/config"
	"github.com/lan/meta-gateway/internal/store"
)

type fakeSched struct {
	cron    string
	enabled bool
	calls   int
}

func (f *fakeSched) SetSchedule(expr string, enabled bool) error {
	f.calls++
	f.cron = expr
	f.enabled = enabled
	return nil
}

// schedulerAdapter satisfies the concrete *checkin.Scheduler slot via a thin wrapper in tests
// by using Appliers.CheckinAllowed only — schedule applier needs real type. We test Validate
// and CheckinAllowed gating through a custom apply path with nil CheckinSched plus allowed flag.

func TestValidateBoundsAndCron(t *testing.T) {
	if err := Validate(Editable{RetryTimes: 1, CooldownSeconds: 1, CheckinCron: "0 8 * * *", StableFirstDenominator: 25, StableFirstPromoteRequests: 100, RoutingConcurrencyLimit: 64, WebhookThrottleSeconds: 300}); err != nil {
		t.Fatal(err)
	}
	if err := Validate(Editable{RetryTimes: -1, CheckinCron: "0 8 * * *"}); err == nil {
		t.Fatal("expected retry bounds error")
	}
	if err := Validate(Editable{RetryTimes: 1, CheckinCron: "not a cron"}); err == nil {
		t.Fatal("expected cron error")
	}
	// Discovery cron: empty = disabled (valid); malformed = rejected.
	if err := Validate(Editable{RetryTimes: 1, CheckinCron: "0 8 * * *", DiscoveryCron: "0 3 * * *", StableFirstDenominator: 25, StableFirstPromoteRequests: 100, RoutingConcurrencyLimit: 64, WebhookThrottleSeconds: 300}); err != nil {
		t.Fatalf("valid discovery cron rejected: %v", err)
	}
	if err := Validate(Editable{RetryTimes: 1, CheckinCron: "0 8 * * *", DiscoveryCron: "nope", StableFirstDenominator: 25, StableFirstPromoteRequests: 100, RoutingConcurrencyLimit: 64, WebhookThrottleSeconds: 300}); err == nil {
		t.Fatal("expected discovery cron error")
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
		persisted.ChannelRetryTimes != 3 || persisted.KeyPoolRotation != 0 {
		t.Fatalf("runtime policy persistence mismatch: %+v", persisted)
	}
	cleared, err := controller.ClearOverride()
	if err != nil {
		t.Fatal(err)
	}
	if cleared.Source != "environment" || cleared.HasOverride || cleared.Editable.RetryTimes != 2 || !cleared.Editable.CrossChannelFailoverEnabled {
		t.Fatalf("after clear=%+v", cleared)
	}
}

func TestCheckinAllowedGatesEnablement(t *testing.T) {
	// Exercise the gate branch used by applyWithError without a real scheduler.
	allowed := false
	appliers := Appliers{
		CheckinAllowed: func() bool { return allowed },
	}
	// Directly call applyWithError through a controller with nil scheduler.
	cfg := &config.Config{CheckinCron: "0 8 * * *", HTTPAddr: ":0", DataDir: "."}
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	controller := New(cfg, db.RuntimeSettings, appliers)
	if err := controller.applyWithError(Editable{CheckinEnabled: true, CheckinCron: "0 8 * * *"}); err != nil {
		t.Fatal(err)
	}
}

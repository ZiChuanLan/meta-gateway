package runtimeconfig

import (
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
	if err := Validate(Editable{RetryTimes: 1, CooldownSeconds: 1, CheckinCron: "0 8 * * *"}); err != nil {
		t.Fatal(err)
	}
	if err := Validate(Editable{RetryTimes: -1, CheckinCron: "0 8 * * *"}); err == nil {
		t.Fatal("expected retry bounds error")
	}
	if err := Validate(Editable{RetryTimes: 1, CheckinCron: "not a cron"}); err == nil {
		t.Fatal("expected cron error")
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

func TestUpdateAndClearOverride(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cfg := &config.Config{
		HTTPAddr:           ":4100",
		DataDir:            "./data",
		RetryTimes:         2,
		Cooldown:           30 * time.Second,
		CheckinEnabled:     false,
		CheckinCron:        "0 8 * * *",
		RelayRatePerMinute: 600,
		RelayRateBurst:     100,
		AdminRatePerMinute: 300,
		AdminRateBurst:     50,
		AuditRetentionDays: 90,
		AuditRetentionRows: 100000,
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
		RetryTimes:         5,
		CooldownSeconds:    60,
		CheckinEnabled:     true,
		CheckinCron:        "15 7 * * 1-5",
		RelayRatePerMinute: 100,
		RelayRateBurst:     10,
		AdminRatePerMinute: 50,
		AdminRateBurst:     5,
		AuditRetentionDays: 7,
		AuditRetentionRows: 500,
	}
	snap, err := controller.Update(next)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Source != "admin_override" || !snap.HasOverride || snap.Editable.RetryTimes != 5 {
		t.Fatalf("after update=%+v", snap)
	}
	if auditDays != 7 || auditRows != 500 {
		t.Fatalf("audit not applied days=%d rows=%d", auditDays, auditRows)
	}
	cleared, err := controller.ClearOverride()
	if err != nil {
		t.Fatal(err)
	}
	if cleared.Source != "environment" || cleared.HasOverride || cleared.Editable.RetryTimes != 2 {
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

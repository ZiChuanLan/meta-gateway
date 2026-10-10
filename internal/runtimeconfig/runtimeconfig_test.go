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

func TestValidateSiteProbeCadence(t *testing.T) {
	base := func(interval, jitter int) Editable {
		return Editable{
			RetryTimes: 1, CooldownSeconds: 1, CheckinCron: "0 8 * * *",
			StableFirstDenominator: 25, StableFirstPromoteRequests: 100,
			RoutingConcurrencyLimit: 64, WebhookThrottleSeconds: 300,
			DefaultModelSyncMode:     "manual",
			SiteProbeIntervalSeconds: interval, SiteProbeJitterSeconds: jitter,
		}
	}
	if err := Validate(base(300, 30)); err != nil {
		t.Fatalf("valid cadence rejected: %v", err)
	}
	// Zero means "unset": an older console build that does not know these fields
	// would otherwise fail the whole settings save.
	if err := Validate(base(0, 0)); err != nil {
		t.Fatalf("unset cadence rejected: %v", err)
	}
	// Below a minute would hammer a public page for data it has not published.
	if err := Validate(base(30, 0)); err == nil {
		t.Fatal("expected the interval floor to be enforced")
	}
	if err := Validate(base(900, 4000)); err == nil {
		t.Fatal("expected the jitter ceiling to be enforced")
	}
	if err := Validate(base(300, 600)); err == nil {
		t.Fatal("expected jitter > interval to be rejected")
	}
}

func TestBootstrapUsesEnvironmentWithoutOverride(t *testing.T) {
	db, err := store.OpenTest(t.TempDir())
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
	db, err := store.OpenTest(t.TempDir())
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
	db, err := store.OpenTest(t.TempDir())
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
	db, err := store.OpenTest(t.TempDir())
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
		SiteProbeIntervalSeconds:    900,
		SiteProbeJitterSeconds:      120,
	}
	var auditDays, auditRows int
	var probeInterval, probeJitter time.Duration
	controller := New(cfg, db.RuntimeSettings, Appliers{
		SetAudit: func(days, rows int) {
			auditDays, auditRows = days, rows
		},
		SetSiteProbeSchedule: func(interval, jitter time.Duration) {
			probeInterval, probeJitter = interval, jitter
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
		DefaultModelSyncMode:        "auto",
		// A site-probe cadence change is the point of this setting: the loop must
		// receive it (applier) and survive a restart (store row).
		SiteProbeIntervalSeconds: 300,
		SiteProbeJitterSeconds:   15,
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
	if probeInterval != 300*time.Second || probeJitter != 15*time.Second {
		t.Fatalf("site probe cadence not applied: %v/%v", probeInterval, probeJitter)
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
		persisted.DefaultModelSyncMode != "auto" ||
		persisted.SiteProbeIntervalSeconds != 300 || persisted.SiteProbeJitterSeconds != 15 {
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
	// Clearing the override must hand the cadence back to the env bootstrap, not
	// leave the loop on the override value.
	if cleared.Editable.SiteProbeIntervalSeconds != 900 || cleared.Editable.SiteProbeJitterSeconds != 120 {
		t.Fatalf("cleared site probe cadence = %d/%d, want 900/120",
			cleared.Editable.SiteProbeIntervalSeconds, cleared.Editable.SiteProbeJitterSeconds)
	}
	if probeInterval != 900*time.Second || probeJitter != 120*time.Second {
		t.Fatalf("cadence after clear = %v/%v, want 15m/2m", probeInterval, probeJitter)
	}
}

// The check-in surface is built in, so the only thing standing between the
// settings checkbox and a live schedule is the applier: it must arm and disarm
// the real scheduler as the flag moves.
func TestCheckinScheduleFollowsEditableFlag(t *testing.T) {
	cfg := &config.Config{CheckinCron: "0 8 * * *", HTTPAddr: ":0", DataDir: "."}
	db, err := store.OpenTest(t.TempDir())
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
	db, err := store.OpenTest(t.TempDir())
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

// The outbound limits are the one knob an operator reaches for during an
// incident, so the applier has to carry three things at once: a non-zero value
// becomes the live limit, a zero falls back to the deployment value (not to
// "no timeout"), and clearing the override hands the deployment values back.
func TestOutboundLimitsFollowEditableAndFallBackToEnv(t *testing.T) {
	db, err := store.OpenTest(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cfg := &config.Config{
		AdminToken:                    "admin-test",
		MetricsToken:                  "metrics-test",
		MaxAdminBodyBytes:             1 << 20,
		RetryTimes:                    2,
		CrossChannelFailoverEnabled:   true,
		Cooldown:                      30 * time.Second,
		StableFirstDenominator:        25,
		StableFirstPromoteRequests:    100,
		RoutingConcurrencyLimit:       64,
		OutboundConnectTimeout:        10 * time.Second,
		OutboundResponseHeaderTimeout: 60 * time.Second,
		OutboundImageHeaderTimeout:    300 * time.Second,
		OutboundTLSHandshakeTimeout:   10 * time.Second,
		OutboundMaxIdleConns:          512,
		OutboundMaxIdleConnsPerHost:   64,
	}
	var applied OutboundLimits
	controller := New(cfg, db.RuntimeSettings, Appliers{
		SetOutboundLimits: func(limits OutboundLimits) { applied = limits },
	})
	if err := controller.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	// Bootstrap applies the deployment values.
	if applied.HeaderTimeout != 60*time.Second || applied.ImageHeaderTimeout != 300*time.Second {
		t.Fatalf("bootstrap limits = %+v, want the deployment values", applied)
	}

	// An override, with the image ceiling left at 0 (deployment default). The
	// baseline mirrors a valid full settings document; only the outbound fields
	// are the subject here.
	next := Editable{
		RetryTimes:                   5,
		CrossChannelFailoverEnabled:  false,
		CooldownSeconds:              60,
		CheckinEnabled:               true,
		CheckinCron:                  "15 7 * * 1-5",
		RelayRatePerMinute:           100,
		RelayRateBurst:               10,
		AdminRatePerMinute:           50,
		AdminRateBurst:               5,
		AuditRetentionDays:           7,
		AuditRetentionRows:           500,
		StableFirstDenominator:       25,
		StableFirstPromoteRequests:   100,
		RoutingConcurrencyLimit:      64,
		WebhookThrottleSeconds:       300,
		StickyEnabled:                true,
		StickyTTLMinutes:             60,
		HealthSweepEnabled:           true,
		HealthSweepIntervalSeconds:   120,
		HealthSweepJitterSeconds:     5,
		HealthSweepDegradedMs:        1000,
		HealthSweepConcurrency:       2,
		HealthSweepTimeoutSeconds:    10,
		ChannelRetryTimes:            3,
		DefaultModelSyncMode:         "auto",
		SiteProbeIntervalSeconds:     300,
		SiteProbeJitterSeconds:       15,
		OutboundHeaderTimeoutSeconds: 180,
		OutboundMaxIdleConnsPerHost:  256,
	}
	snap, err := controller.Update(next)
	if err != nil {
		t.Fatal(err)
	}
	if applied.HeaderTimeout != 180*time.Second {
		t.Fatalf("header timeout = %v, want 3m", applied.HeaderTimeout)
	}
	if applied.ImageHeaderTimeout != 300*time.Second {
		t.Fatalf("image header timeout = %v, want the deployment 5m (0 means default, not no ceiling)",
			applied.ImageHeaderTimeout)
	}
	if applied.MaxIdleConnsPerHost != 256 {
		t.Fatalf("per-host idle conns = %d, want 256", applied.MaxIdleConnsPerHost)
	}
	if applied.MaxIdleConns != 512 || applied.ConnectTimeout != 10*time.Second {
		t.Fatalf("untouched limits drifted: %+v", applied)
	}
	// The console shows the effective values, not the stored zero.
	if snap.Editable.OutboundImageHeaderTimeoutSeconds != 300 {
		t.Fatalf("console shows image ceiling %d, want the effective 300",
			snap.Editable.OutboundImageHeaderTimeoutSeconds)
	}

	cleared, err := controller.ClearOverride()
	if err != nil {
		t.Fatal(err)
	}
	if applied.HeaderTimeout != 60*time.Second || applied.MaxIdleConnsPerHost != 64 {
		t.Fatalf("after clear = %+v, want the deployment values", applied)
	}
	if cleared.Editable.OutboundHeaderTimeoutSeconds != 60 {
		t.Fatalf("cleared console value = %d, want 60", cleared.Editable.OutboundHeaderTimeoutSeconds)
	}
}

// The body ceilings are runtime settings for the same reason the outbound
// limits are: the number decides whether an ordinary request is refused, and it
// has to be changeable without recreating the container. 0 stays "no override"
// and must resolve to the deployment bytes, never to a zero ceiling.
func TestRelayBodyLimitsHotReload(t *testing.T) {
	db, err := store.OpenTest(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cfg := &config.Config{
		AdminToken:         "admin-test",
		MetricsToken:       "metrics-test",
		MaxAdminBodyBytes:  1 << 20,
		RelayMaxBodyBytes:  32 << 20,
		RelayMaxImageBytes: 64 << 20,
	}
	var appliedJSON, appliedImage int64
	applied := 0
	controller := New(cfg, db.RuntimeSettings, Appliers{
		SetRelayBodyLimits: func(jsonBytes, imageBytes int64) {
			appliedJSON, appliedImage = jsonBytes, imageBytes
			applied++
		},
	})
	if err := controller.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	if appliedJSON != 32<<20 || appliedImage != 64<<20 {
		t.Fatalf("bootstrap ceilings = %d/%d, want the deployment 32/64 MB", appliedJSON, appliedImage)
	}

	// A minimal config leaves fields Validate insists on (a denominator, a pool
	// limit, a throttle) at zero, so the baseline is the console's own document
	// with those filled in — the subject here is the two ceilings.
	next := controller.Snapshot().Editable
	next.StableFirstDenominator = 25
	next.StableFirstPromoteRequests = 100
	next.RoutingConcurrencyLimit = 64
	next.WebhookThrottleSeconds = 300
	next.StickyTTLMinutes = 60
	next.RelayMaxBodyMB = 96
	next.RelayMaxImageMB = 0 // untouched: the deployment value must still win
	snap, err := controller.Update(next)
	if err != nil {
		t.Fatal(err)
	}
	if appliedJSON != 96<<20 {
		t.Fatalf("json ceiling = %d, want the saved 96 MB", appliedJSON)
	}
	if appliedImage != 64<<20 {
		t.Fatalf("image ceiling = %d, want the deployment 64 MB (0 is not a ceiling)", appliedImage)
	}
	// The console reads back the effective values, so a later save re-sends what
	// is actually in force instead of a stored zero.
	if snap.Editable.RelayMaxBodyMB != 96 || snap.Editable.RelayMaxImageMB != 64 {
		t.Fatalf("console values = %d/%d, want 96/64",
			snap.Editable.RelayMaxBodyMB, snap.Editable.RelayMaxImageMB)
	}
	if snap.EnvBootstrap.RelayMaxBodyMB != 32 {
		t.Fatalf("env bootstrap = %d, want the deployment 32", snap.EnvBootstrap.RelayMaxBodyMB)
	}

	// Clearing the override goes back to the deployment bytes.
	cleared, err := controller.ClearOverride()
	if err != nil {
		t.Fatal(err)
	}
	if appliedJSON != 32<<20 || appliedImage != 64<<20 {
		t.Fatalf("after clear = %d/%d, want the deployment 32/64 MB", appliedJSON, appliedImage)
	}
	if cleared.Editable.RelayMaxBodyMB != 32 {
		t.Fatalf("cleared console value = %d, want 32", cleared.Editable.RelayMaxBodyMB)
	}

	// Out of range is refused before anything is applied or stored.
	tooBig := cleared.Editable
	tooBig.RelayMaxBodyMB = 4096
	before := applied
	if _, err := controller.Update(tooBig); err == nil {
		t.Fatal("a 4 GB ceiling must be refused")
	}
	if applied != before {
		t.Fatal("a refused save must not reach the applier")
	}
}

// megabytesOrZero is the bridge between the byte ceilings the process starts
// with and the whole megabytes the console speaks. Collapsing a positive byte
// value to 0 would read as "no override" and hand the request back to the
// handler's built-in default — the exact silent-limit mistake this setting
// exists to remove.
func TestMegabytesOrZero(t *testing.T) {
	cases := []struct {
		bytes int64
		want  int
	}{
		{0, 0},
		{1, 1},
		{300 << 10, 1},
		{1 << 20, 1},
		{32 << 20, 32},
		{96<<20 + 1, 96},
		{512 << 20, 512},
	}
	for _, tc := range cases {
		if got := megabytesOrZero(tc.bytes); got != tc.want {
			t.Errorf("megabytesOrZero(%d) = %d, want %d", tc.bytes, got, tc.want)
		}
	}
}

// The notice-board cadence differs from the site-probe pair next door: 0 is a
// value the operator picks ("off"), not the unset sentinel, so it has to survive
// a restart and must not be replaced by the env bootstrap.
func TestSiteNewsCadenceOffSurvivesRestart(t *testing.T) {
	cfg := &config.Config{SiteNewsIntervalSeconds: 300, HTTPAddr: ":0", DataDir: "."}
	db, err := store.OpenTest(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	var applied time.Duration
	apply := func(d time.Duration) { applied = d }
	controller := New(cfg, db.RuntimeSettings, Appliers{SetSiteNewsSchedule: apply})
	if err := controller.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	if applied != 300*time.Second {
		t.Fatalf("bootstrap cadence = %v, want the env value 5m", applied)
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
		DefaultModelSyncMode:        "auto",
		SiteProbeIntervalSeconds:    300,
		SiteProbeJitterSeconds:      15,
		SiteNewsIntervalSeconds:     0, // off
	}
	if _, err := controller.Update(next); err != nil {
		t.Fatal(err)
	}
	if applied != 0 {
		t.Fatalf("cadence after switching off = %v, want 0", applied)
	}
	persisted, err := db.RuntimeSettings.Get()
	if err != nil {
		t.Fatal(err)
	}
	if persisted.SiteNewsIntervalSeconds != 0 {
		t.Fatalf("stored cadence = %d, want 0 (off is a value, not unset)", persisted.SiteNewsIntervalSeconds)
	}

	// A restart reads the stored 0 back and must hand 0 to the loop rather than
	// the env bootstrap.
	applied = -1
	restarted := New(cfg, db.RuntimeSettings, Appliers{SetSiteNewsSchedule: apply})
	if err := restarted.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	if applied != 0 {
		t.Fatalf("cadence after restart = %v, want the stored 0", applied)
	}

	// Clearing the override hands the cadence back to the deployment value.
	cleared, err := controller.ClearOverride()
	if err != nil {
		t.Fatal(err)
	}
	if cleared.Editable.SiteNewsIntervalSeconds != 300 {
		t.Fatalf("cleared cadence = %d, want the env 300", cleared.Editable.SiteNewsIntervalSeconds)
	}
	if applied != 300*time.Second {
		t.Fatalf("cadence after clear = %v, want 5m", applied)
	}

	// 0 (off) validates; a cadence under a minute or past a day does not.
	if err := Validate(next); err != nil {
		t.Fatalf("0 (off) failed validation: %v", err)
	}
	tooFast := next
	tooFast.SiteNewsIntervalSeconds = 30
	if err := Validate(tooFast); err == nil {
		t.Fatal("30s passed validation, want an error")
	}
	tooSlow := next
	tooSlow.SiteNewsIntervalSeconds = 90000
	if err := Validate(tooSlow); err == nil {
		t.Fatal("90000s passed validation, want an error")
	}
}

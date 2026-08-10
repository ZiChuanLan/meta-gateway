package healthsweep

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/discovery"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

type fakeProber struct {
	mu    sync.Mutex
	lat   map[int64]int
	errs  map[int64]error
	calls int
}

func (f *fakeProber) Probe(_ context.Context, channelID int64) (*discovery.ProbeResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if err := f.errs[channelID]; err != nil {
		return nil, err
	}
	return &discovery.ProbeResult{ChannelID: channelID, LatencyMs: f.lat[channelID], CheckedAt: time.Now()}, nil
}

func openSweepTestDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestProbeOnceGradesStates(t *testing.T) {
	fake := &fakeProber{lat: map[int64]int{1: 100, 2: 5000}, errs: map[int64]error{3: errors.New("boom")}}
	db := openSweepTestDB(t)
	for _, name := range []string{"fast", "slow", "failed"} {
		if _, err := db.Channel.Create(&domain.Channel{Name: name, Status: domain.StatusEnabled}); err != nil {
			t.Fatal(err)
		}
	}
	svc := New(db, fake, nil, DefaultConfig())

	svc.probeOnce(1)
	svc.probeOnce(2)
	svc.probeOnce(3)

	svc.mu.RLock()
	defer svc.mu.RUnlock()
	if got := svc.status[1].State; got != StateOperational {
		t.Fatalf("channel 1 state=%q want operational", got)
	}
	if got := svc.status[2].State; got != StateDegraded {
		t.Fatalf("channel 2 state=%q want degraded", got)
	}
	if got := svc.status[3].State; got != StateError {
		t.Fatalf("channel 3 state=%q want error", got)
	}
	if svc.status[3].Error == "" {
		t.Fatal("channel 3 must carry a redacted error category")
	}
	overviews, err := db.Channel.ListOverviews(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(overviews) != 3 {
		t.Fatalf("overviews=%d want 3", len(overviews))
	}
	if overviews[1].HealthState != domain.HealthStateDegraded || overviews[1].HealthReason != "probe_slow" {
		t.Fatalf("slow probe was not persisted as degraded: %+v", overviews[1])
	}
}

func TestProbeCategoryRedacts(t *testing.T) {
	if got := probeCategory(&discovery.Error{Kind: discovery.ErrorUpstream, Category: "upstream_unauthorized"}); got != "upstream_unauthorized" {
		t.Fatalf("category=%q", got)
	}
	if got := probeCategory(errors.New("raw network text")); got != "upstream_failure" {
		t.Fatalf("non-discovery error must map to stable category, got %q", got)
	}
	if got := probeCategory(nil); got != "" {
		t.Fatalf("nil error category=%q", got)
	}
}

func TestProbeConcurrencyGateHonorsHotLimit(t *testing.T) {
	svc := New(nil, nil, nil, Config{Concurrency: 1})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if !svc.acquireProbeSlot(ctx, 1) {
		t.Fatal("first probe should acquire the only slot")
	}

	acquired := make(chan struct{})
	go func() {
		if svc.acquireProbeSlot(ctx, 1) {
			close(acquired)
			svc.releaseProbeSlot()
		}
	}()
	select {
	case <-acquired:
		t.Fatal("second probe bypassed concurrency limit")
	case <-time.After(50 * time.Millisecond):
	}

	// A live configuration increase wakes waiters without requiring a restart.
	svc.SetConfig(Config{Concurrency: 2})
	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("concurrency increase did not wake a waiting probe")
	}
	svc.releaseProbeSlot()
}

func TestSetConfigHotSwapsSweep(t *testing.T) {
	db := openSweepTestDB(t)
	siteID, _ := db.Site.Create(&domain.Site{Name: "s", Status: domain.StatusEnabled})
	credID, _ := db.Credential.Create(&domain.Credential{SiteID: siteID, Kind: "api_key", SecretEnc: []byte("enc"), Status: domain.StatusEnabled})
	if _, err := db.Channel.Create(&domain.Channel{SiteID: &siteID, CredentialID: &credID, Name: "c", Status: domain.StatusEnabled}); err != nil {
		t.Fatal(err)
	}
	fake := &fakeProber{lat: map[int64]int{1: 50}}
	// Start disabled (DefaultConfig) — nothing may probe.
	svc := New(db, fake, nil, DefaultConfig())
	svc.Start()
	defer svc.Stop()
	time.Sleep(150 * time.Millisecond)
	if got := svc.Status(); len(got) != 0 {
		t.Fatalf("disabled sweep must not probe, got status %+v", got)
	}
	// Enable with a 1s interval — probes must start.
	svc.SetConfig(Config{Enabled: true, IntervalSeconds: 1, JitterSeconds: 0, DegradedThresholdMs: 2000, Concurrency: 4, TimeoutSeconds: 15})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		status := svc.Status()
		if len(status) > 0 && status[0].State == StateOperational {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if status := svc.Status(); len(status) == 0 || status[0].State != StateOperational {
		t.Fatalf("sweep did not start after SetConfig(enable): %+v", status)
	}
	// Disable again — loops must drain and stop probing.
	svc.SetConfig(DefaultConfig())
	time.Sleep(2 * time.Second)
	before := fake.calls
	time.Sleep(2 * time.Second)
	if fake.calls != before {
		t.Fatalf("sweep kept probing after SetConfig(disable): calls %d -> %d", before, fake.calls)
	}
}

func TestStartStopLifecycle(t *testing.T) {
	db := openSweepTestDB(t)
	siteID, _ := db.Site.Create(&domain.Site{Name: "s", Status: domain.StatusEnabled})
	credID, _ := db.Credential.Create(&domain.Credential{SiteID: siteID, Kind: "api_key", SecretEnc: []byte("enc"), Status: domain.StatusEnabled})
	if _, err := db.Channel.Create(&domain.Channel{SiteID: &siteID, CredentialID: &credID, Name: "c", Status: domain.StatusEnabled}); err != nil {
		t.Fatal(err)
	}
	fake := &fakeProber{lat: map[int64]int{1: 50}}
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.IntervalSeconds = 1
	cfg.JitterSeconds = 0
	svc := New(db, fake, nil, cfg)
	svc.Start()
	defer svc.Stop()
	// Idempotent start.
	svc.Start()

	// Give the loop time to probe (interval 1s + jitter 0).
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		status := svc.Status()
		if len(status) > 0 && status[0].State == StateOperational {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("sweep never probed the enabled channel: %+v", svc.Status())
}

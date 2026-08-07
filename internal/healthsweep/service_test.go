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
	svc := New(openSweepTestDB(t), fake, nil, DefaultConfig())

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

func TestStartStopLifecycle(t *testing.T) {
	db := openSweepTestDB(t)
	siteID, _ := db.Site.Create(&domain.Site{Name: "s", Status: domain.StatusEnabled})
	credID, _ := db.Credential.Create(&domain.Credential{SiteID: siteID, Kind: "api_key", SecretEnc: []byte("enc"), Status: domain.StatusEnabled})
	if _, err := db.Channel.Create(&domain.Channel{SiteID: &siteID, CredentialID: &credID, Name: "c", Status: domain.StatusEnabled}); err != nil {
		t.Fatal(err)
	}
	fake := &fakeProber{lat: map[int64]int{1: 50}}
	cfg := DefaultConfig()
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

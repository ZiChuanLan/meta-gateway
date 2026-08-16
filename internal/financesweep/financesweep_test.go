package financesweep

import (
	"context"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/account"
	"github.com/lan/meta-gateway/internal/webhook"
)

// fakeProber is a FinanceProber stub that counts probe invocations.
type fakeProber struct {
	calls chan struct{}
}

func (f *fakeProber) FinanceOverview(ctx context.Context) ([]account.FinanceItem, error) {
	_ = ctx
	f.calls <- struct{}{}
	return nil, nil
}

func (f *fakeProber) ProbeAll(ctx context.Context) ([]account.ProbeAllItem, error) {
	_ = ctx
	return nil, nil
}

func TestSweepSetIntervalHotReload(t *testing.T) {
	notifier := webhook.New("", 0)
	prober := &fakeProber{calls: make(chan struct{}, 10)}
	sweep := NewSweep(prober, notifier, 0) // disabled initially
	sweep.Start()
	defer sweep.Stop()

	select {
	case <-prober.calls:
		t.Fatal("disabled sweep must not fire")
	case <-time.After(80 * time.Millisecond):
	}

	// Enable with a fast interval; must start ticking without a restart.
	sweep.SetInterval(20 * time.Millisecond)
	select {
	case <-prober.calls:
	case <-time.After(2 * time.Second):
		t.Fatal("sweep did not start after SetInterval")
	}

	// Disable again; must pause.
	sweep.SetInterval(0)
	select {
	case <-prober.calls:
		t.Fatal("sweep must pause when interval <= 0")
	case <-time.After(120 * time.Millisecond):
	}
}

func TestDailySummarySetIntervalFromDisabled(t *testing.T) {
	// A nil-db digest is inert but must tolerate hot reloads.
	summary := NewDailySummary(nil, nil, 0, false)
	summary.Start()
	defer summary.Stop()
	summary.SetInterval(10 * time.Millisecond)
	summary.SetInterval(0)
	summary.SetInterval(time.Hour)
}

package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/config"
	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/store"
)

// ---------------------------------------------------------------------------
// Why this file exists
//
// NewWithDependencies starts a flock of perpetual background schedulers: the
// alert sweep, balance sweep, health sweep, alert rules, daily summary, model
// catalog refresh, discovery scheduler, DB GC, probe schedule, update check and
// the discovery recovery loop. Each registers a stop callback with
// RegisterStopper, and the production shutdown path in cmd/server drains that
// registry through StopBackground.
//
// Tests did not. Every test that built a router left its own schedulers — plus
// those of every earlier test in the package — ticking until the test binary
// exited: 458 live goroutines by the time the package gave up, ~44 leaked
// routers deep.
//
// Under plain `go test` that is merely wasteful (the package still finished in
// ~40s). Under `go test -race` it is fatal, for two compounding reasons: the
// race detector keeps a shadow stack per goroutine, and the leaked tickers
// keep waking up and touching a database their test already closed. The
// package blew its 10-minute -timeout and CI's race step failed on a suite that
// was race-clean — no DATA RACE was ever reported. Measured on one test:
// 2.26s running alone, 4.11s once 400-odd leaked goroutines were live, i.e. the
// leak roughly doubled the cost of everything that ran after the first test.
//
// So: tests must stop what they start. NewTestRouter is the one-line way to do
// that, and the two guards below keep the invariant from decaying again.
// ---------------------------------------------------------------------------

// TestMain is a backstop, not the fix: by the time it runs, the accumulation
// damage is done. It exists to notice a test that built a router with New
// instead of NewTestRouter and left its schedulers registered for the rest of
// the run — the exact condition that made -race time out.
func TestMain(m *testing.M) {
	code := m.Run()

	if pending := pendingStoppers(); pending > 0 {
		fmt.Fprintf(os.Stderr, "\nhttpapi: %d background component(s) were still registered when the "+
			"test binary finished.\n"+
			"A test built a router with New instead of NewTestRouter, so its schedulers outlived it.\n"+
			"Under -race that accumulation is what times the package out. Fix the test.\n%s",
			pending, goroutineDump())
		if code == 0 {
			code = 1
		}
	}

	StopBackground(context.Background())
	os.Exit(code)
}

// NewTestRouter builds the admin router for a test and stops its background
// schedulers when that test ends. Tests must use this instead of New; see the
// file comment for the CI failure that made it necessary.
//
// Exported because the `httpapi_test` files in this directory are a separate
// package that shares this test binary, so they can only call names the
// package exports. It lives in a _test.go file, so it is invisible to
// production builds.
func NewTestRouter(t *testing.T, cfg *config.Config, db *store.DB, enc *crypto.Encrypter) http.Handler {
	t.Helper()
	handler := New(cfg, db, enc)
	// t.Cleanup runs after the test body's own defers, so the test's
	// `defer db.Close()` has already run by the time the schedulers stop.
	// The window where a tick can land on a closed database is therefore
	// microseconds rather than the minutes a leaked scheduler used to spend
	// there, and the bounded context keeps one wedged stopper from hanging
	// the whole binary.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		StopBackground(ctx)
	})
	return handler
}

// TestRouterShutdownReclaimsBackgroundGoroutines pins the invariant the rest of
// the package leans on: a router stopped through StopBackground leaves nothing
// behind. It is what turns "a scheduler was added without a RegisterStopper
// call" from a silent CI timeout into a named failure.
func TestRouterShutdownReclaimsBackgroundGoroutines(t *testing.T) {
	before := runtime.NumGoroutine()

	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	enc, err := crypto.New("background-test-master-key-at-least-32-characters")
	if err != nil {
		t.Fatal(err)
	}

	New(&config.Config{AdminToken: "admin-test", MetricsToken: "metrics-test"}, db, enc)

	started := waitGoroutines(func(n int) bool { return n >= before+2 }, 10*time.Second)
	if started < before+2 {
		t.Fatalf("router start: goroutines stayed at %d (baseline %d); expected the "+
			"schedulers to launch goroutines", started, before)
	}

	// Production ordering: stop the background work before the database goes
	// away, so a shutdown bug cannot hide behind closed-database errors from a
	// scheduler that is still running.
	stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	StopBackground(stopCtx)

	settled := waitGoroutines(func(n int) bool { return n <= before+1 }, 15*time.Second)
	if settled > before+1 {
		t.Fatalf("after StopBackground: %d goroutines, want <= %d (leaked %d).\n"+
			"Something was started without registering a stopper.\n%s",
			settled, before+1, settled-before, goroutineDump())
	}
}

// waitGoroutines polls runtime.NumGoroutine until want is satisfied, returning
// the last count either way. Goroutine counts settle asynchronously — a cron
// entry jitters, a supervisor spawns its workers — so polling beats a single
// sleep-then-assert, which would be flaky in both directions.
func waitGoroutines(want func(int) bool, budget time.Duration) int {
	deadline := time.Now().Add(budget)
	for {
		n := runtime.NumGoroutine()
		if want(n) || time.Now().After(deadline) {
			return n
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// pendingStoppers reports how many stop callbacks are still registered. A
// drained registry is empty, so a non-zero count at process exit means a test
// left a router running.
func pendingStoppers() int {
	lifecycleMu.Lock()
	defer lifecycleMu.Unlock()
	return len(lifecycleStop)
}

// goroutineDump returns the stacks of live goroutines that belong to this
// repository, so a failure names the component that leaked instead of burying
// it under runtime and test-framework frames.
func goroutineDump() string {
	buf := make([]byte, 4<<20)
	n := runtime.Stack(buf, true)
	var b strings.Builder
	for _, block := range strings.Split(string(buf[:n]), "\n\n") {
		if strings.Contains(block, "github.com/lan/meta-gateway/") {
			b.WriteString(block)
			b.WriteString("\n\n")
		}
	}
	return b.String()
}

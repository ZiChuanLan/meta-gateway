package probe

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/proxy"
	"github.com/lan/meta-gateway/internal/relay"
	"github.com/lan/meta-gateway/internal/store"
)

// fakeRelay answers probes from a fixed table instead of calling upstream, and
// records what it was asked so the test can assert on probe semantics.
type fakeRelay struct {
	ok      map[int64]bool
	calls   int
	sawFlag bool
	release chan struct{}

	mu       sync.Mutex
	prompts  []string
	maxToken int
}

// probeBody is the shape the prober marshals. Only the fields the tests assert
// on are decoded.
type probeBody struct {
	Messages []struct {
		Content string `json:"content"`
	} `json:"messages"`
	MaxTokens int `json:"max_tokens"`
}

func (f *fakeRelay) sentPrompt() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.prompts) == 0 {
		return ""
	}
	return f.prompts[0]
}

func (f *fakeRelay) ChatCompletionsWithMeta(_ context.Context, req proxy.Request) (*relay.Result, *proxy.AttemptMeta) {
	f.mu.Lock()
	f.calls++
	f.sawFlag = req.Probe
	release := f.release
	ok := f.ok
	f.mu.Unlock()

	var body probeBody
	if err := json.Unmarshal(req.Body, &body); err == nil && len(body.Messages) > 0 {
		f.mu.Lock()
		f.prompts = append(f.prompts, body.Messages[0].Content)
		f.maxToken = body.MaxTokens
		f.mu.Unlock()
	}

	if release != nil {
		<-release
	}
	status := http.StatusOK
	if !ok[req.PreferChannelID] {
		status = http.StatusBadGateway
	}
	return &relay.Result{StatusCode: status, LatencyMs: 7},
		&proxy.AttemptMeta{ChannelID: req.PreferChannelID}
}

func waitForTask(t *testing.T, db *store.DB, id int64) *store.ProbeTask {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		task, err := db.GetProbeTask(id)
		if err != nil {
			t.Fatalf("get task: %v", err)
		}
		if task != nil && task.Status != store.ProbeTaskRunning {
			return task
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("probe task did not finish in time")
	return nil
}

// A run must mark the probe flag, tally both outcomes, and persist a row per
// pair — the tally is what the UI shows, the rows are what routing reads.
func TestProbeRunRecordsResultsAndHealth(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	fake := &fakeRelay{ok: map[int64]bool{1: true, 2: false}}
	service := NewService(db, fake, nil)
	task, err := service.Start(context.Background(), []Pair{
		{ChannelID: 1, Model: "m1"},
		{ChannelID: 2, Model: "m1"},
	}, Options{MaxTokens: 1, Concurrency: 2})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	finished := waitForTask(t, db, task.ID)

	if finished.Status != store.ProbeTaskDone {
		t.Fatalf("status = %q, want done", finished.Status)
	}
	if finished.Total != 2 || finished.Completed != 2 {
		t.Fatalf("total/completed = %d/%d, want 2/2", finished.Total, finished.Completed)
	}
	if finished.OKCount != 1 || finished.FailCount != 1 {
		t.Fatalf("ok/fail = %d/%d, want 1/1", finished.OKCount, finished.FailCount)
	}
	if !fake.sawFlag {
		t.Error("probe flag not set: probe traffic would pollute health bookkeeping")
	}

	results, err := db.ListProbeResults(task.ID, 0)
	if err != nil {
		t.Fatalf("list results: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	for _, result := range results {
		want := result.ChannelID == 1
		if result.OK != want {
			t.Errorf("channel %d: ok = %v, want %v", result.ChannelID, result.OK, want)
		}
		if result.StatusCode == 0 {
			t.Errorf("channel %d: status code not recorded", result.ChannelID)
		}
	}

	// model_health records each pair's latest outcome.
	health, err := db.ListModelHealth()
	if err != nil {
		t.Fatalf("list health: %v", err)
	}
	byChannel := make(map[int64]store.ModelHealth, len(health))
	for _, item := range health {
		if item.Model == "m1" {
			byChannel[item.ChannelID] = item
		}
	}
	if item, ok := byChannel[2]; !ok || item.OK {
		t.Errorf("channel 2 health = %+v (found=%v), want a failed record", item, ok)
	} else if item.ConsecutiveFailures != 1 {
		t.Errorf("channel 2 consecutive failures = %d, want 1", item.ConsecutiveFailures)
	}
	if item, ok := byChannel[1]; !ok || !item.OK {
		t.Errorf("channel 1 health = %+v (found=%v), want a successful record", item, ok)
	}
}

// An empty selection must be rejected rather than silently starting nothing.
func TestProbeRejectsEmptySelection(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service := NewService(db, &fakeRelay{}, nil)
	if _, err := service.Start(context.Background(), nil, Options{}); err == nil {
		t.Fatal("expected an error for an empty selection")
	}
}

// Cancelling must stop issuing new probes; the task ends as cancelled, not done,
// so the UI can tell "stopped" apart from "finished everything".
func TestProbeCancelStopsDispatching(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Block the first probe so the cancel lands while work is still pending.
	fake := &fakeRelay{ok: map[int64]bool{1: true, 2: true, 3: true}, release: make(chan struct{})}
	service := NewService(db, fake, nil)
	pairs := []Pair{{ChannelID: 1, Model: "m"}, {ChannelID: 2, Model: "m"}, {ChannelID: 3, Model: "m"}}
	task, err := service.Start(context.Background(), pairs, Options{MaxTokens: 1, Concurrency: 1})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := service.Cancel(task.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	// Let the in-flight probe finish, then unblock the rest.
	time.Sleep(50 * time.Millisecond)
	close(fake.release)

	finished := waitForTask(t, db, task.ID)
	if finished.Status != store.ProbeTaskCancelled {
		t.Fatalf("status = %q, want cancelled", finished.Status)
	}
	if fake.calls >= len(pairs) {
		t.Errorf("calls = %d, want fewer than %d (cancel should stop new probes)", fake.calls, len(pairs))
	}
}

// A custom prompt must reach the upstream and be recorded on the task: a run
// that used "hi" and one that used a long prompt do not measure the same
// thing, and the history has to say which was which.
func TestProbeSendsConfiguredPrompt(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	fake := &fakeRelay{ok: map[int64]bool{1: true}}
	service := NewService(db, fake, nil)
	task, err := service.Start(context.Background(), []Pair{{ChannelID: 1, Model: "m1"}},
		Options{Prompt: "say ok", MaxTokens: 32})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForTask(t, db, task.ID)

	if got := fake.sentPrompt(); got != "say ok" {
		t.Errorf("upstream received prompt %q, want %q", got, "say ok")
	}
	if fake.maxToken != 32 {
		t.Errorf("upstream received max_tokens %d, want 32", fake.maxToken)
	}
	stored, err := db.GetProbeTask(task.ID)
	if err != nil || stored == nil {
		t.Fatalf("reload task: %v", err)
	}
	if stored.Prompt != "say ok" {
		t.Errorf("task prompt = %q, want %q", stored.Prompt, "say ok")
	}
}

// An empty prompt must still send something, and it must be the documented
// default rather than an empty user message.
func TestProbeFallsBackToDefaultPrompt(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	fake := &fakeRelay{ok: map[int64]bool{1: true}}
	service := NewService(db, fake, nil)
	task, err := service.Start(context.Background(), []Pair{{ChannelID: 1, Model: "m1"}}, Options{})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForTask(t, db, task.ID)

	if got := fake.sentPrompt(); got != DefaultPrompt {
		t.Errorf("prompt = %q, want default %q", got, DefaultPrompt)
	}
}

// newProbeFixture builds one enabled channel serving one enabled route, which
// is the minimum shape automatic disabling needs to find a member.
func newProbeFixture(t *testing.T, db *store.DB, model string) int64 {
	t.Helper()
	chID, err := db.Channel.Create(&domain.Channel{
		Name:    "probe-target",
		BaseURL: "https://api.example.com",
		Weight:  100,
		Status:  domain.StatusEnabled,
	})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	routeID, err := db.Route.Create(&domain.Route{ModelPattern: model, Enabled: true})
	if err != nil {
		t.Fatalf("create route: %v", err)
	}
	if _, err := db.RouteMember.Create(&domain.RouteMember{
		RouteID: routeID, ChannelID: chID, Weight: 100, Enabled: true,
	}); err != nil {
		t.Fatalf("create member: %v", err)
	}
	return chID
}

func memberEnabled(t *testing.T, db *store.DB, channelID int64, model string) bool {
	t.Helper()
	overviews, err := db.RouteMember.ListRouteOverviews()
	if err != nil {
		t.Fatalf("list overviews: %v", err)
	}
	for _, overview := range overviews {
		if overview.Route.ModelPattern != model {
			continue
		}
		for _, candidate := range overview.Members {
			if candidate.Member.ChannelID == channelID {
				return candidate.Member.Enabled
			}
		}
	}
	t.Fatalf("member for channel %d / model %s not found", channelID, model)
	return false
}

// Automatic disabling is preventive, so a single failure must not be enough —
// only a consecutive run reaching the threshold takes a member out of rotation.
func TestProbeAutoDisableNeedsConsecutiveFailures(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	chID := newProbeFixture(t, db, "m1")
	fake := &fakeRelay{ok: map[int64]bool{}}
	service := NewService(db, fake, nil)
	pairs := []Pair{{ChannelID: chID, Model: "m1"}}
	const threshold = 2

	// First failure: recorded, but below the threshold.
	task, err := service.Start(context.Background(), pairs, Options{AutoDisableAfter: threshold})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForTask(t, db, task.ID)
	if !memberEnabled(t, db, chID, "m1") {
		t.Fatal("member disabled after a single failure; one flaky probe is not evidence")
	}

	// Second consecutive failure reaches the threshold.
	task, err = service.Start(context.Background(), pairs, Options{AutoDisableAfter: threshold})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForTask(t, db, task.ID)
	if memberEnabled(t, db, chID, "m1") {
		t.Fatal("member still enabled after reaching the failure threshold")
	}
}

// With the threshold at 0 the feature is off, and failing pairs leave routing
// untouched.
func TestProbeAutoDisableOffByDefault(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	chID := newProbeFixture(t, db, "m1")
	service := NewService(db, &fakeRelay{ok: map[int64]bool{}}, nil)
	pairs := []Pair{{ChannelID: chID, Model: "m1"}}
	for i := 0; i < 3; i++ {
		task, err := service.Start(context.Background(), pairs, Options{})
		if err != nil {
			t.Fatalf("start: %v", err)
		}
		waitForTask(t, db, task.ID)
	}
	if !memberEnabled(t, db, chID, "m1") {
		t.Error("member disabled with AutoDisableAfter unset; the feature must be opt-in")
	}
}

// A successful probe restores what probing disabled, and must never resurrect
// a member an operator switched off by hand.
func TestProbeRecoveryNeverOverridesManualDisable(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	chID := newProbeFixture(t, db, "m1")
	pairs := []Pair{{ChannelID: chID, Model: "m1"}}

	// Take the member out through the probe path first.
	service := NewService(db, &fakeRelay{ok: map[int64]bool{}}, nil)
	task, err := service.Start(context.Background(), pairs, Options{AutoDisableAfter: 1})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForTask(t, db, task.ID)
	if memberEnabled(t, db, chID, "m1") {
		t.Fatal("precondition: member should be disabled by the failing probe")
	}

	// An operator now disables it explicitly: the UI PUTs the member with
	// enabled off, and the handler records the manual intent — which also
	// clears the probe marker AutoRecoverByPair keys on.
	member := memberByChannel(t, db, chID)
	member.Enabled = false
	if err := db.RouteMember.Update(member); err != nil {
		t.Fatalf("manual disable: %v", err)
	}
	if err := db.RouteMember.ApplyManualIntent(member.ID, false); err != nil {
		t.Fatalf("manual intent: %v", err)
	}

	// Probes come back green. The member must stay off.
	service = NewService(db, &fakeRelay{ok: map[int64]bool{chID: true}}, nil)
	task, err = service.Start(context.Background(), pairs, Options{AutoDisableAfter: 1})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForTask(t, db, task.ID)
	if memberEnabled(t, db, chID, "m1") {
		t.Error("probe re-enabled a member the operator disabled by hand")
	}
}

// The flip side of the guard: a member disabled by probing does come back once
// the upstream answers again.
func TestProbeRecoversSelfDisabledMember(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	chID := newProbeFixture(t, db, "m1")
	pairs := []Pair{{ChannelID: chID, Model: "m1"}}

	service := NewService(db, &fakeRelay{ok: map[int64]bool{}}, nil)
	task, err := service.Start(context.Background(), pairs, Options{AutoDisableAfter: 1})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForTask(t, db, task.ID)
	if memberEnabled(t, db, chID, "m1") {
		t.Fatal("precondition: member should be disabled")
	}

	service = NewService(db, &fakeRelay{ok: map[int64]bool{chID: true}}, nil)
	task, err = service.Start(context.Background(), pairs, Options{AutoDisableAfter: 1})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForTask(t, db, task.ID)
	if !memberEnabled(t, db, chID, "m1") {
		t.Error("member stayed disabled after a successful probe; recovery should restore it")
	}
}

package proxy

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/relay"
	"github.com/lan/meta-gateway/internal/store"
)

func TestCircuitBreakerStateMachine(t *testing.T) {
	breaker := NewModelCircuitBreaker(0)
	clock := fixedClock{now: time.Now()}
	breaker.now = func() time.Time { return clock.now }

	// Fresh: closed, full weight.
	if w := breaker.EffectiveWeight(1, "model", 100); w != 100 {
		t.Fatalf("fresh weight=%v want 100", w)
	}
	// 2 failures: still closed.
	breaker.RecordError(1, "model", false)
	breaker.RecordError(1, "model", false)
	if w := breaker.EffectiveWeight(1, "model", 100); w != 100 {
		t.Fatalf("after 2 failures weight=%v want 100", w)
	}
	// 3rd failure: half-open, weight × 0.3.
	breaker.RecordError(1, "model", false)
	if w := breaker.EffectiveWeight(1, "model", 100); w != 30 {
		t.Fatalf("half-open weight=%v want 30", w)
	}
	// 2 more: open, weight 0 before probe window.
	breaker.RecordError(1, "model", false)
	breaker.RecordError(1, "model", false)
	if w := breaker.EffectiveWeight(1, "model", 100); w != 0 {
		t.Fatalf("open weight=%v want 0", w)
	}
	if !breaker.IsOpen(1, "model") {
		t.Fatal("expected open state")
	}
	// Probe window not due: no probe slot.
	if breaker.TryBeginProbe(1, "model") {
		t.Fatal("probe must not start before window")
	}
	// Advance clock past the probe interval.
	clock.now = clock.now.Add(breakerProbeInterval + time.Second)
	if !breaker.TryBeginProbe(1, "model") {
		t.Fatal("probe should be allowed after window")
	}
	// Second probe attempt while probing: rejected.
	if breaker.TryBeginProbe(1, "model") {
		t.Fatal("only one probe at a time")
	}
	// Successful probe heals fully.
	breaker.RecordSuccess(1, "model")
	if breaker.IsOpen(1, "model") {
		t.Fatal("success must close the breaker")
	}
	if w := breaker.EffectiveWeight(1, "model", 100); w != 100 {
		t.Fatalf("after success weight=%v want 100", w)
	}
}

func TestCircuitBreakerBackoffOnlyAdvancesOnProbeFailure(t *testing.T) {
	breaker := NewModelCircuitBreaker(0)
	clock := fixedClock{now: time.Now()}
	breaker.now = func() time.Time { return clock.now }

	for i := 0; i < breakerOpenThreshold; i++ {
		breaker.RecordError(1, "m", false)
	}
	firstWindow := clock.now.Add(breakerProbeInterval + time.Second)
	clock.now = firstWindow
	if !breaker.TryBeginProbe(1, "m") {
		t.Fatal("probe 1 expected")
	}
	// Probe fails → backoff doubles: next window = 2× interval.
	breaker.RecordError(1, "m", true)
	breaker.EndProbe(1, "m")

	// Ordinary failures in open state must NOT advance backoff.
	breaker.RecordError(1, "m", false)
	breaker.RecordError(1, "m", false)

	clock.now = firstWindow.Add(breakerProbeInterval - time.Second)
	if breaker.TryBeginProbe(1, "m") {
		t.Fatal("backoff not honored: probe too early")
	}
	clock.now = firstWindow.Add(breakerProbeInterval + time.Second)
	if !breaker.TryBeginProbe(1, "m") {
		t.Fatal("probe 2 should be allowed after doubled window")
	}
	// Probe succeeds → closed.
	breaker.RecordSuccess(1, "m")
	if breaker.IsOpen(1, "m") {
		t.Fatal("expected closed after successful probe")
	}
}

func TestCircuitBreakerRuntimeThreshold(t *testing.T) {
	breaker := NewModelCircuitBreaker(0)
	clock := fixedClock{now: time.Now()}
	breaker.now = func() time.Time { return clock.now }

	// Default: open after 5 failures (half-open from 3).
	for i := 0; i < breakerOpenThreshold-1; i++ {
		breaker.RecordError(1, "m", false)
	}
	if breaker.IsOpen(1, "m") {
		t.Fatal("must not open before default threshold")
	}
	breaker.RecordError(1, "m", false)
	if !breaker.IsOpen(1, "m") {
		t.Fatal("must open at default threshold")
	}
	breaker.RecordSuccess(1, "m")

	// Runtime override: open after 3 (half-open from 2).
	breaker.SetOpenThreshold(3)
	for i := 0; i < 2; i++ {
		breaker.RecordError(1, "m", false)
	}
	if w := breaker.EffectiveWeight(1, "m", 100); w != 30 {
		t.Fatalf("half-open at ceil(3/2)=2 failures, weight=%v want 30", w)
	}
	breaker.RecordError(1, "m", false)
	if !breaker.IsOpen(1, "m") {
		t.Fatal("must open at configured threshold 3")
	}
	breaker.RecordSuccess(1, "m")

	// Threshold 0 disables the breaker entirely: failures are ignored, weight
	// stays full, and the breaker never opens.
	breaker.SetOpenThreshold(0)
	for i := 0; i < 10; i++ {
		breaker.RecordError(1, "m", false)
	}
	if breaker.IsOpen(1, "m") {
		t.Fatal("threshold 0 must disable the breaker")
	}
	if w := breaker.EffectiveWeight(1, "m", 100); w != 100 {
		t.Fatalf("disabled breaker must keep full weight, got %v", w)
	}
	// Negative value resets to the default.
	breaker.SetOpenThreshold(-1)
	breaker.RecordError(1, "m", false)
	if breaker.IsOpen(1, "m") {
		t.Fatal("negative threshold must reset to default")
	}
}

func TestPerKeyAutoDisableExcludesBadKeyOnly(t *testing.T) {
	service := &Service{}
	service.keyErrCounts = make(map[int64]map[string]map[int]int)
	service.disabledKeys = make(map[disabledKey]time.Time)
	service.now = time.Now
	service.SetKeyFailThreshold(3)

	good := "sk-good-abcdef"
	bad := "sk-bad-abcdef"

	// Three 401 failures on the bad key trigger its disable.
	for i := 0; i < 3; i++ {
		if triggered := service.recordKeyFailure(7, bad, 401); i < 2 && triggered {
			t.Fatalf("premature trigger on attempt %d", i+1)
		}
	}
	if !service.keyDisabled(7, bad) {
		t.Fatal("bad key should be disabled")
	}
	if service.keyDisabled(7, good) {
		t.Fatal("good key must stay enabled")
	}
	// Different status code on the bad key is a separate counter: one 500
	// failure does not trip the 401 threshold.
	if triggered := service.recordKeyFailure(7, bad, 500); triggered {
		t.Fatal("500 must not trip the 401 counter")
	}
	// Success on the good key clears nothing on the bad key.
	service.recordKeySuccess(7, good)
	if !service.keyDisabled(7, bad) {
		t.Fatal("bad key still disabled after unrelated success")
	}
	// A success on another channel must NOT lift this channel's exclusion.
	service.recordKeySuccess(8, bad)
	if !service.keyDisabled(7, bad) {
		t.Fatal("cross-channel success must not lift the disable")
	}
	// Success on the bad key on the same channel heals it.
	service.recordKeySuccess(7, bad)
	if service.keyDisabled(7, bad) {
		t.Fatal("bad key should heal after success on the same channel")
	}
}

func TestAllKeysDisabledCascadesChannelDisable(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	enc, err := crypto.New("cascade-test-key")
	if err != nil {
		t.Fatal(err)
	}
	secret1, _ := enc.Encrypt([]byte("sk-one-abcdef"))
	secret2, _ := enc.Encrypt([]byte("sk-two-abcdef"))
	siteID, err := db.Site.Create(&domain.Site{Name: "s", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	cred1, _ := db.Credential.Create(&domain.Credential{SiteID: siteID, Kind: "api_key", SecretEnc: []byte(secret1), Status: domain.StatusEnabled})
	_, _ = db.Credential.Create(&domain.Credential{SiteID: siteID, Kind: "api_key", SecretEnc: []byte(secret2), Status: domain.StatusEnabled})
	channelID, err := db.Channel.Create(&domain.Channel{SiteID: &siteID, CredentialID: &cred1, Name: "two-key", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}

	service := &Service{db: db, enc: enc}
	service.keyErrCounts = make(map[int64]map[string]map[int]int)
	service.disabledKeys = make(map[disabledKey]time.Time)
	service.now = time.Now
	service.SetKeyFailThreshold(2)
	service.SetAutoDisableThreshold(2)
	service.SetKeyPoolRotation(true)
	channel, _ := db.Channel.GetByID(channelID)

	// Disable key 1: pool still has key 2 → no cascade.
	service.recordKeyFailure(channelID, "sk-one-abcdef", 401)
	service.recordKeyFailure(channelID, "sk-one-abcdef", 401)
	service.cascadeChannelIfAllKeysDisabled(*channel)
	fresh, _ := db.Channel.GetByID(channelID)
	if fresh.Status != domain.StatusEnabled {
		t.Fatalf("channel must stay enabled while one key lives: %s", fresh.Status)
	}

	// Disable key 2: pool now empty → cascade disables the channel.
	service.recordKeyFailure(channelID, "sk-two-abcdef", 401)
	service.recordKeyFailure(channelID, "sk-two-abcdef", 401)
	service.cascadeChannelIfAllKeysDisabled(*channel)
	fresh, _ = db.Channel.GetByID(channelID)
	if fresh.Status != domain.StatusAutoDisabled {
		t.Fatalf("channel must be auto-disabled when all keys are down: %s", fresh.Status)
	}
}

func TestPerKeyCountersAreScopedByChannel(t *testing.T) {
	service := &Service{}
	service.keyErrCounts = make(map[int64]map[string]map[int]int)
	service.disabledKeys = make(map[disabledKey]time.Time)
	service.now = time.Now
	service.SetKeyFailThreshold(2)

	key := "sk-shared-abcdef"
	service.recordKeyFailure(1, key, 403)
	if service.keyDisabled(1, key) {
		t.Fatal("one failure must not disable")
	}
	// The same key failing on another channel does not count toward this one.
	service.recordKeyFailure(2, key, 403)
	if service.keyDisabled(1, key) {
		t.Fatal("cross-channel failures must not accumulate")
	}
	service.recordKeyFailure(1, key, 403)
	if !service.keyDisabled(1, key) {
		t.Fatal("two failures on the same channel must disable")
	}
	// The exclusion is channel-scoped: channel 2 still sees the key as healthy.
	if service.keyDisabled(2, key) {
		t.Fatal("channel 2 must not inherit channel 1's exclusion")
	}
}

func TestCircuitBreakerSkipsOpenChannelInRelay(t *testing.T) {
	// High channel is open → skipped entirely; only low (healthy) is called.
	upstream := &queuedRelay{results: []*relay.Result{
		response(http.StatusOK, `{"ok":true}`), // low channel success
	}}
	service, _, _, _ := setupProxy(t, upstream)
	// Force high channel open by feeding 5 failures through the breaker.
	highChannelID := int64(1) // setupProxy creates high first
	for i := 0; i < breakerOpenThreshold; i++ {
		service.breaker.RecordError(highChannelID, "model", false)
	}
	result := service.ChatCompletions(context.Background(), Request{RequestID: "req-brk", Model: "model", Body: []byte(`{}`)})
	defer result.Body.Close()
	if result.Err != nil || result.StatusCode != http.StatusOK {
		t.Fatalf("result=%+v", result)
	}
	// High skipped → exactly one call on the low channel.
	if len(upstream.calls) != 1 {
		t.Fatalf("expected 1 call on healthy channel, got %#v", upstream.calls)
	}
}

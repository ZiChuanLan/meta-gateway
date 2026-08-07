package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/adapters"
	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/relay"
	"github.com/lan/meta-gateway/internal/routing"
	"github.com/lan/meta-gateway/internal/store"
	"github.com/lan/meta-gateway/internal/usage"
)

type queuedRelay struct {
	results []*relay.Result
	calls   []string
}

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

type firstRandom struct{}

func (firstRandom) Intn(int) int { return 0 }

func (firstRandom) Float64() float64 { return 0 }

func (r *queuedRelay) ChatCompletionsContext(_ context.Context, upstreamURL, _ string, _ []byte, _ bool) *relay.Result {
	r.calls = append(r.calls, upstreamURL)
	result := r.results[0]
	r.results = r.results[1:]
	return result
}

func (r *queuedRelay) ForwardWithHeaders(_ context.Context, _, upstreamURL string, _ http.Header, _ []byte) *relay.Result {
	return r.ChatCompletionsContext(context.Background(), upstreamURL, "", nil, false)
}

func (r *queuedRelay) ForwardContext(_ context.Context, _, upstreamURL, _ string, _ []byte) *relay.Result {
	return r.ChatCompletionsContext(context.Background(), upstreamURL, "", nil, false)
}

func response(status int, body string) *relay.Result {
	return &relay.Result{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), LatencyMs: 2}
}

func setupProxy(t *testing.T, upstream Relay) (*Service, *store.DB, int64, int64) {
	t.Helper()
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	enc, err := crypto.New("proxy-test-master-key")
	if err != nil {
		t.Fatal(err)
	}
	secret, err := enc.Encrypt([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	siteID, _ := db.Site.Create(&domain.Site{Name: "site", Status: domain.StatusEnabled})
	credentialID, err := db.Credential.Create(&domain.Credential{SiteID: siteID, Kind: "api_key", SecretEnc: []byte(secret), Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	createChannel := func(name, url string) int64 {
		id, err := db.Channel.Create(&domain.Channel{SiteID: &siteID, CredentialID: &credentialID, Name: name, BaseURL: url, Status: domain.StatusEnabled})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	highChannel := createChannel("high", "https://high.example")
	lowChannel := createChannel("low", "https://low.example")
	routeID, err := db.Route.Create(&domain.Route{ModelPattern: "model", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	highMember, _ := db.RouteMember.Create(&domain.RouteMember{RouteID: routeID, ChannelID: highChannel, Priority: 20, Weight: 100, Enabled: true})
	lowMember, _ := db.RouteMember.Create(&domain.RouteMember{RouteID: routeID, ChannelID: lowChannel, Priority: 10, Weight: 100, Enabled: true})
	now := time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC)
	selector := routing.NewWithDependencies(db.RouteMember, fixedClock{now: now}, firstRandom{})
	service := New(selector, upstream, db, enc, 2, time.Minute)
	service.now = func() time.Time { return now }
	return service, db, highMember, lowMember
}

func TestAdapterLocalFailureDoesNotHealKeyState(t *testing.T) {
	upstream := &queuedRelay{results: []*relay.Result{
		response(http.StatusOK, `{"promptFeedback":{"blockReason":"SAFETY"}}`),
	}}
	service, db, highMember, _ := setupProxy(t, upstream)
	member, err := db.RouteMember.GetByID(highMember)
	if err != nil {
		t.Fatal(err)
	}
	channel, err := db.Channel.GetByID(member.ChannelID)
	if err != nil {
		t.Fatal(err)
	}
	service.SetAutoDisableThreshold(2)
	service.recordKeyFailure(channel.ID, "secret", 500)
	channel.TypeHint = "gemini"
	channel.BaseURL = "https://gemini.example"
	if err := db.Channel.Update(channel); err != nil {
		t.Fatal(err)
	}
	service.SetAdapterRegistry(adapters.NewRegistry(nil))
	result := service.ChatCompletions(context.Background(), Request{
		RequestID: "req-local-adapter-error",
		Model:     "model",
		Body:      []byte(`{"model":"model","messages":[{"role":"user","content":"hi"}]}`),
	})
	if !errors.Is(result.Err, adapters.ErrContentBlocked) {
		t.Fatalf("error=%v, want ErrContentBlocked", result.Err)
	}
	// A second real key failure should cross the threshold. If the local
	// adapter error incorrectly called recordKeySuccess, the counter would
	// have been cleared and this assertion would fail.
	service.recordKeyFailure(channel.ID, "secret", 500)
	if !service.keyDisabled(channel.ID, "secret") {
		t.Fatal("local adapter error must not heal key state")
	}
}

func TestInvalidBaseURLIsLocalFailureWithoutHealthMutation(t *testing.T) {
	upstream := &queuedRelay{}
	service, db, highMember, _ := setupProxy(t, upstream)
	member, err := db.RouteMember.GetByID(highMember)
	if err != nil {
		t.Fatal(err)
	}
	channel, err := db.Channel.GetByID(member.ChannelID)
	if err != nil {
		t.Fatal(err)
	}
	channel.BaseURL = "relative-base-url"
	if err := db.Channel.Update(channel); err != nil {
		t.Fatal(err)
	}

	result := service.ChatCompletions(context.Background(), Request{
		RequestID: "req-invalid-url",
		Model:     "model",
		Body:      []byte(`{"model":"model"}`),
	})
	if !errors.Is(result.Err, adapters.ErrInvalidURL) {
		t.Fatalf("error=%v, want ErrInvalidURL", result.Err)
	}
	if len(upstream.calls) != 0 {
		t.Fatalf("invalid URL must not call upstream: %#v", upstream.calls)
	}
	freshMember, err := db.RouteMember.GetByID(highMember)
	if err != nil {
		t.Fatal(err)
	}
	if freshMember.FailCount != 0 || freshMember.CooldownUntil != nil {
		t.Fatalf("invalid URL mutated member health: %+v", freshMember)
	}
	freshChannel, err := db.Channel.GetByID(member.ChannelID)
	if err != nil {
		t.Fatal(err)
	}
	if freshChannel.ConsecutiveFailures != 0 {
		t.Fatalf("invalid URL mutated channel health: %+v", freshChannel)
	}
}

func TestChannelFailureCountsOncePerRequestMultiKey(t *testing.T) {
	// A 2-key pool failing on one request must increment the channel
	// consecutive-failure counter exactly once (not once per key). Two channels
	// exist (high/low), so the retry chain can make 4 upstream calls.
	upstream := &queuedRelay{results: []*relay.Result{
		response(http.StatusInternalServerError, `{"error":"boom"}`),
		response(http.StatusInternalServerError, `{"error":"boom"}`),
		response(http.StatusInternalServerError, `{"error":"boom"}`),
		response(http.StatusInternalServerError, `{"error":"boom"}`),
	}}
	service, db, highMember, _ := setupProxy(t, upstream)
	member, _ := db.RouteMember.GetByID(highMember)
	channel, _ := db.Channel.GetByID(member.ChannelID)

	// Second api_key on the same site forms the failover pool.
	enc, _ := crypto.New("proxy-test-master-key")
	secret2, err := enc.Encrypt([]byte("secret-2"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Credential.Create(&domain.Credential{SiteID: *channel.SiteID, Kind: "api_key", SecretEnc: []byte(secret2), Status: domain.StatusEnabled}); err != nil {
		t.Fatal(err)
	}

	service.SetAutoDisableThreshold(100) // never trips; only the count matters
	result := service.ChatCompletions(context.Background(), Request{RequestID: "req-count", Model: "model", Body: []byte(`{"model":"model"}`)})
	if result.Body != nil {
		_ = result.Body.Close()
	}
	if result.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status=%d, want 500", result.StatusCode)
	}
	if len(upstream.calls) != 4 {
		t.Fatalf("expected 4 upstream calls (2 keys x 2 channels), got %d: %#v", len(upstream.calls), upstream.calls)
	}
	fresh, _ := db.Channel.GetByID(member.ChannelID)
	if fresh.ConsecutiveFailures != 1 {
		t.Fatalf("consecutive_failures=%d, want 1 (once per request, not per key)", fresh.ConsecutiveFailures)
	}
}

func TestRetryFallsBackAndRecordsCooldown(t *testing.T) {
	upstream := &queuedRelay{results: []*relay.Result{response(http.StatusServiceUnavailable, `{"error":"busy"}`), response(http.StatusOK, `{"ok":true}`)}}
	service, db, highMember, lowMember := setupProxy(t, upstream)
	result := service.ChatCompletions(context.Background(), Request{RequestID: "req-1", Model: "model", Body: []byte(`{"model":"model"}`)})
	defer result.Body.Close()
	if result.Err != nil || result.StatusCode != http.StatusOK {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(upstream.calls) != 2 || !strings.Contains(upstream.calls[0], "high.example") || !strings.Contains(upstream.calls[1], "low.example") {
		t.Fatalf("unexpected calls: %#v", upstream.calls)
	}
	high, _ := db.RouteMember.GetByID(highMember)
	low, _ := db.RouteMember.GetByID(lowMember)
	if high.FailCount != 1 || high.CooldownUntil == nil {
		t.Fatalf("high member not cooled down: %+v", high)
	}
	if low.FailCount != 0 || low.CooldownUntil != nil {
		t.Fatalf("successful member should be healthy: %+v", low)
	}
	logs, err := db.ProxyLog.List(10)
	if err != nil || len(logs) != 2 {
		t.Fatalf("logs=%+v err=%v", logs, err)
	}
	if logs[1].Status != http.StatusServiceUnavailable || logs[0].Status != http.StatusOK {
		t.Fatalf("unexpected statuses: %+v", logs)
	}
}

func TestKeyPoolFailsOverBeforeNextChannel(t *testing.T) {
	upstream := &queuedRelay{results: []*relay.Result{
		response(http.StatusServiceUnavailable, `{"error":"key1 busy"}`),
		response(http.StatusOK, `{"ok":true}`),
	}}
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	enc, err := crypto.New("proxy-key-pool-master")
	if err != nil {
		t.Fatal(err)
	}
	secretOne, err := enc.Encrypt([]byte("sk-first"))
	if err != nil {
		t.Fatal(err)
	}
	secretTwo, err := enc.Encrypt([]byte("sk-second"))
	if err != nil {
		t.Fatal(err)
	}
	siteID, err := db.Site.Create(&domain.Site{Name: "site", BaseURL: "https://pool.example", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	firstKeyID, err := db.Credential.Create(&domain.Credential{
		SiteID: siteID, Kind: "api_key", SecretEnc: []byte(secretOne), Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Credential.Create(&domain.Credential{
		SiteID: siteID, Kind: "api_key", SecretEnc: []byte(secretTwo), Status: domain.StatusEnabled,
	}); err != nil {
		t.Fatal(err)
	}
	channelID, err := db.Channel.Create(&domain.Channel{
		SiteID: &siteID, CredentialID: &firstKeyID, Name: "pooled", BaseURL: "", Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	routeID, err := db.Route.Create(&domain.Route{ModelPattern: "model", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.RouteMember.Create(&domain.RouteMember{
		RouteID: routeID, ChannelID: channelID, Priority: 10, Weight: 100, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC)
	selector := routing.NewWithDependencies(db.RouteMember, fixedClock{now: now}, firstRandom{})
	service := New(selector, upstream, db, enc, 0, time.Minute)
	service.now = func() time.Time { return now }

	result := service.ChatCompletions(context.Background(), Request{
		RequestID: "req-pool", Model: "model", Body: []byte(`{}`),
	})
	if result.Err != nil || result.StatusCode != http.StatusOK {
		t.Fatalf("unexpected result: %+v", result)
	}
	defer result.Body.Close()
	if len(upstream.calls) != 2 {
		t.Fatalf("expected two key attempts on same channel, got %#v", upstream.calls)
	}
	for _, call := range upstream.calls {
		if !strings.Contains(call, "pool.example") {
			t.Fatalf("unexpected upstream host: %#v", upstream.calls)
		}
	}
	logs, err := db.ProxyLog.List(10)
	if err != nil {
		t.Fatal(err)
	}
	// Only the successful (or final) key attempt is recorded for the channel attempt.
	if len(logs) != 1 || logs[0].Status != http.StatusOK {
		t.Fatalf("logs=%+v", logs)
	}
}

func TestChatUsesSiteBaseWhenChannelBaseEmpty(t *testing.T) {
	upstream := &queuedRelay{results: []*relay.Result{response(http.StatusOK, `{"ok":true}`)}}
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	enc, err := crypto.New("proxy-test-master-key")
	if err != nil {
		t.Fatal(err)
	}
	secret, err := enc.Encrypt([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	siteID, err := db.Site.Create(&domain.Site{Name: "site", BaseURL: "https://site.example/v1", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	credentialID, err := db.Credential.Create(&domain.Credential{SiteID: siteID, Kind: "api_key", SecretEnc: []byte(secret), Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	channelID, err := db.Channel.Create(&domain.Channel{SiteID: &siteID, CredentialID: &credentialID, Name: "ch", BaseURL: "", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	routeID, err := db.Route.Create(&domain.Route{ModelPattern: "model", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.RouteMember.Create(&domain.RouteMember{RouteID: routeID, ChannelID: channelID, Priority: 10, Weight: 100, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC)
	selector := routing.NewWithDependencies(db.RouteMember, fixedClock{now: now}, firstRandom{})
	service := New(selector, upstream, db, enc, 0, time.Minute)
	service.now = func() time.Time { return now }
	result := service.ChatCompletions(context.Background(), Request{RequestID: "req-site-base", Model: "model", Body: []byte(`{}`)})
	defer result.Body.Close()
	if result.Err != nil || result.StatusCode != http.StatusOK {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(upstream.calls) != 1 || upstream.calls[0] != "https://site.example/v1/chat/completions" {
		t.Fatalf("unexpected upstream URL: %#v", upstream.calls)
	}
}

func TestClientErrorFailsOverAndRecordsCooldown(t *testing.T) {
	upstream := &queuedRelay{results: []*relay.Result{
		response(http.StatusBadRequest, `{"error":"bad request"}`),
	}}
	service, db, highMember, _ := setupProxy(t, upstream)
	result := service.ChatCompletions(context.Background(), Request{RequestID: "req-2", Model: "model", Body: []byte(`{}`)})
	defer result.Body.Close()
	// AxonHub semantics: 4xx is NOT retryable by default — a bad request will
	// not heal by failing over. Exactly one upstream call, no cooldown.
	if result.StatusCode != http.StatusBadRequest || len(upstream.calls) != 1 {
		t.Fatalf("result=%+v calls=%#v", result, upstream.calls)
	}
	high, _ := db.RouteMember.GetByID(highMember)
	if high.FailCount != 0 {
		t.Fatalf("4xx must not cool down the member: %+v", high)
	}
}

func TestChannelRetryConfigAddsCustomStatusCodes(t *testing.T) {
	upstream := &queuedRelay{results: []*relay.Result{
		response(http.StatusBadRequest, `{"error":"bad request"}`),
		response(http.StatusBadRequest, `{"error":"bad request"}`),
	}}
	service, db, highMember, _ := setupProxy(t, upstream)
	ch, err := db.Channel.GetByID(highMember)
	if err != nil || ch == nil {
		t.Fatalf("channel get: %v", err)
	}
	// Opt the channel into retrying 400 via retry_config.
	ch.RetryConfig = `{"status_codes":[400]}`
	if err := db.Channel.Update(ch); err != nil {
		t.Fatalf("channel update: %v", err)
	}
	result := service.ChatCompletions(context.Background(), Request{RequestID: "req-cfg", Model: "model", Body: []byte(`{}`)})
	defer result.Body.Close()
	// Channel config makes 400 retryable → failover to the second channel.
	if len(upstream.calls) != 2 {
		t.Fatalf("expected 2 calls with channel retry config, got %#v", upstream.calls)
	}
	if result.StatusCode != http.StatusBadRequest {
		t.Fatalf("result=%+v", result)
	}
}

func TestRetryExhaustionReturnsLastUpstreamResponse(t *testing.T) {
	final := response(http.StatusGatewayTimeout, `{"error":"still busy"}`)
	final.Header.Set("Retry-After", "7")
	upstream := &queuedRelay{results: []*relay.Result{
		response(http.StatusServiceUnavailable, `{"error":"busy"}`),
		final,
	}}
	service, db, _, _ := setupProxy(t, upstream)

	result := service.ChatCompletions(context.Background(), Request{RequestID: "req-exhausted", Model: "model", Body: []byte(`{}`)})
	if result.Err != nil || result.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("unexpected result: %+v", result)
	}
	defer result.Body.Close()
	body, err := io.ReadAll(result.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"error":"still busy"}` || result.Header.Get("Retry-After") != "7" {
		t.Fatalf("body=%q header=%q", body, result.Header.Get("Retry-After"))
	}
	if len(upstream.calls) != 2 {
		t.Fatalf("unexpected calls: %#v", upstream.calls)
	}
	logs, err := db.ProxyLog.List(10)
	if err != nil || len(logs) != 2 {
		t.Fatalf("logs=%+v err=%v", logs, err)
	}
	if logs[0].Status != http.StatusGatewayTimeout || logs[0].Attempt != 2 || logs[1].Status != http.StatusServiceUnavailable || logs[1].Attempt != 1 {
		t.Fatalf("unexpected logs: %+v", logs)
	}
}

func TestCancellationDoesNotRetry(t *testing.T) {
	upstream := &queuedRelay{results: []*relay.Result{{Err: fmt.Errorf("relay: %w", context.Canceled)}}}
	service, _, _, _ := setupProxy(t, upstream)
	result := service.ChatCompletions(context.Background(), Request{RequestID: "req-3", Model: "model", Body: []byte(`{}`)})
	if !errors.Is(result.Err, context.Canceled) || len(upstream.calls) != 1 {
		t.Fatalf("result=%+v calls=%#v", result, upstream.calls)
	}
}

func TestStreamFirstByteFailureFailsOver(t *testing.T) {
	// First channel returns 200 and then dies before emitting any SSE data; the
	// gateway must treat it as a retryable failure and fail over to the second
	// channel instead of surfacing a silent truncated 200 to the client.
	deadStream := response(http.StatusOK, "")
	okStream := response(http.StatusOK, "data: {\"chunk\":1}\n\n")
	upstream := &queuedRelay{results: []*relay.Result{deadStream, okStream}}
	service, db, highMember, lowMember := setupProxy(t, upstream)

	result := service.ChatCompletions(context.Background(), Request{
		RequestID: "req-stream", Model: "model", Body: []byte(`{"model":"model","stream":true}`), Stream: true,
	})
	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}
	defer result.Body.Close()
	if len(upstream.calls) != 2 || !strings.Contains(upstream.calls[0], "high.example") || !strings.Contains(upstream.calls[1], "low.example") {
		t.Fatalf("unexpected calls: %#v", upstream.calls)
	}
	body, err := io.ReadAll(result.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "data: {\"chunk\":1}\n\n" {
		t.Fatalf("unexpected body: %q", body)
	}
	high, _ := db.RouteMember.GetByID(highMember)
	if high.FailCount == 0 || high.CooldownUntil == nil || high.LastError != "stream_interrupted" {
		t.Fatalf("dead-stream channel should be cooled down: %+v", high)
	}
	low, _ := db.RouteMember.GetByID(lowMember)
	if low.FailCount != 0 || low.CooldownUntil != nil {
		t.Fatalf("successful channel should be healthy: %+v", low)
	}
}

func TestRetryAfterExtendsCooldown(t *testing.T) {
	busy := response(http.StatusServiceUnavailable, `{"error":"busy"}`)
	busy.Header.Set("Retry-After", "3600")
	ok := response(http.StatusOK, `{"ok":true}`)
	upstream := &queuedRelay{results: []*relay.Result{busy, ok}}
	service, db, highMember, _ := setupProxy(t, upstream)

	result := service.ChatCompletions(context.Background(), Request{RequestID: "req-ra", Model: "model", Body: []byte(`{}`)})
	if result.Err != nil || result.StatusCode != http.StatusOK {
		t.Fatalf("unexpected result: %+v", result)
	}
	defer result.Body.Close()
	high, _ := db.RouteMember.GetByID(highMember)
	if high.CooldownUntil == nil {
		t.Fatalf("member not cooled down: %+v", high)
	}
	// Base cool-down is 1 minute; Retry-After: 3600 must win.
	want := service.now().Add(time.Hour)
	if !high.CooldownUntil.Equal(want) {
		t.Fatalf("Retry-After not honored: cooldown_until=%v want=%v", high.CooldownUntil, want)
	}
}

func TestErrorEMARisesOnFailureAndDecaysOnSuccess(t *testing.T) {
	service := &Service{}

	if _, ok := service.ChannelErrorRate(7, "test-model"); ok {
		t.Fatal("fresh channel must have no error sample")
	}
	// One failure: EMA 0.5 (alpha 0.5 — a single failure halves the share).
	service.observeError(7, "test-model")
	rate, ok := service.ChannelErrorRate(7, "test-model")
	if !ok || rate < 0.49 || rate > 0.51 {
		t.Fatalf("after 1 failure rate=%v ok=%v, want ~0.5", rate, ok)
	}
	// Two more failures: 0.5 + 0.5×0.5 = 0.75, then 0.5 + 0.5×0.75 = 0.875.
	service.observeError(7, "test-model")
	service.observeError(7, "test-model")
	rate, _ = service.ChannelErrorRate(7, "test-model")
	if rate < 0.87 || rate > 0.88 {
		t.Fatalf("after 3 failures rate=%v, want ~0.875", rate)
	}
	// A success decays: ×0.5 → 0.4375.
	service.decayError(7, "test-model")
	rate, _ = service.ChannelErrorRate(7, "test-model")
	if rate < 0.43 || rate > 0.45 {
		t.Fatalf("after 1 success rate=%v, want ~0.4375", rate)
	}
	// Repeated successes recover toward zero but never clear instantly.
	for i := 0; i < 20; i++ {
		service.decayError(7, "test-model")
	}
	rate, _ = service.ChannelErrorRate(7, "test-model")
	if rate > 0.01 {
		t.Fatalf("after many successes rate=%v, want ~0", rate)
	}
}

func TestConcurrencyGuardAcquireRelease(t *testing.T) {
	upstream := &queuedRelay{results: []*relay.Result{response(http.StatusOK, `{"ok":true}`)}}
	service, _, _, _ := setupProxy(t, upstream)

	// Guard off: acquire/release are no-ops and Inflight stays 0.
	service.acquireChannel(42)
	service.releaseChannel(42)
	if got := service.Inflight(42); got != 0 {
		t.Fatalf("guard off: inflight=%d want 0", got)
	}

	// Guard on: acquire reserves a slot visible to the selector provider.
	service.SetConcurrencyAware(true, 5)
	service.acquireChannel(42)
	service.acquireChannel(42)
	if got := service.Inflight(42); got != 2 {
		t.Fatalf("inflight=%d want 2", got)
	}
	service.releaseChannel(42)
	if got := service.Inflight(42); got != 1 {
		t.Fatalf("after release inflight=%d want 1", got)
	}
	service.releaseChannel(42)
	if got := service.Inflight(42); got != 0 {
		t.Fatalf("after second release inflight=%d want 0", got)
	}
}

func TestPreserveTruncatesErrorBodyOnly(t *testing.T) {
	bigBody := strings.Repeat("x", 200*1024)

	// Error responses are capped to the error-text bound: the failure body is
	// never buffered whole (only its leading text matters for classification).
	errResult := preserve(response(http.StatusInternalServerError, bigBody))
	got, err := io.ReadAll(errResult.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) > preserveErrorReadLimit {
		t.Fatalf("error body preserved %d bytes, want <= %d", len(got), preserveErrorReadLimit)
	}
	if errResult.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status changed: %d", errResult.StatusCode)
	}

	// 2xx bodies stay intact (the client receives them).
	okResult := preserve(response(http.StatusOK, bigBody))
	gotOK, err := io.ReadAll(okResult.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotOK) != len(bigBody) {
		t.Fatalf("ok body preserved %d bytes, want %d", len(gotOK), len(bigBody))
	}
}

func TestIsRetryableForChannelUsesPrecompiledRegex(t *testing.T) {
	cfg := domain.ParseRetryConfig(`{"error_patterns":[{"pattern":"ov[ae]rloaded","regex":true}]}`)
	if !isRetryableForChannel(0, "provider overloaded", cfg) {
		t.Fatal("precompiled regex must match")
	}
	if isRetryableForChannel(0, "provider underloaded", cfg) {
		t.Fatal("non-matching text must not be retryable")
	}
	// Global defaults still apply on top of channel config.
	if !isRetryableForChannel(429, "anything", cfg) {
		t.Fatal("default retryable status must remain retryable")
	}
}

func TestBillingCostFormula(t *testing.T) {
	upstream := &queuedRelay{results: []*relay.Result{response(http.StatusOK, `{"ok":true}`)}}
	service, db, _, _ := setupProxy(t, upstream)
	// Key with unit prices; model ratio 3.0.
	key := &domain.DownstreamKey{
		TokenHash: "hash-formula-1", Name: "billed", Enabled: true, Scopes: "relay",
		PricePromptPer1k: 1.0, PriceCompletionPer1k: 2.0,
	}
	keyID, err := db.DownstreamKey.Create(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.ModelRatio.SetRatio("ratio-model", 3.0); err != nil {
		t.Fatal(err)
	}
	req := Request{DownstreamKeyID: keyID, Model: "ratio-model"}
	tokens := usage.Tokens{PromptTokens: 1000, CompletionTokens: 250, CacheReadTokens: 100}
	// prompt = 1000+100 = 1100 → 1.1 * 1.0; completion = 250 → 0.25 * 2.0;
	// (1.1 + 0.5) * 3.0 = 4.8
	cost := service.billingCost(req, tokens)
	if cost < 4.79 || cost > 4.81 {
		t.Fatalf("cost=%v want ~4.8", cost)
	}
	// Unknown model → ratio 1.0, no key → 0 price → 0 cost.
	if cost := service.billingCost(Request{}, usage.Tokens{PromptTokens: 100}); cost != 0 {
		t.Fatalf("no-key cost=%v want 0", cost)
	}
}

func TestIsSilentSSEStart(t *testing.T) {
	cases := []struct {
		name   string
		prefix string
		want   bool
	}{
		{"empty", "", false},
		{"normal first chunk with role", "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"},\"index\":0}]}\n\n", false},
		{"normal content chunk", "data: {\"choices\":[{\"delta\":{\"content\":\"Hello\"},\"index\":0}]}\n\n", false},
		{"immediate DONE", "data: [DONE]\n\n", true},
		{"empty choices", "data: {\"choices\":[]}\n\ndata: [DONE]\n\n", true},
		{"delta with neither role nor content", "data: {\"choices\":[{\"delta\":{},\"index\":0}]}\n\n", true},
		{"usage-only frame then done", "data: {\"choices\":[],\"usage\":{\"total_tokens\":5}}\n\ndata: [DONE]\n\n", true},
		{"content after silent frame is not silent", "data: {\"choices\":[{\"delta\":{},\"index\":0}]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"Hi\"},\"index\":0}]}\n\n", false},
		{"non-json keepalive not silent", ": keep-alive\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n", false},
		{"nonstandard json without choices not silent", "data: {\"chunk\":1}\n\n", false},
	}
	for _, tc := range cases {
		if got := isSilentSSEStart([]byte(tc.prefix)); got != tc.want {
			t.Errorf("%s: isSilentSSEStart=%v want %v", tc.name, got, tc.want)
		}
	}
}

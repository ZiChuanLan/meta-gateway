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
	bodies  [][]byte
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

func (r *queuedRelay) ForwardWithHeaders(_ context.Context, _, upstreamURL string, _ http.Header, body []byte) *relay.Result {
	r.bodies = append(r.bodies, append([]byte(nil), body...))
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

func TestScopedPromptMaskSurvivesModelAliasRewrite(t *testing.T) {
	upstream := &queuedRelay{results: []*relay.Result{response(http.StatusOK, `{"ok":true}`)}}
	service, db, highMemberID, _ := setupProxy(t, upstream)
	member, err := db.RouteMember.GetByID(highMemberID)
	if err != nil {
		t.Fatal(err)
	}
	route, err := db.Route.GetByID(member.RouteID)
	if err != nil {
		t.Fatal(err)
	}
	route.MappingJSON = `{"real":"upstream-model"}`
	if err := db.Route.Update(route); err != nil {
		t.Fatal(err)
	}
	rule := &store.PromptGuardRule{
		Name:         "channel secret",
		Pattern:      `sk-[A-Za-z0-9]{16,}`,
		Action:       "mask",
		Replacement:  "[REDACTED]",
		ChannelScope: member.ChannelID,
		Enabled:      true,
	}
	if err := db.PromptGuard.Upsert(rule); err != nil {
		t.Fatal(err)
	}

	result := service.ChatCompletions(context.Background(), Request{
		RequestID: "req-scoped-mask-alias",
		Model:     "model",
		Body:      []byte(`{"model":"model","messages":[{"role":"user","content":"sk-ABCDEFGHIJKLMNOP1234"}]}`),
	})
	if result.Err != nil || result.StatusCode != http.StatusOK {
		t.Fatalf("unexpected result: %+v", result)
	}
	defer result.Body.Close()
	if len(upstream.bodies) != 1 {
		t.Fatalf("upstream calls = %d, want 1", len(upstream.bodies))
	}
	forwarded := string(upstream.bodies[0])
	if strings.Contains(forwarded, "sk-ABCDEFGHIJKLMNOP1234") {
		t.Fatalf("scoped prompt secret leaked after alias rewrite: %s", forwarded)
	}
	if !strings.Contains(forwarded, `"model":"upstream-model"`) || !strings.Contains(forwarded, "[REDACTED]") {
		t.Fatalf("forwarded body missed alias or mask: %s", forwarded)
	}
}

func TestOversizedTransformedResponseIsBoundedAndNotRetried(t *testing.T) {
	_, err := readResponseBody(strings.NewReader("123456"), 5)
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("oversized response error=%v, want ErrResponseTooLarge", err)
	}
	category, retryable := classifyForChannel(&relay.Result{Err: err}, domain.RetryConfig{})
	if category != "response_too_large" || retryable {
		t.Fatalf("classification=(%q,%v), want response_too_large/non-retryable", category, retryable)
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
	// consecutive-failure counter exactly once (not once per key). Each key is
	// re-sent once (channel retry = 1), so the retry chain makes 8 upstream
	// calls: 2 keys × 2 re-sends × 2 channels.
	upstream := &queuedRelay{results: []*relay.Result{
		response(http.StatusInternalServerError, `{"error":"boom"}`),
		response(http.StatusInternalServerError, `{"error":"boom"}`),
		response(http.StatusInternalServerError, `{"error":"boom"}`),
		response(http.StatusInternalServerError, `{"error":"boom"}`),
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
	if len(upstream.calls) != 8 {
		t.Fatalf("expected 8 upstream calls (2 keys x 2 re-sends x 2 channels), got %d: %#v", len(upstream.calls), upstream.calls)
	}
	fresh, _ := db.Channel.GetByID(member.ChannelID)
	if fresh.ConsecutiveFailures != 1 {
		t.Fatalf("consecutive_failures=%d, want 1 (once per request, not per key)", fresh.ConsecutiveFailures)
	}
}

func TestRetryFallsBackAndRecordsCooldown(t *testing.T) {
	// First channel fails twice (initial + same-key retry), then the request
	// fails over to the second channel and succeeds.
	upstream := &queuedRelay{results: []*relay.Result{
		response(http.StatusServiceUnavailable, `{"error":"busy"}`),
		response(http.StatusServiceUnavailable, `{"error":"busy"}`),
		response(http.StatusOK, `{"ok":true}`),
	}}
	service, db, highMember, lowMember := setupProxy(t, upstream)
	result := service.ChatCompletions(context.Background(), Request{RequestID: "req-1", Model: "model", Body: []byte(`{"model":"model"}`)})
	defer result.Body.Close()
	if result.Err != nil || result.StatusCode != http.StatusOK {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(upstream.calls) != 3 || !strings.Contains(upstream.calls[0], "high.example") || !strings.Contains(upstream.calls[1], "high.example") || !strings.Contains(upstream.calls[2], "low.example") {
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
		response(http.StatusOK, `{"ok":true}`),
	}}
	service, db, highMember, lowMember := setupProxy(t, upstream)
	result := service.ChatCompletions(context.Background(), Request{RequestID: "req-2", Model: "model", Body: []byte(`{}`)})
	defer result.Body.Close()
	// 4xx fails over to the next channel: a different upstream may accept the
	// same request (heterogeneous channel capabilities).
	if result.StatusCode != http.StatusOK || len(upstream.calls) != 2 {
		t.Fatalf("result=%+v calls=%#v", result, upstream.calls)
	}
	high, _ := db.RouteMember.GetByID(highMember)
	if high.FailCount != 1 || high.CooldownUntil == nil {
		t.Fatalf("4xx must cool the member down: %+v", high)
	}
	low, _ := db.RouteMember.GetByID(lowMember)
	if low.FailCount != 0 || low.CooldownUntil != nil {
		t.Fatalf("successful member should be healthy: %+v", low)
	}
}

func TestCrossChannelFailoverCanBeDisabled(t *testing.T) {
	upstream := &queuedRelay{results: []*relay.Result{
		response(http.StatusBadRequest, `{"error":"bad request"}`),
	}}
	service, db, highMemberID, _ := setupProxy(t, upstream)
	member, err := db.RouteMember.GetByID(highMemberID)
	if err != nil {
		t.Fatal(err)
	}
	route, err := db.Route.GetByID(member.RouteID)
	if err != nil {
		t.Fatal(err)
	}
	retries := 3
	route.RetryTimes = &retries
	if err := db.Route.Update(route); err != nil {
		t.Fatal(err)
	}
	service.SetCrossChannelFailoverEnabled(false)
	result := service.ChatCompletions(context.Background(), Request{RequestID: "req-no-failover", Model: "model", Body: []byte(`{}`)})
	defer result.Body.Close()
	if result.StatusCode != http.StatusBadRequest || len(upstream.calls) != 1 {
		t.Fatalf("failover disabled must stop after first channel: result=%+v calls=%#v", result, upstream.calls)
	}
}

func TestClientErrorAllChannelsExhausted(t *testing.T) {
	upstream := &queuedRelay{results: []*relay.Result{
		response(http.StatusBadRequest, `{"error":"bad request"}`),
		response(http.StatusBadRequest, `{"error":"bad request"}`),
	}}
	service, db, highMember, lowMember := setupProxy(t, upstream)
	result := service.ChatCompletions(context.Background(), Request{RequestID: "req-4xx-all", Model: "model", Body: []byte(`{}`)})
	defer result.Body.Close()
	// Both channels tried; the last failure is returned to the client.
	if result.StatusCode != http.StatusBadRequest || len(upstream.calls) != 2 {
		t.Fatalf("result=%+v calls=%#v", result, upstream.calls)
	}
	// Both members are cooled down (skipped on subsequent requests)...
	high, _ := db.RouteMember.GetByID(highMember)
	low, _ := db.RouteMember.GetByID(lowMember)
	if high.FailCount != 1 || high.CooldownUntil == nil || low.FailCount != 1 || low.CooldownUntil == nil {
		t.Fatalf("members not cooled: high=%+v low=%+v", high, low)
	}
	// ...but the channel-level consecutive-failure tally is untouched: a bad
	// client request must not auto-disable every channel.
	for _, memberID := range []int64{highMember, lowMember} {
		member, _ := db.RouteMember.GetByID(memberID)
		channel, err := db.Channel.GetByID(member.ChannelID)
		if err != nil {
			t.Fatal(err)
		}
		if channel.Status != domain.StatusEnabled {
			t.Fatalf("channel %d must stay enabled after 4xx, got %s", channel.ID, channel.Status)
		}
	}
}

func TestChannelRetryConfigAddsCustomStatusCodes(t *testing.T) {
	// Each channel tries its key twice (initial + same-key retry) before
	// failing over.
	upstream := &queuedRelay{results: []*relay.Result{
		response(http.StatusBadRequest, `{"error":"bad request"}`),
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
	// Channel config makes 400 retryable → same-key retry, then failover to
	// the second channel (2 re-sends + 1 on the fallback = 3 calls).
	if len(upstream.calls) != 3 {
		t.Fatalf("expected 3 calls with channel retry config, got %#v", upstream.calls)
	}
	if result.StatusCode != http.StatusBadRequest {
		t.Fatalf("result=%+v", result)
	}
}

func TestResolveAPIKeyPoolModelAllowlist(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	enc, _ := crypto.New("allowlist-test-master")
	siteID, _ := db.Site.Create(&domain.Site{Name: "s", Status: domain.StatusEnabled})
	keyA, _ := enc.Encrypt([]byte("sk-allow-a"))
	keyB, _ := enc.Encrypt([]byte("sk-allow-b"))
	credA, _ := db.Credential.Create(&domain.Credential{SiteID: siteID, Kind: "api_key", SecretEnc: []byte(keyA), Status: domain.StatusEnabled, ModelsCSV: "gpt-4*,gpt-5"})
	_, _ = db.Credential.Create(&domain.Credential{SiteID: siteID, Kind: "api_key", SecretEnc: []byte(keyB), Status: domain.StatusEnabled})
	channelID, _ := db.Channel.Create(&domain.Channel{SiteID: &siteID, CredentialID: &credA, Name: "c", Status: domain.StatusEnabled})
	channel, _ := db.Channel.GetByID(channelID)

	service := &Service{db: db, enc: enc}
	service.SetKeyPoolRotation(true)

	// gpt-4o: matches key A (wildcard) and key B (empty = all).
	keys, err := service.resolveAPIKeyPool(*channel, "gpt-4o")
	if err != nil || len(keys) != 2 {
		t.Fatalf("gpt-4o pool=%v err=%v, want both keys", keys, err)
	}
	// gpt-5: exact match on A plus B.
	keys, _ = service.resolveAPIKeyPool(*channel, "gpt-5")
	if len(keys) != 2 {
		t.Fatalf("gpt-5 pool=%v, want both keys", keys)
	}
	// claude: only key B (A's allowlist excludes it).
	keys, _ = service.resolveAPIKeyPool(*channel, "claude-3-5-sonnet")
	if len(keys) != 1 || keys[0] != "sk-allow-b" {
		t.Fatalf("claude pool=%v, want only sk-allow-b", keys)
	}
}

func TestKeyPoolRotationOffUsesBoundKeyOnly(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	enc, _ := crypto.New("rotation-off-test-master")
	siteID, _ := db.Site.Create(&domain.Site{Name: "s", Status: domain.StatusEnabled})
	key1, _ := enc.Encrypt([]byte("sk-rot-1"))
	key2, _ := enc.Encrypt([]byte("sk-rot-2"))
	cred1, _ := db.Credential.Create(&domain.Credential{SiteID: siteID, Kind: "api_key", SecretEnc: []byte(key1), Status: domain.StatusEnabled})
	_, _ = db.Credential.Create(&domain.Credential{SiteID: siteID, Kind: "api_key", SecretEnc: []byte(key2), Status: domain.StatusEnabled})
	channelID, _ := db.Channel.Create(&domain.Channel{SiteID: &siteID, CredentialID: &cred1, Name: "c", Status: domain.StatusEnabled})
	channel, _ := db.Channel.GetByID(channelID)

	service := &Service{db: db, enc: enc}
	service.SetKeyPoolRotation(false)

	// Rotation off: only the bound key is returned, never the pool sibling.
	keys, err := service.resolveAPIKeyPool(*channel, "")
	if err != nil || len(keys) != 1 || keys[0] != "sk-rot-1" {
		t.Fatalf("rotation-off pool=%v err=%v, want only bound key", keys, err)
	}
}

func TestSameKeyResendCountsAreConfigurable(t *testing.T) {
	// Channel retry = 2: the same key is re-sent twice after a 503 before the
	// key pool / channel failover kicks in.
	upstream := &queuedRelay{results: []*relay.Result{
		response(http.StatusServiceUnavailable, `{"error":"busy"}`),
		response(http.StatusServiceUnavailable, `{"error":"busy"}`),
		response(http.StatusServiceUnavailable, `{"error":"busy"}`),
		response(http.StatusOK, `{"ok":true}`),
	}}
	service, db, highMember, _ := setupProxy(t, upstream)
	service.SetChannelRetryTimes(2)

	result := service.ChatCompletions(context.Background(), Request{RequestID: "req-resend", Model: "model", Body: []byte(`{}`)})
	defer result.Body.Close()
	if result.Err != nil || result.StatusCode != http.StatusOK {
		t.Fatalf("unexpected result: %+v", result)
	}
	// 3 sends on high (initial + 2 re-sends), then failover to low succeeds.
	if len(upstream.calls) != 4 {
		t.Fatalf("expected 4 calls (1 initial + 2 re-sends + 1 failover), got %#v", upstream.calls)
	}
	if !strings.Contains(upstream.calls[0], "high.example") || !strings.Contains(upstream.calls[1], "high.example") || !strings.Contains(upstream.calls[2], "high.example") || !strings.Contains(upstream.calls[3], "low.example") {
		t.Fatalf("expected 3 same-key sends on high then failover to low: %#v", upstream.calls)
	}
	high, _ := db.RouteMember.GetByID(highMember)
	if high.FailCount != 1 || high.CooldownUntil == nil {
		t.Fatalf("failed channel must be cooled down after re-sends exhaust: %+v", high)
	}
}

func TestTransportErrorFailsFastAfterSameKeyResend(t *testing.T) {
	// A network error (dial refused) is re-sent on the same key, then the
	// request fails over to the next channel instead of returning early.
	// Transport jitter is not penalized: no cooldown, no failure count.
	upstream := &queuedRelay{results: []*relay.Result{
		{Err: fmt.Errorf("dial tcp 10.0.0.1:443: connect: connection refused")},
		{Err: fmt.Errorf("dial tcp 10.0.0.1:443: connect: connection refused")},
		{Err: fmt.Errorf("dial tcp 10.0.0.2:443: connect: connection refused")},
		{Err: fmt.Errorf("dial tcp 10.0.0.2:443: connect: connection refused")},
	}}
	service, db, highMember, lowMember := setupProxy(t, upstream)

	result := service.ChatCompletions(context.Background(), Request{RequestID: "req-net", Model: "model", Body: []byte(`{}`)})
	if result.Body != nil {
		_ = result.Body.Close()
	}
	if result.Err == nil {
		t.Fatalf("expected transport error, got %+v", result)
	}
	// 2 same-key sends on high, then failover to low (2 sends), both exhausted.
	if len(upstream.calls) != 4 {
		t.Fatalf("expected 4 sends across two channels, got %#v", upstream.calls)
	}
	// Transport jitter must NOT cool the members or count channel failures.
	high, _ := db.RouteMember.GetByID(highMember)
	low, _ := db.RouteMember.GetByID(lowMember)
	if high.FailCount != 0 || high.CooldownUntil != nil {
		t.Fatalf("transport error must not cool high: %+v", high)
	}
	if low.FailCount != 0 || low.CooldownUntil != nil {
		t.Fatalf("transport error must not cool low: %+v", low)
	}
}

func TestRetryExhaustionReturnsLastUpstreamResponse(t *testing.T) {
	mkFinal := func() *relay.Result {
		f := response(http.StatusGatewayTimeout, `{"error":"still busy"}`)
		f.Header.Set("Retry-After", "7")
		return f
	}
	// First channel: 503 then 504 (same-key retry). Fallback channel: 504 twice.
	upstream := &queuedRelay{results: []*relay.Result{
		response(http.StatusServiceUnavailable, `{"error":"busy"}`),
		mkFinal(),
		mkFinal(),
		mkFinal(),
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
	if len(upstream.calls) != 4 {
		t.Fatalf("unexpected calls: %#v", upstream.calls)
	}
	logs, err := db.ProxyLog.List(10)
	if err != nil || len(logs) != 2 {
		t.Fatalf("logs=%+v err=%v", logs, err)
	}
	if logs[0].Status != http.StatusGatewayTimeout || logs[0].Attempt != 2 || logs[1].Status != http.StatusGatewayTimeout || logs[1].Attempt != 1 {
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

func TestStreamFirstByteFailureResendsSameKey(t *testing.T) {
	// First channel returns 200 and then dies before emitting any SSE data; the
	// gateway must re-send on the same key (channel retry = 1) instead of
	// surfacing a silent truncated 200 to the client.
	deadStream := response(http.StatusOK, "")
	okStream := response(http.StatusOK, "data: {\"chunk\":1}\n\n")
	upstream := &queuedRelay{results: []*relay.Result{deadStream, okStream}}
	service, db, highMember, _ := setupProxy(t, upstream)

	result := service.ChatCompletions(context.Background(), Request{
		RequestID: "req-stream", Model: "model", Body: []byte(`{"model":"model","stream":true}`), Stream: true,
	})
	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}
	defer result.Body.Close()
	if len(upstream.calls) != 2 || !strings.Contains(upstream.calls[0], "high.example") || !strings.Contains(upstream.calls[1], "high.example") {
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
	if high.FailCount != 0 || high.CooldownUntil != nil {
		t.Fatalf("same-key retry success must keep the channel healthy: %+v", high)
	}
}

func TestRetryAfterExtendsCooldown(t *testing.T) {
	busy := response(http.StatusServiceUnavailable, `{"error":"busy"}`)
	busy.Header.Set("Retry-After", "3600")
	busy2 := response(http.StatusServiceUnavailable, `{"error":"busy"}`)
	busy2.Header.Set("Retry-After", "3600")
	ok := response(http.StatusOK, `{"ok":true}`)
	upstream := &queuedRelay{results: []*relay.Result{busy, busy2, ok}}
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

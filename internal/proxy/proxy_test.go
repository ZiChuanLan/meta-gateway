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

	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/relay"
	"github.com/lan/meta-gateway/internal/routing"
	"github.com/lan/meta-gateway/internal/store"
)

type queuedRelay struct {
	results []*relay.Result
	calls   []string
}

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

type firstRandom struct{}

func (firstRandom) Intn(int) int { return 0 }

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
		response(http.StatusBadRequest, `{"error":"bad request"}`),
	}}
	service, db, highMember, lowMember := setupProxy(t, upstream)
	result := service.ChatCompletions(context.Background(), Request{RequestID: "req-2", Model: "model", Body: []byte(`{}`)})
	defer result.Body.Close()
	// 4xx is retryable: both channels get tried, both are cooled down, and
	// the last upstream response is returned.
	if result.StatusCode != http.StatusBadRequest || len(upstream.calls) != 2 {
		t.Fatalf("result=%+v calls=%#v", result, upstream.calls)
	}
	high, _ := db.RouteMember.GetByID(highMember)
	low, _ := db.RouteMember.GetByID(lowMember)
	if high.FailCount == 0 || high.CooldownUntil == nil {
		t.Fatalf("high member not cooled down: %+v", high)
	}
	if low.FailCount == 0 || low.CooldownUntil == nil {
		t.Fatalf("low member not cooled down: %+v", low)
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

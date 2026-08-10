package proxy

import (
	"context"
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

// seqRandom returns preloaded Intn values (then 0); used to prove that the
// second request would have picked a different channel without stickiness.
type seqRandom struct{ values []int }

func (r *seqRandom) Intn(n int) int {
	if len(r.values) == 0 {
		return 0
	}
	value := r.values[0]
	r.values = r.values[1:]
	return value % n
}

func (r *seqRandom) Float64() float64 { return 0 }

// setupStickyProxy builds a service with two same-priority channels so the
// weighted pick is the only differentiator between them.
func setupStickyProxy(t *testing.T, upstream Relay) (*Service, *routing.StickyStore, time.Time) {
	t.Helper()
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	enc, err := crypto.New("proxy-sticky-master-key")
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
	channelA := createChannel("a", "https://a.example")
	channelB := createChannel("b", "https://b.example")
	routeID, err := db.Route.Create(&domain.Route{ModelPattern: "model", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.RouteMember.Create(&domain.RouteMember{RouteID: routeID, ChannelID: channelA, Priority: 10, Weight: 100, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.RouteMember.Create(&domain.RouteMember{RouteID: routeID, ChannelID: channelB, Priority: 10, Weight: 100, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC)
	// Sequence: first pick -> A (value 0), second pick would be B (value 100)
	// when stickiness is absent.
	random := &seqRandom{values: []int{0, 100, 0}}
	selector := routing.NewWithDependencies(db.RouteMember, fixedClock{now: now}, random)
	sticky := routing.NewStickyStore(30*time.Minute, fixedClock{now: now})
	selector.SetSticky(sticky)
	service := New(selector, upstream, db, enc, 0, time.Minute)
	service.now = func() time.Time { return now }
	service.SetSticky(sticky)
	return service, sticky, now
}

// setupStickyProxyWithRetries is setupStickyProxy with a configurable retry
// budget (for failover scenarios).
func setupStickyProxyWithRetries(t *testing.T, upstream Relay, retryTimes int) (*Service, *routing.StickyStore, time.Time) {
	t.Helper()
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	enc, err := crypto.New("proxy-sticky-master-key")
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
	channelA := createChannel("a", "https://a.example")
	channelB := createChannel("b", "https://b.example")
	routeID, err := db.Route.Create(&domain.Route{ModelPattern: "model", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.RouteMember.Create(&domain.RouteMember{RouteID: routeID, ChannelID: channelA, Priority: 10, Weight: 100, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.RouteMember.Create(&domain.RouteMember{RouteID: routeID, ChannelID: channelB, Priority: 10, Weight: 100, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC)
	random := &seqRandom{values: []int{0, 100, 0}}
	selector := routing.NewWithDependencies(db.RouteMember, fixedClock{now: now}, random)
	sticky := routing.NewStickyStore(30*time.Minute, fixedClock{now: now})
	selector.SetSticky(sticky)
	service := New(selector, upstream, db, enc, retryTimes, time.Minute)
	service.now = func() time.Time { return now }
	service.SetSticky(sticky)
	return service, sticky, now
}

func TestStickyBindsSuccessfulRelay(t *testing.T) {
	upstream := &queuedRelay{results: []*relay.Result{
		response(http.StatusOK, `{"ok":true}`),
		response(http.StatusOK, `{"ok":true}`),
	}}
	service, sticky, now := setupStickyProxy(t, upstream)

	first := service.ChatCompletions(context.Background(), Request{RequestID: "r1", Model: "model", Body: []byte(`{"model":"model"}`), SessionKey: "sess-1"})
	defer first.Body.Close()
	if first.Err != nil || first.StatusCode != http.StatusOK {
		t.Fatalf("first request failed: %+v", first)
	}
	if len(upstream.calls) != 1 || !strings.Contains(upstream.calls[0], "a.example") {
		t.Fatalf("first pick must land on a.example, got %#v", upstream.calls)
	}
	if _, ok := sticky.Lookup("sess-1", now); !ok {
		t.Fatal("successful relay must bind the session key")
	}

	// Second request with the same session: without stickiness the seqRandom
	// would pick b.example; with the binding it must stay on a.example.
	second := service.ChatCompletions(context.Background(), Request{RequestID: "r2", Model: "model", Body: []byte(`{"model":"model"}`), SessionKey: "sess-1"})
	defer second.Body.Close()
	if second.Err != nil || second.StatusCode != http.StatusOK {
		t.Fatalf("second request failed: %+v", second)
	}
	if len(upstream.calls) != 2 || !strings.Contains(upstream.calls[1], "a.example") {
		t.Fatalf("sticky request must stay on a.example, got %#v", upstream.calls)
	}
	// Two successful relays: one bind for each (the second refreshes), and the
	// second request is a sticky hit.
	if stats := sticky.Stats(); stats.Hits != 1 || stats.Binds != 2 {
		t.Fatalf("expected 1 hit and 2 binds (bind + refresh), got %+v", stats)
	}
}

func TestStickyDoesNotBindFailedRelay(t *testing.T) {
	// The 503 is retried on the same key (channel retry = 1) and succeeds, so
	// the session binds to the channel that actually served it (a, id 1).
	upstream := &queuedRelay{results: []*relay.Result{
		response(http.StatusServiceUnavailable, `{"error":"down"}`),
		response(http.StatusOK, `{"ok":true}`),
		response(http.StatusOK, `{"ok":true}`),
	}}
	service, sticky, now := setupStickyProxyWithRetries(t, upstream, 1)

	// First request fails on A once, is re-sent on the same key and succeeds
	// there — no cross-channel fallback needed.
	first := service.ChatCompletions(context.Background(), Request{RequestID: "r1", Model: "model", Body: []byte(`{"model":"model"}`), SessionKey: "sess-1"})
	defer first.Body.Close()
	if first.Err != nil || first.StatusCode != http.StatusOK {
		t.Fatalf("first request must succeed via same-key retry: %+v", first)
	}
	bound, ok := sticky.Lookup("sess-1", now)
	if !ok || !strings.Contains(upstream.calls[1], "a.example") || bound != 1 {
		t.Fatalf("session must be bound to channel a (served the retry), got bound=%d ok=%v calls=%#v", bound, ok, upstream.calls)
	}

	second := service.ChatCompletions(context.Background(), Request{RequestID: "r2", Model: "model", Body: []byte(`{"model":"model"}`), SessionKey: "sess-1"})
	defer second.Body.Close()
	if len(upstream.calls) != 3 || !strings.Contains(upstream.calls[2], "a.example") {
		t.Fatalf("second request must reuse the bound channel a, got %#v", upstream.calls)
	}
}

func TestStickyContentDigestSessionKey(t *testing.T) {
	upstream := &queuedRelay{results: []*relay.Result{
		response(http.StatusOK, `{"ok":true}`),
		response(http.StatusOK, `{"ok":true}`),
	}}
	service, _, _ := setupStickyProxy(t, upstream)
	body := []byte(`{"model":"model","messages":[{"role":"user","content":"hello sticky"}]}`)

	// No explicit session key: the content digest must drive stickiness.
	first := service.ChatCompletions(context.Background(), Request{RequestID: "r1", Model: "model", Body: body})
	defer first.Body.Close()
	second := service.ChatCompletions(context.Background(), Request{RequestID: "r2", Model: "model", Body: body})
	defer second.Body.Close()
	if len(upstream.calls) != 2 || upstream.calls[0] != upstream.calls[1] {
		t.Fatalf("content-digest sessions must stick to one channel, got %#v", upstream.calls)
	}
}

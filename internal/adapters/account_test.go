package adapters

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestAuthAutoAccountFallback covers the documented auto-mode cookie
// fallback: a GET the upstream rejects with 401 while holding a valid cookie
// is retried once with the Cookie credential.
func TestAuthAutoAccountFallback(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "" {
			http.Error(w, `{"success":false}`, http.StatusUnauthorized)
			return
		}
		if r.Header.Get("Cookie") != "session=cookie-value" {
			http.Error(w, `{"success":false}`, http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"data":{"id":9,"username":"auto-user"}}`))
	}))
	defer server.Close()
	adapter := NewNewAPIAccountAdapter("new-api", server.Client(), true)
	self, err := adapter.ProbeSelf(context.Background(), AccountInput{
		BaseURL: server.URL, Secret: "user-token", Cookie: "session=cookie-value", AuthMode: AuthAuto,
	})
	if err != nil {
		t.Fatalf("auto fallback: %v", err)
	}
	if self.Username != "auto-user" || calls != 2 {
		t.Fatalf("self=%+v calls=%d", self, calls)
	}
}

func TestNewAPIAccountAdapterCookieAuth(t *testing.T) {
	var gotAuth, gotCookie string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotCookie = r.Header.Get("Cookie")
		_, _ = w.Write([]byte(`{"success":true,"data":{"id":42,"username":"cookie-user"}}`))
	}))
	defer server.Close()
	adapter := NewNewAPIAccountAdapter("new-api", server.Client(), true)
	_, err := adapter.ProbeSelf(context.Background(), AccountInput{
		BaseURL: server.URL, Secret: "must-not-be-bearer", Cookie: "session=cookie-value", AuthMode: AuthCookie,
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "" || gotCookie != "session=cookie-value" {
		t.Fatalf("auth=%q cookie=%q", gotAuth, gotCookie)
	}
}

func TestNewAPIAccountProbeSelfAndListKeys(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer user-token" {
			t.Fatalf("auth=%q", r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/api/user/self":
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":42,"username":"alice","quota":100,"used_quota":3}}`))
		case "/api/token/":
			_, _ = w.Write([]byte(`{"success":true,"data":{"items":[{"id":7,"name":"main","key":"sk-live-1","status":1},{"id":8,"name":"masked","key":"sk-****abcd","status":1}]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	adapter := NewNewAPIAccountAdapter("new-api", server.Client(), true)
	self, err := adapter.ProbeSelf(context.Background(), AccountInput{
		BaseURL: server.URL + "/v1",
		Secret:  "user-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	if self.Username != "alice" || self.PlatformUserID != 42 || self.Quota == nil || *self.Quota != 100 {
		t.Fatalf("self=%+v", self)
	}
	keys, err := adapter.ListAPIKeys(context.Background(), AccountInput{
		BaseURL: server.URL,
		Secret:  "user-token",
	}, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 {
		t.Fatalf("keys=%d", len(keys))
	}
	if keys[0].Secret != "sk-live-1" {
		t.Fatalf("secret0=%q", keys[0].Secret)
	}
	if keys[1].Secret != "" {
		t.Fatalf("masked secret should be empty, got %q", keys[1].Secret)
	}
}

// TestListAPIKeysRetriesWithUserHeaderWhen401 mirrors New-API forks that
// require the New-Api-User compat header on /api/token/ while tolerating its
// absence on /api/user/self: the one-api adapter (userHeader=false) must still
// succeed by retrying with headers once a user id is known.
func TestListAPIKeysRetriesWithUserHeaderWhen401(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/token/" {
			http.NotFound(w, r)
			return
		}
		// First attempt has no user-id header → 401. Retry carries it → ok.
		if r.Header.Get("New-Api-User") == "" {
			http.Error(w, `{"success":false,"message":"missing user header"}`, http.StatusUnauthorized)
			return
		}
		if r.Header.Get("New-Api-User") != "42" {
			t.Fatalf("New-Api-User=%q", r.Header.Get("New-Api-User"))
		}
		_, _ = w.Write([]byte(`{"success":true,"data":[{"id":7,"name":"main","key":"sk-live-1","status":1}]}`))
	}))
	t.Cleanup(server.Close)

	adapter := NewNewAPIAccountAdapter("one-api", server.Client(), false)
	keys, err := adapter.ListAPIKeys(context.Background(), AccountInput{
		BaseURL:        server.URL,
		Secret:         "user-token",
		PlatformUserID: 42,
	}, 0, 20)
	if err != nil {
		t.Fatalf("list keys with header retry: %v", err)
	}
	if len(keys) != 1 || keys[0].Secret != "sk-live-1" {
		t.Fatalf("keys=%+v", keys)
	}
	if calls < 2 {
		t.Fatalf("expected a retry after 401, calls=%d", calls)
	}
}

// TestListAPIKeysDropsUserHeaderWhen401 mirrors forks that reject the compat
// headers (e.g. a stale stored user id): the new-api adapter (userHeader=true)
// must retry without them.
func TestListAPIKeysDropsUserHeaderWhen401(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/token/" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("New-Api-User") != "" {
			http.Error(w, `{"success":false,"message":"header rejected"}`, http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"data":[{"id":7,"name":"main","key":"sk-live-2","status":1}]}`))
	}))
	t.Cleanup(server.Close)

	adapter := NewNewAPIAccountAdapter("new-api", server.Client(), true)
	keys, err := adapter.ListAPIKeys(context.Background(), AccountInput{
		BaseURL:        server.URL,
		Secret:         "user-token",
		PlatformUserID: 42,
	}, 0, 20)
	if err != nil {
		t.Fatalf("list keys without header: %v", err)
	}
	if len(keys) != 1 || keys[0].Secret != "sk-live-2" {
		t.Fatalf("keys=%+v", keys)
	}
}

// TestPricingParsesPlainAndObjectForms covers the two /api/pricing shapes:
// plain numbers (default currency) and {currency, price} objects.
func TestPricingParsesPlainAndObjectForms(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/pricing" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"data":{
			"gpt-4o": 0.005,
			"deepseek-v4": {"currency":"CNY","price":2.0},
			"claude-sonnet": {"currency":"USD","price":3.0}
		}}`))
	}))
	t.Cleanup(server.Close)

	adapter := NewNewAPIAccountAdapter("new-api", server.Client(), true)
	prices, err := adapter.Pricing(context.Background(), AccountInput{BaseURL: server.URL, Secret: "user-token"})
	if err != nil {
		t.Fatalf("pricing: %v", err)
	}
	if len(prices) != 3 {
		t.Fatalf("prices=%d", len(prices))
	}
	byModel := map[string]ModelPrice{}
	for _, p := range prices {
		byModel[p.Model] = p
	}
	if byModel["gpt-4o"].QuotaPer1M != 0.005 || byModel["gpt-4o"].Currency != "" {
		t.Fatalf("plain price: %+v", byModel["gpt-4o"])
	}
	if byModel["deepseek-v4"].PriceUSD != 2.0 || byModel["deepseek-v4"].Currency != "CNY" {
		t.Fatalf("object price: %+v", byModel["deepseek-v4"])
	}
}

// TestPricingParsesNewListForm covers the New API v0.13+ /api/pricing list
// shape: [{model_name, model_price, model_ratio}]. Prices are quota per 1M
// tokens; ratio × 1M when model_price is unset.
func TestPricingParsesNewListForm(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/pricing" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"data":[
			{"model_name":"deepseek-v4-flash","model_price":0,"model_ratio":0.03125},
			{"model_name":"gpt-4o","model_price":10,"model_ratio":0},
			{"model_name":"free-model","model_price":0,"model_ratio":0}
		]}`))
	}))
	t.Cleanup(server.Close)

	adapter := NewNewAPIAccountAdapter("new-api", server.Client(), true)
	prices, err := adapter.Pricing(context.Background(), AccountInput{BaseURL: server.URL, Secret: "user-token"})
	if err != nil {
		t.Fatalf("pricing list: %v", err)
	}
	byModel := map[string]ModelPrice{}
	for _, p := range prices {
		byModel[p.Model] = p
	}
	// ratio mode keeps the raw ratio; the service converts to USD/1M later.
	if byModel["deepseek-v4-flash"].Ratio != 0.03125 || byModel["deepseek-v4-flash"].PriceUSD != 0 {
		t.Fatalf("ratio price: %+v", byModel["deepseek-v4-flash"])
	}
	if byModel["gpt-4o"].PriceUSD != 10 {
		t.Fatalf("model_price: %+v", byModel["gpt-4o"])
	}
	if _, exists := byModel["free-model"]; exists {
		t.Fatalf("unpriced model must be skipped")
	}
}

package adapters

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRetryAfterFromHeader(t *testing.T) {
	tests := []struct {
		name   string
		header http.Header
		want   time.Duration
	}{
		{name: "whole seconds", header: http.Header{"Retry-After": {"5"}}, want: 5 * time.Second},
		{name: "http-date", header: http.Header{"Retry-After": {time.Now().Add(90 * time.Second).UTC().Format(http.TimeFormat)}}, want: 90 * time.Second},
		{name: "absent", header: http.Header{}, want: 0},
		{name: "nil", header: nil, want: 0},
		{name: "garbage", header: http.Header{"Retry-After": {"soon-ish"}}, want: 0},
		{name: "zero seconds means unknown", header: http.Header{"Retry-After": {"0"}}, want: 0},
		{name: "expired http-date", header: http.Header{"Retry-After": {time.Now().Add(-time.Minute).UTC().Format(http.TimeFormat)}}, want: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := retryAfterFromHeader(tc.header)
			if tc.want == 0 {
				if got != 0 {
					t.Fatalf("got %v, want 0", got)
				}
				return
			}
			if diff := got - tc.want; diff < -time.Second || diff > time.Second {
				t.Fatalf("got %v, want ~%v", got, tc.want)
			}
		})
	}
}

// TestModelAdaptersPropagateRetryAfter verifies the 429 Retry-After hint is
// carried into adapters.Error.RetryAfter for every model-listing adapter,
// so account.retryAfterFrom can honor upstream rate-limit pauses.
func TestModelAdaptersPropagateRetryAfter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "5")
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	t.Cleanup(server.Close)

	type adapterCase struct {
		name    string
		adapter ModelAdapter
		key     string
	}
	cases := []adapterCase{
		{name: "openai", adapter: NewOpenAIModelAdapter("openai-compatible", server.Client()), key: "k"},
		{name: "anthropic", adapter: NewAnthropicModelAdapter("anthropic", server.Client()), key: "k"},
		{name: "gemini", adapter: NewGeminiModelAdapter("gemini", server.Client()), key: "k"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.adapter.ListModels(context.Background(), server.URL, tc.key)
			if err == nil {
				t.Fatal("want error")
			}
			var adapterErr *Error
			if !errors.As(err, &adapterErr) {
				t.Fatalf("err=%T, want *Error", err)
			}
			if adapterErr.Status != http.StatusTooManyRequests {
				t.Fatalf("status=%d", adapterErr.Status)
			}
			if adapterErr.RetryAfter != 5*time.Second {
				t.Fatalf("RetryAfter=%v, want 5s", adapterErr.RetryAfter)
			}
		})
	}
}

// TestAccountAdapterPropagatesRetryAfter verifies doJSON-based account probes
// carry the upstream Retry-After hint into adapters.Error.RetryAfter.
func TestAccountAdapterPropagatesRetryAfter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7")
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	t.Cleanup(server.Close)

	adapter := NewNewAPIAccountAdapter("new-api", server.Client(), true)
	_, err := adapter.QuotaPerUnit(context.Background(), AccountInput{
		BaseURL: server.URL,
		Secret:  "user-token",
	})
	if err == nil {
		t.Fatal("want error")
	}
	var adapterErr *Error
	if !errors.As(err, &adapterErr) {
		t.Fatalf("err=%T, want *Error", err)
	}
	if adapterErr.Status != http.StatusTooManyRequests {
		t.Fatalf("status=%d", adapterErr.Status)
	}
	if adapterErr.RetryAfter != 7*time.Second {
		t.Fatalf("RetryAfter=%v, want 7s", adapterErr.RetryAfter)
	}
}

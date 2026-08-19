package adapters

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExternalCheckinAdapter(t *testing.T) {
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/api/checkin/spin", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if got := r.Header.Get("Cookie"); got != "auth_token=abc123" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.Header.Get("Origin"); got != srv.URL {
			t.Errorf("origin = %q, want %q", got, srv.URL)
		}
		if got := r.Header.Get("Referer"); got != srv.URL+"/" {
			t.Errorf("referer = %q, want %q", got, srv.URL+"/")
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("content-type = %q, want application/json", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"success":true,"message":"签到成功","data":{"reward":"100 积分"}}`))
	})
	mux.HandleFunc("/api/checkin/already", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"success":false,"message":"今日已签到"}`))
	})
	mux.HandleFunc("/api/checkin/plain", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("/api/checkin/fail", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"success":false,"message":"会话过期"}`))
	})
	mux.HandleFunc("/api/checkin/already400", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"success":false,"message":"今日已签到"}`))
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	adapter := NewExternalCheckinAdapter("external-checkin", srv.Client())

	// Success with reward.
	result, err := adapter.Checkin(context.Background(), CheckinInput{
		BaseURL: srv.URL,
		Cookie:  "auth_token=abc123",
	})
	if err != nil {
		t.Fatalf("checkin: %v", err)
	}
	if result.Outcome != CheckinSuccess || result.Reward != "100 积分" {
		t.Fatalf("result = %+v", result)
	}

	// Already checked in (success:false + marker message) → success outcome.
	result, err = adapter.Checkin(context.Background(), CheckinInput{
		BaseURL:       srv.URL,
		Cookie:        "auth_token=abc123",
		CheckinPath:   "/api/checkin/already",
		CheckinMethod: http.MethodGet,
	})
	if err != nil {
		t.Fatalf("already: %v", err)
	}
	if result.Category != "already_checked_in" {
		t.Fatalf("already category = %s", result.Category)
	}

	// Non-JSON 2xx still counts as success.
	result, err = adapter.Checkin(context.Background(), CheckinInput{
		BaseURL:       srv.URL,
		Cookie:        "auth_token=abc123",
		CheckinPath:   "/api/checkin/plain",
		CheckinMethod: http.MethodGet,
	})
	if err != nil || result.Outcome != CheckinSuccess {
		t.Fatalf("plain = %+v err=%v", result, err)
	}

	// Non-2xx keeps the upstream's safe message so the operator sees the real
	// reason instead of a generic HTTP 400.
	if _, err := adapter.Checkin(context.Background(), CheckinInput{
		BaseURL:       srv.URL,
		Cookie:        "auth_token=abc123",
		CheckinPath:   "/api/checkin/fail",
		CheckinMethod: http.MethodGet,
	}); err == nil || !strings.Contains(err.Error(), "会话过期") {
		t.Fatalf("expected HTTP error with upstream message, got %v", err)
	}

	// Some sites return HTTP 400 for an already-completed daily check-in; that
	// remains a successful idempotent outcome.
	result, err = adapter.Checkin(context.Background(), CheckinInput{
		BaseURL:       srv.URL,
		Cookie:        "auth_token=abc123",
		CheckinPath:   "/api/checkin/already400",
		CheckinMethod: http.MethodGet,
	})
	if err != nil || result.Category != "already_checked_in" {
		t.Fatalf("already400 result=%+v err=%v", result, err)
	}

	// 401 with a wrong cookie surfaces as a status error.
	if _, err := adapter.Checkin(context.Background(), CheckinInput{
		BaseURL: srv.URL,
		Cookie:  "auth_token=wrong",
	}); err == nil {
		t.Fatal("expected 401 error")
	}

	// Missing cookie is rejected up front.
	if _, err := adapter.Checkin(context.Background(), CheckinInput{BaseURL: srv.URL}); err == nil {
		t.Fatal("expected missing-cookie error")
	}

	// Default path matches the 薄荷公益站 convention.
	if DefaultExternalCheckinPath != "/api/checkin/spin" {
		t.Fatalf("default path = %s", DefaultExternalCheckinPath)
	}
}

// TestExternalCheckinAdapterCustomHeaders verifies extra headers (e.g.
// new-api-user for New-API forks signing in via /api/user/sign_in) reach the
// upstream, while Host/Cookie cannot be overridden.
func TestExternalCheckinAdapterCustomHeaders(t *testing.T) {
	var gotUser, gotCookie string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser = r.Header.Get("New-Api-User")
		gotCookie = r.Header.Get("Cookie")
		_, _ = w.Write([]byte(`{"success":true,"data":{"reward":"5 积分"}}`))
	}))
	t.Cleanup(srv.Close)

	adapter := NewExternalCheckinAdapter("external-checkin", srv.Client())
	_, err := adapter.Checkin(context.Background(), CheckinInput{
		BaseURL: srv.URL,
		Cookie:  "session=abc",
		Headers: map[string]string{
			"New-Api-User": "68760",
			"X-Host":       "evil",
			"Host":         "evil",
			"Cookie":       "evil",
		},
	})
	if err != nil {
		t.Fatalf("checkin with headers: %v", err)
	}
	if gotUser != "68760" {
		t.Fatalf("new-api-user = %q, want 68760", gotUser)
	}
	if gotCookie != "session=abc" {
		t.Fatalf("cookie = %q, want untouched session=abc", gotCookie)
	}
}

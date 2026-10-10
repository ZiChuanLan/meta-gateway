package httpapi

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestClientIPWarnsWhenForwardingHeadersAreIgnored: a deployment behind a proxy
// that nobody trusts collapses every caller into the proxy's address, and nothing
// in the traffic says so — the rate-limit buckets and the audit trail just quietly
// stop being per-client. The resolver has to say it once, naming the peer the
// operator has to trust.
func TestClientIPWarnsWhenForwardingHeadersAreIgnored(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))
	resolver, _ := newClientIPResolver(nil, logger)
	handler := resolver.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if got := ClientIP(r).String(); got != "127.0.0.1" {
			t.Fatalf("client IP=%s", got)
		}
	}))
	for i := 0; i < 3; i++ {
		request := httptest.NewRequest(http.MethodPost, "/admin/session", nil)
		request.RemoteAddr = "127.0.0.1:51000"
		request.Header.Set("X-Forwarded-For", "203.0.113.7, 10.0.0.1")
		handler.ServeHTTP(httptest.NewRecorder(), request)
	}
	output := logs.String()
	if !strings.Contains(output, "forwarding header ignored") || !strings.Contains(output, "peer=127.0.0.1") {
		t.Fatalf("no diagnostic for the ignored proxy header: %q", output)
	}
	if got := strings.Count(output, "forwarding header ignored"); got != 1 {
		t.Fatalf("warned %d times; a deployment fact belongs in one line", got)
	}
	if strings.Contains(output, "203.0.113.7") {
		t.Fatalf("the caller-supplied header leaked into the log: %q", output)
	}
}

// TestClientIPStaysQuietWhenTheProxyIsTrusted: the notice is for the mistake, not
// for normal operation.
func TestClientIPStaysQuietWhenTheProxyIsTrusted(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))
	resolver, _ := newClientIPResolver([]string{"10.0.0.0/8"}, logger)
	handler := resolver.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if got := ClientIP(r).String(); got != "203.0.113.7" {
			t.Fatalf("client IP=%s", got)
		}
	}))
	request := httptest.NewRequest(http.MethodPost, "/admin/session", nil)
	request.RemoteAddr = "10.0.0.2:51000"
	request.Header.Set("X-Forwarded-For", "203.0.113.7")
	handler.ServeHTTP(httptest.NewRecorder(), request)
	if logs.Len() != 0 {
		t.Fatalf("a correctly trusted proxy warned: %q", logs.String())
	}
}

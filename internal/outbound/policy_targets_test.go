package outbound

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// A gateway-owned destination (its own plugin process) is reachable even though
// the address rules refuse loopback; the exception is keyed on the exact
// endpoint, so the same host on another port stays blocked.
func TestTargetAllowlistOpensOneEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	host, port := hostPort(t, server.URL)
	policy, err := NewPolicy(Options{})
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{host + ":" + port: true}
	policy.SetTargetAllowlist(func(h, p string) bool { return allowed[h+":"+p] })

	response, err := NewClient(policy, ClientOptions{}).Get(server.URL)
	if err != nil {
		t.Fatalf("allowed endpoint still blocked: %v", err)
	}
	response.Body.Close()

	// Same host, different port: the exception must not widen.
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer other.Close()
	if _, err := NewClient(policy, ClientOptions{}).Get(other.URL); !errors.Is(err, ErrBlocked) {
		t.Fatalf("unregistered port reached: %v", err)
	}
}

func TestTargetAllowlistValidatesDestinations(t *testing.T) {
	policy, err := NewPolicy(Options{})
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse("http://127.0.0.1:44215/v1")
	if err := policy.ValidateDestination(context.Background(), u); !errors.Is(err, ErrBlocked) {
		t.Fatalf("loopback destination accepted without an exception: %v", err)
	}
	policy.SetTargetAllowlist(func(host, port string) bool {
		return host == "127.0.0.1" && port == "44215"
	})
	if err := policy.ValidateDestination(context.Background(), u); err != nil {
		t.Fatalf("endpoint rejected despite the exception: %v", err)
	}
	// A URL with no explicit port is never covered: the exception names an
	// endpoint, not a host.
	bare, _ := url.Parse("http://127.0.0.1/v1")
	if err := policy.ValidateDestination(context.Background(), bare); !errors.Is(err, ErrBlocked) {
		t.Fatalf("portless URL accepted: %v", err)
	}
}

func hostPort(t *testing.T, raw string) (string, string) {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Hostname(), u.Port()
}

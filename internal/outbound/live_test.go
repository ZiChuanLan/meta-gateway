package outbound

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// The point of LiveClient is that a settings change reaches traffic without a
// restart, so the test drives it the way the console does: build with one
// ceiling, prove it applies, Rebuild with another, prove the same request now
// behaves differently. No mocks — a real slow upstream and the real transport.
func TestLiveClientRebuildAppliesTimeouts(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer slow.Close()

	policy, err := NewPolicy(Options{AllowCIDRs: []string{"127.0.0.0/8", "::1/128"}})
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	client := NewLiveClient(policy, ClientOptions{ResponseHeaderTimeout: 50 * time.Millisecond})

	if _, err := client.Get(slow.URL); err == nil {
		t.Fatal("a 50ms header ceiling let a 300ms upstream through")
	}

	client.Rebuild(ClientOptions{ResponseHeaderTimeout: 2 * time.Second})
	response, err := client.Get(slow.URL)
	if err != nil {
		t.Fatalf("after Rebuild the longer ceiling did not apply: %v", err)
	}
	response.Body.Close()

	// And back: the knob has to work in both directions.
	client.Rebuild(ClientOptions{ResponseHeaderTimeout: 50 * time.Millisecond})
	if _, err := client.Get(slow.URL); err == nil {
		t.Fatal("Rebuild back to 50ms did not apply")
	}
}

// A rebuild must not silently drop the proxy hook, which is installed after the
// client is built (the global outbound proxy is a runtime setting too).
func TestLiveClientRebuildKeepsTheProxyHook(t *testing.T) {
	policy, err := NewPolicy(Options{AllowCIDRs: []string{"127.0.0.0/8", "::1/128"}})
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	client := NewLiveClient(policy, ClientOptions{})

	calls := 0
	if !SetClientProxy(client.Client, func(*http.Request) (*url.URL, error) {
		calls++
		return nil, nil
	}) {
		t.Fatal("SetClientProxy did not recognize the live client")
	}
	client.Rebuild(ClientOptions{ResponseHeaderTimeout: time.Second})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	response, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	response.Body.Close()
	if calls == 0 {
		t.Fatal("the proxy hook was dropped by Rebuild")
	}
}

// The connect timeout lives on the policy's dialer, so it has its own setter and
// has to reach the transport the client is currently using.
func TestPolicySetDialTimeoutReachesNewTransports(t *testing.T) {
	policy, err := NewPolicy(Options{AllowCIDRs: []string{"127.0.0.0/8", "::1/128"}})
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	policy.SetDialTimeout(1500 * time.Millisecond)
	dialer, ok := policy.currentDialer().(*net.Dialer)
	if !ok {
		t.Fatalf("built-in dialer is %T, want *net.Dialer", policy.currentDialer())
	}
	if dialer.Timeout != 1500*time.Millisecond {
		t.Fatalf("dialer timeout = %v, want 1.5s", dialer.Timeout)
	}

	// An injected dialer is the test's own and must not be replaced.
	custom := &net.Dialer{Timeout: 42 * time.Millisecond}
	injected, err := NewPolicy(Options{Dialer: custom, AllowCIDRs: []string{"127.0.0.0/8"}})
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	injected.SetDialTimeout(time.Second)
	if injected.currentDialer() != Dialer(custom) {
		t.Fatal("SetDialTimeout replaced an injected dialer")
	}
}

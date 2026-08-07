package outbound

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"
)

type resolverMap map[string][]netip.Addr

func (r resolverMap) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	values, ok := r[host]
	if !ok {
		return nil, errors.New("not found")
	}
	return values, nil
}

func TestClientBlocksLoopbackUnlessExplicitlyAllowed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	blocked, _ := NewPolicy(Options{})
	if _, err := NewClient(blocked, ClientOptions{}).Get(server.URL); !errors.Is(err, ErrBlocked) {
		t.Fatalf("expected loopback block, got %v", err)
	}

	allowed, _ := NewPolicy(Options{AllowCIDRs: []string{"127.0.0.0/8", "::1/128"}})
	response, err := NewClient(allowed, ClientOptions{}).Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("status=%d", response.StatusCode)
	}
}

func TestClientRevalidatesRedirectAndStripsCredentials(t *testing.T) {
	var gotAuth, gotCookie string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotCookie = r.Header.Get("Cookie")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirect.Close()

	policy, _ := NewPolicy(Options{AllowCIDRs: []string{"127.0.0.0/8", "::1/128"}})
	request, _ := http.NewRequest(http.MethodGet, redirect.URL, nil)
	request.Header.Set("Authorization", "Bearer secret")
	request.Header.Set("Cookie", "session=secret")
	response, err := NewClient(policy, ClientOptions{}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if gotAuth != "" || gotCookie != "" {
		t.Fatalf("redirect forwarded credentials: auth=%q cookie=%q", gotAuth, gotCookie)
	}

	blocked, _ := NewPolicy(Options{})
	if _, err := NewClient(blocked, ClientOptions{}).Get(redirect.URL); !errors.Is(err, ErrBlocked) {
		t.Fatalf("expected redirect path to be blocked, got %v", err)
	}
}

func TestPolicyAddressRules(t *testing.T) {
	policy, err := NewPolicy(Options{AllowCIDRs: []string{"10.20.0.0/16"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "192.0.2.1", "::1", "fc00::1"} {
		if policy.addressAllowed(netip.MustParseAddr(value)) {
			t.Fatalf("expected %s to be blocked", value)
		}
	}
	if !policy.addressAllowed(netip.MustParseAddr("10.20.1.2")) || !policy.addressAllowed(netip.MustParseAddr("8.8.8.8")) {
		t.Fatal("expected configured private CIDR and public address to be allowed")
	}
	if policy.addressAllowed(netip.MustParseAddr("::ffff:127.0.0.1")) {
		t.Fatal("mapped loopback bypassed policy")
	}
}

func TestPolicyBlocksNAT64AndSpecialIPv6(t *testing.T) {
	policy, err := NewPolicy(Options{})
	if err != nil {
		t.Fatal(err)
	}
	// NAT64 well-known prefix (RFC 6052) encoding loopback/metadata/private.
	for _, value := range []string{
		"64:ff9b::7f00:1",   // 127.0.0.1 via NAT64
		"64:ff9b::a9fe:a9fe", // 169.254.169.254 via NAT64
		"64:ff9b::a00:1",     // 10.0.0.1 via NAT64
		"64:ff9b:1::7f00:1",  // local-use NAT64 (RFC 8215)
		"::7f00:1",           // IPv4-compatible loopback
		"2001:20::1",         // ORCHIDv2
	} {
		if policy.addressAllowed(netip.MustParseAddr(value)) {
			t.Fatalf("expected %s to be blocked", value)
		}
	}
}

func TestClientIgnoresEnvironmentProxy(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:9999")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:9999")
	t.Setenv("NO_PROXY", "")
	policy, err := NewPolicy(Options{})
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(policy, ClientOptions{})
	transport, ok := client.Transport.(validatingTransport)
	if !ok {
		t.Fatalf("unexpected transport type %T", client.Transport)
	}
	inner, ok := transport.next.(*http.Transport)
	if !ok {
		t.Fatalf("unexpected inner transport %T", transport.next)
	}
	if inner.Proxy != nil {
		t.Fatal("environment proxy must be disabled (SSRF contract)")
	}
	// With a loopback exception, the client must dial directly (no proxy hop).
	proxyTrap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("request went through the environment proxy")
	}))
	defer proxyTrap.Close()
	allowPolicy, err := NewPolicy(Options{AllowCIDRs: []string{"127.0.0.0/8"}})
	if err != nil {
		t.Fatal(err)
	}
	allowedClient := NewClient(allowPolicy, ClientOptions{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer server.Close()
	if _, err := allowedClient.Get(server.URL); err != nil {
		t.Fatalf("direct dial must succeed without proxy: %v", err)
	}
}

func TestPolicyHostAllowlistIsExact(t *testing.T) {
	policy, err := NewPolicy(Options{AllowHosts: []string{"internal.example"}})
	if err != nil {
		t.Fatal(err)
	}
	if !policy.hostAllowed("internal.example") || policy.hostAllowed("sub.internal.example") {
		t.Fatal("hostname allowlist was not exact")
	}
}

func TestPolicyRejectsInvalidConfigurationAndURLs(t *testing.T) {
	for _, opts := range []Options{{AllowHosts: []string{"https://example.com"}}, {AllowCIDRs: []string{"bad"}}} {
		if _, err := NewPolicy(opts); err == nil {
			t.Fatal("expected invalid policy configuration")
		}
	}
	policy, _ := NewPolicy(Options{})
	for _, value := range []string{"relative", "ftp://example.com", "https://user:pass@example.com", "http://example.com:99999"} {
		if err := policy.ValidateURL(value); err == nil {
			t.Fatalf("expected URL %q to be rejected", value)
		}
	}
}

func TestDialNeverAttemptsDeniedDNSAnswer(t *testing.T) {
	policy, err := NewPolicy(Options{
		Resolver: resolverMap{"blocked.example": {netip.MustParseAddr("127.0.0.1")}},
		Dialer:   &net.Dialer{Timeout: time.Millisecond},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = policy.DialContext(t.Context(), "tcp", "blocked.example:80")
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("err=%v", err)
	}
}

// recordingDialer records the exact address passed to the socket dial, so a
// test can prove DialContext dials the validated IP — never re-resolving the
// hostname (which is what makes DNS-rebinding attacks impossible: the answer
// is validated once and pinned). It embeds net.Dialer so it fits the policy's
// *net.Dialer field while overriding DialContext.
type recordingDialer struct {
	net.Dialer
	addresses []string
}

func (d *recordingDialer) DialContext(_ context.Context, network, address string) (net.Conn, error) {
	d.addresses = append(d.addresses, address)
	return nil, errors.New("no real dial in test")
}

// TestDialPinsValidatedIPAgainstDNSRebinding: the first resolution returns a
// public IP (allowed); a rebinding attack would make a second resolution
// return a private IP. DialContext must dial the pinned public IP directly
// and never re-resolve the hostname — the socket layer never sees the
// hostname, so the attack cannot land.
func TestDialPinsValidatedIPAgainstDNSRebinding(t *testing.T) {
	var dialer recordingDialer
	policy, err := NewPolicy(Options{
		Resolver: resolverMap{"rebind.example": {netip.MustParseAddr("93.184.216.34")}},
		Dialer:   &dialer,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = policy.DialContext(t.Context(), "tcp", "rebind.example:443")
	if len(dialer.addresses) != 1 {
		t.Fatalf("expected exactly one dial attempt, got %#v", dialer.addresses)
	}
	// The dial target must be the IP:port — the hostname must never reach
	// the socket layer (no second resolution = no rebinding window).
	if dialer.addresses[0] != "93.184.216.34:443" {
		t.Fatalf("dialed %q, want pinned IP 93.184.216.34:443", dialer.addresses[0])
	}
}

// TestDialRejectsPrivateAfterPublicMix: when DNS returns a mix of public and
// private answers, only the public one is dialable; private answers are
// skipped (not dialed).
func TestDialSkipsPrivateAnswersInMixedSet(t *testing.T) {
	var dialer recordingDialer
	policy, err := NewPolicy(Options{
		Resolver: resolverMap{"mixed.example": {
			netip.MustParseAddr("10.0.0.5"),   // private — must be skipped
			netip.MustParseAddr("93.184.216.34"), // public — dialed
		}},
		Dialer: &dialer,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = policy.DialContext(t.Context(), "tcp", "mixed.example:443")
	if len(dialer.addresses) != 1 || dialer.addresses[0] != "93.184.216.34:443" {
		t.Fatalf("dialed %#v, want only public IP 93.184.216.34:443", dialer.addresses)
	}
}

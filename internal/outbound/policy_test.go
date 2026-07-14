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

package outbound

import (
	"net/http"
	"net/url"
	"testing"
)

func TestGlobalProxySetAndResolve(t *testing.T) {
	g := NewGlobalProxy()
	// Default: direct.
	req, _ := http.NewRequest(http.MethodGet, "https://api.example.com/v1", nil)
	u, err := g.ForRequest(req, nil)
	if err != nil || u != nil {
		t.Fatalf("default proxy = %v err=%v, want direct", u, err)
	}
	// Invalid schemes are rejected.
	for _, raw := range []string{"ftp://proxy:8080", "socks5://proxy:1080", "http://", "not a url"} {
		if err := g.Set(raw, nil); err == nil {
			t.Fatalf("Set(%q) accepted, want error", raw)
		}
	}
	// A valid global proxy applies.
	if err := g.Set("http://proxy.example:3128", nil); err != nil {
		t.Fatal(err)
	}
	u, err = g.ForRequest(req, nil)
	if err != nil || u == nil || u.Host != "proxy.example:3128" {
		t.Fatalf("global proxy = %v err=%v", u, err)
	}
	// Per-request override wins.
	override := WithChannelProxy(req.Context(), "http://channel-proxy:8080")
	overrideReq := req.WithContext(override)
	u, err = g.ForRequest(overrideReq, nil)
	if err != nil || u == nil || u.Host != "channel-proxy:8080" {
		t.Fatalf("override proxy = %v err=%v", u, err)
	}
	// Invalid override is rejected (never silently routed).
	bad := WithChannelProxy(req.Context(), "socks5://bad:1080")
	_, err = g.ForRequest(req.WithContext(bad), nil)
	if err == nil {
		t.Fatal("invalid override accepted")
	}
	// Empty override falls back to the global proxy.
	empty := WithChannelProxy(req.Context(), "  ")
	u, err = g.ForRequest(req.WithContext(empty), nil)
	if err != nil || u == nil || u.Host != "proxy.example:3128" {
		t.Fatalf("empty override fallback = %v err=%v", u, err)
	}
	// Clearing restores direct.
	if err := g.Set("", nil); err != nil {
		t.Fatal(err)
	}
	u, err = g.ForRequest(req, nil)
	if err != nil || u != nil {
		t.Fatalf("cleared proxy = %v err=%v", u, err)
	}
}

func TestSetClientProxyWiring(t *testing.T) {
	policy, err := NewPolicy(Options{AllowCIDRs: []string{"127.0.0.0/8"}})
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(policy, ClientOptions{})
	// No hook wired → direct.
	req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:1/x", nil)
	transport, ok := client.Transport.(validatingTransport)
	if !ok {
		t.Fatalf("unexpected transport %T", client.Transport)
	}
	inner := transport.next.(*http.Transport)
	if inner.Proxy != nil {
		t.Fatal("proxy should be nil by default")
	}
	// Wire a hook that always returns a fixed proxy.
	hook := func(*http.Request) (*url.URL, error) {
		return url.Parse("http://127.0.0.1:9999")
	}
	if !SetClientProxy(client, hook) {
		t.Fatal("SetClientProxy returned false")
	}
	if inner.Proxy == nil {
		t.Fatal("proxy hook not wired")
	}
	got, err := inner.Proxy(req)
	if err != nil || got.Host != "127.0.0.1:9999" {
		t.Fatalf("hook result = %v err=%v", got, err)
	}
}

// Package outbound provides the shared security boundary for upstream HTTP.
package outbound

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const maxRedirects = 5

var ErrBlocked = errors.New("outbound destination blocked")

type Resolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

type Policy struct {
	hosts    map[string]struct{}
	prefixes []netip.Prefix
	resolver Resolver
	dialer   Dialer
}

type Options struct {
	AllowHosts  []string
	AllowCIDRs  []string
	Resolver    Resolver
	Dialer      Dialer
	DialTimeout time.Duration
}

// Dialer abstracts the socket dialer so tests can record dial targets (the
// policy dials validated IPs, never hostnames).
type Dialer interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

func NewPolicy(opts Options) (*Policy, error) {
	hosts := make(map[string]struct{}, len(opts.AllowHosts))
	for _, value := range opts.AllowHosts {
		host := normalizeHost(value)
		if host == "" || net.ParseIP(host) != nil || strings.ContainsAny(host, "/:@") {
			return nil, fmt.Errorf("outbound: invalid allow host")
		}
		hosts[host] = struct{}{}
	}
	prefixes := make([]netip.Prefix, 0, len(opts.AllowCIDRs))
	for _, value := range opts.AllowCIDRs {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(value))
		if err != nil {
			return nil, fmt.Errorf("outbound: invalid allow CIDR")
		}
		prefixes = append(prefixes, prefix.Masked())
	}
	resolver := opts.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	dialer := opts.Dialer
	if dialer == nil {
		timeout := opts.DialTimeout
		if timeout <= 0 {
			timeout = 10 * time.Second
		}
		dialer = &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
	}
	policy := &Policy{hosts: hosts, prefixes: prefixes, resolver: resolver, dialer: dialer}
	return policy, nil
}
func (p *Policy) ValidateURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !u.IsAbs() || u.Host == "" || u.User != nil {
		return ErrBlocked
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return ErrBlocked
	}
	if u.Hostname() == "" {
		return ErrBlocked
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return ErrBlocked
		}
	}
	return nil
}

// ValidateDestination validates a URL that will be resolved by an explicit
// HTTP(S) proxy.  In direct mode DialContext resolves and pins the destination
// IP itself.  A proxy, however, receives the hostname and performs its own
// DNS lookup, so the gateway must validate the final target before handing the
// request to http.Transport.  Explicitly allowlisted hostnames retain their
// configured semantics and are permitted even when they resolve to private
// addresses; all other hostnames must resolve exclusively to allowed
// addresses, since a proxy could choose any answer returned by DNS.
func (p *Policy) ValidateDestination(ctx context.Context, u *url.URL) error {
	if u == nil || p == nil || p.ValidateURL(u.String()) != nil {
		return ErrBlocked
	}
	host := normalizeHost(u.Hostname())
	if p.hostAllowed(host) {
		return nil
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		if p.addressAllowed(addr.Unmap()) {
			return nil
		}
		return ErrBlocked
	}
	addresses, err := p.resolve(ctx, host)
	if err != nil || len(addresses) == 0 {
		return ErrBlocked
	}
	for _, addr := range addresses {
		if !p.addressAllowed(addr.Unmap()) {
			return ErrBlocked
		}
	}
	return nil
}

func (p *Policy) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil || host == "" || port == "" {
		return nil, ErrBlocked
	}
	normalized := normalizeHost(host)
	allowedHost := p.hostAllowed(normalized)
	addresses, err := p.resolve(ctx, normalized)
	if err != nil {
		return nil, fmt.Errorf("outbound resolve: %w", ErrBlocked)
	}
	var lastErr error
	for _, addr := range addresses {
		addr = addr.Unmap()
		if !allowedHost && !p.addressAllowed(addr) {
			continue
		}
		conn, dialErr := p.dialer.DialContext(ctx, network, net.JoinHostPort(addr.String(), port))
		if dialErr == nil {
			return conn, nil
		}
		lastErr = dialErr
	}
	if lastErr != nil {
		return nil, fmt.Errorf("outbound dial: %w", lastErr)
	}
	return nil, ErrBlocked
}

func (p *Policy) resolve(ctx context.Context, host string) ([]netip.Addr, error) {
	if addr, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{addr}, nil
	}
	return p.resolver.LookupNetIP(ctx, "ip", host)
}

func (p *Policy) hostAllowed(host string) bool {
	_, ok := p.hosts[host]
	return ok
}

func (p *Policy) addressAllowed(addr netip.Addr) bool {
	if !addr.IsValid() {
		return false
	}
	for _, prefix := range p.prefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return !isSpecial(addr)
}

func normalizeHost(value string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".")
}

func isSpecial(addr netip.Addr) bool {
	addr = addr.Unmap()
	if addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() || addr.IsMulticast() || addr.IsUnspecified() {
		return true
	}
	for _, prefix := range specialPrefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

var specialPrefixes = mustPrefixes(
	"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24",
	"198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4",
	// NAT64 well-known prefix (RFC 6052): encodes IPv4 inside IPv6 and would
	// otherwise smuggle private/loopback/metadata addresses past Unmap().
	"64:ff9b::/96", "64:ff9b:1::/48",
	// IPv4-compatible (deprecated) and ORCHIDv2 (RFC 7343) are not routable
	// public destinations; deny them outright.
	"::/96", "2001:20::/28",
	"100::/64", "2001:2::/48", "2001:10::/28",
	"2001:db8::/32",
)

func mustPrefixes(values ...string) []netip.Prefix {
	result := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		result = append(result, netip.MustParsePrefix(value))
	}
	return result
}

type ClientOptions struct {
	ResponseHeaderTimeout time.Duration
	TLSHandshakeTimeout   time.Duration
	IdleConnTimeout       time.Duration
	// MaxIdleConns is the total outbound idle connection ceiling.
	// 0 uses DefaultMaxIdleConns.
	MaxIdleConns int
	// MaxIdleConnsPerHost is the per-upstream-host idle connection ceiling.
	// 0 uses DefaultMaxIdleConnsPerHost.
	MaxIdleConnsPerHost int
	// Proxy resolves the outbound proxy for a request (per-request channel
	// override first, then the global proxy, then direct). nil = direct only
	// (the historical P7 behavior). The transport validates both the returned
	// proxy URL and the final request destination before handing the request to
	// the proxy; the proxy socket is dialed through DialContext as well.
	Proxy func(*http.Request) (*url.URL, error)
}

// DefaultMaxIdleConns is the total outbound idle connection ceiling.
const DefaultMaxIdleConns = 512

// DefaultMaxIdleConnsPerHost is the per-upstream-host idle connection ceiling.
// The Go zero value is 2, which starves multi-channel gateways that fan out to
// the same upstream host under concurrency.
const DefaultMaxIdleConnsPerHost = 64

func NewClient(policy *Policy, opts ClientOptions) *http.Client {
	if policy == nil {
		panic("outbound: nil policy")
	}
	tlsTimeout := opts.TLSHandshakeTimeout
	if tlsTimeout <= 0 {
		tlsTimeout = 10 * time.Second
	}
	idleTimeout := opts.IdleConnTimeout
	if idleTimeout <= 0 {
		idleTimeout = 90 * time.Second
	}
	maxIdleConns := opts.MaxIdleConns
	if maxIdleConns <= 0 {
		maxIdleConns = DefaultMaxIdleConns
	}
	maxIdleConnsPerHost := opts.MaxIdleConnsPerHost
	if maxIdleConnsPerHost <= 0 {
		maxIdleConnsPerHost = DefaultMaxIdleConnsPerHost
	}
	transport := &http.Transport{
		// Environment proxies are intentionally disabled (P7 contract): proxy-
		// side DNS resolution would bypass DialContext's address validation and
		// re-open the SSRF surface. An explicit configured proxy (opts.Proxy) is
		// still honored: validatingTransport checks its URL and the final target,
		// while the proxy socket itself is dialed through DialContext.
		Proxy:                 wrapProxyHook(opts.Proxy),
		DialContext:           policy.DialContext,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   tlsTimeout,
		ResponseHeaderTimeout: opts.ResponseHeaderTimeout,
		IdleConnTimeout:       idleTimeout,
		MaxIdleConns:          maxIdleConns,
		MaxIdleConnsPerHost:   maxIdleConnsPerHost,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	}
	client := &http.Client{Transport: validatingTransport{policy: policy, next: transport, proxy: transport.Proxy}}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxRedirects {
			return errors.New("outbound redirect limit exceeded")
		}
		if err := policy.ValidateURL(req.URL.String()); err != nil {
			return ErrBlocked
		}
		if len(via) > 0 && !sameOrigin(via[len(via)-1].URL, req.URL) {
			req.Header.Del("Authorization")
			req.Header.Del("Cookie")
			req.Header.Del("Proxy-Authorization")
		}
		return nil
	}
	return client
}

type validatingTransport struct {
	policy *Policy
	next   http.RoundTripper
	proxy  func(*http.Request) (*url.URL, error)
}

func (t validatingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil || req.URL == nil || t.policy.ValidateURL(req.URL.String()) != nil {
		return nil, ErrBlocked
	}
	proxyHook := t.proxy
	if transport, ok := t.next.(*http.Transport); ok {
		// SetClientProxy may replace the hook after client construction, so read
		// the live transport field instead of relying only on the snapshot.
		proxyHook = transport.Proxy
	}
	if proxyHook != nil {
		proxyURL, err := proxyHook(req)
		if err != nil {
			return nil, err
		}
		if proxyURL != nil {
			if err := t.policy.ValidateDestination(req.Context(), req.URL); err != nil {
				return nil, err
			}
			if err := t.policy.ValidateDestination(req.Context(), proxyURL); err != nil {
				return nil, err
			}
			// The wrapped transport.Proxy hook recognizes this context value and
			// returns the already-validated URL without invoking user code again.
			copyURL := *proxyURL
			ctx := context.WithValue(req.Context(), proxyURLContextKey{}, &copyURL)
			req = req.Clone(ctx)
		}
	}
	return t.next.RoundTrip(req)
}

// proxyURLContextKey carries the proxy URL selected during validation into
// http.Transport, avoiding a second call to a dynamic per-request hook.
type proxyURLContextKey struct{}

func wrapProxyHook(hook func(*http.Request) (*url.URL, error)) func(*http.Request) (*url.URL, error) {
	if hook == nil {
		return nil
	}
	return func(req *http.Request) (*url.URL, error) {
		if req != nil {
			if proxyURL, ok := req.Context().Value(proxyURLContextKey{}).(*url.URL); ok {
				return proxyURL, nil
			}
		}
		return hook(req)
	}
}

func sameOrigin(a, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) &&
		strings.EqualFold(a.Hostname(), b.Hostname()) && effectivePort(a) == effectivePort(b)
}

func effectivePort(u *url.URL) string {
	if u.Port() != "" {
		return u.Port()
	}
	if u.Scheme == "https" {
		return "443"
	}
	return "80"
}

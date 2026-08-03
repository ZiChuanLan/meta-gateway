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
	hosts     map[string]struct{}
	prefixes  []netip.Prefix
	resolver  Resolver
	dialer    *net.Dialer
	proxyHost string
	proxyPort string
}

type Options struct {
	AllowHosts  []string
	AllowCIDRs  []string
	Resolver    Resolver
	Dialer      *net.Dialer
	DialTimeout time.Duration
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
	// Remember the environment proxy address so DialContext can exempt the
	// proxy hop itself (commonly private: host.docker.internal, 127.0.0.1, ...)
	// from SSRF checks.
	if proxyURL, err := http.ProxyFromEnvironment(&http.Request{URL: &url.URL{Scheme: "https", Host: "example.com"}}); err == nil && proxyURL != nil {
		if host, port, splitErr := net.SplitHostPort(proxyURL.Host); splitErr == nil {
			policy.proxyHost = host
			policy.proxyPort = port
		}
	}
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

func (p *Policy) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil || host == "" || port == "" {
		return nil, ErrBlocked
	}
	// The proxy hop itself is exempt from SSRF validation: when an outbound
	// proxy is configured, Go dials the proxy address through DialContext, and
	// that address is commonly private (host.docker.internal, 127.0.0.1, ...).
	if p.proxyHost != "" && strings.EqualFold(host, p.proxyHost) && port == p.proxyPort {
		return p.dialer.DialContext(ctx, network, address)
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
	"64:ff9b:1::/48", "100::/64", "2001:2::/48", "2001:10::/28",
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
}

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
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           policy.DialContext,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   tlsTimeout,
		ResponseHeaderTimeout: opts.ResponseHeaderTimeout,
		IdleConnTimeout:       idleTimeout,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	}
	client := &http.Client{Transport: validatingTransport{policy: policy, next: transport}}
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
}

func (t validatingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil || req.URL == nil || t.policy.ValidateURL(req.URL.String()) != nil {
		return nil, ErrBlocked
	}
	return t.next.RoundTrip(req)
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

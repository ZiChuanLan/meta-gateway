package outbound

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
)

// errProxyScheme/errProxyHost guard the proxy URL shape (http/https only,
// host required) before it is ever handed to the transport.
var (
	errProxyScheme = fmt.Errorf("outbound: proxy URL must be http or https")
	errProxyHost   = fmt.Errorf("outbound: proxy URL must include a host")
)

// channelProxyKey carries a per-request proxy override (a channel's
// proxy_url) through the request context. The transport's proxy hook reads
// it first; a missing/empty value falls back to the global proxy.
type channelProxyKey struct{}

// SetClientProxy wires (or replaces) the proxy hook on an existing outbound
// client, unwrapping the validating transport. A nil hook restores direct
// connections. Returns false when the transport chain is unrecognized.
func SetClientProxy(client *http.Client, hook func(*http.Request) (*url.URL, error)) bool {
	transport, ok := client.Transport.(validatingTransport)
	if !ok {
		if plain, ok := client.Transport.(*http.Transport); ok {
			plain.Proxy = hook
			return true
		}
		return false
	}
	if inner, ok := transport.next.(*http.Transport); ok {
		inner.Proxy = hook
		return true
	}
	return false
}

// WithChannelProxy returns a context whose upstream requests route through
// the given proxy URL. An empty raw value is a no-op (global fallback).
func WithChannelProxy(ctx context.Context, raw string) context.Context {
	if strings.TrimSpace(raw) == "" {
		return ctx
	}
	return context.WithValue(ctx, channelProxyKey{}, strings.TrimSpace(raw))
}

// GlobalProxy holds the hot-swappable global outbound proxy ("" = direct).
// It is shared by every outbound client transport so a settings change is
// live without restarting connections.
type GlobalProxy struct {
	value atomic.Value // *url.URL or nil
}

// NewGlobalProxy starts with no proxy (direct connections).
func NewGlobalProxy() *GlobalProxy {
	g := &GlobalProxy{}
	g.value.Store((*url.URL)(nil))
	return g
}

// Set updates the global proxy. An empty raw clears it. The URL must be an
// http/https absolute URL; validation reuses the SSRF policy so a proxy can
// never point at a host outside the allowed network.
func (g *GlobalProxy) Set(raw string, policy *Policy) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		g.value.Store((*url.URL)(nil))
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errProxyScheme
	}
	if u.Host == "" {
		return errProxyHost
	}
	if policy != nil {
		if err := policy.ValidateURL(raw); err != nil {
			return err
		}
	}
	g.value.Store(u)
	return nil
}

// ForRequest resolves the effective proxy for one request: a per-request
// channel override wins, then the global proxy, then direct. Both values are
// re-validated against the policy on every call so a revoked network can
// never silently route through a stale proxy.
func (g *GlobalProxy) ForRequest(req *http.Request, policy *Policy) (*url.URL, error) {
	if override, ok := req.Context().Value(channelProxyKey{}).(string); ok && strings.TrimSpace(override) != "" {
		return parseAndValidate(override, policy)
	}
	current, _ := g.value.Load().(*url.URL)
	if current == nil {
		return nil, nil
	}
	return parseAndValidate(current.String(), policy)
}

func parseAndValidate(raw string, policy *Policy) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errProxyScheme
	}
	if u.Host == "" {
		return nil, errProxyHost
	}
	if policy != nil {
		if err := policy.ValidateURL(raw); err != nil {
			return nil, err
		}
	}
	return u, nil
}

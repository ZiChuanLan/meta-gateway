package httpapi

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"

	"github.com/lan/meta-gateway/internal/auth"
)

type clientIPKey struct{}

type clientIPResolver struct {
	trusted []netip.Prefix
	logger  *slog.Logger
	// warned keeps the ignored-forwarding notice to one line per process: it
	// describes a deployment fact, not a per-request event.
	warned sync.Once
}

func newClientIPResolver(values []string, logger *slog.Logger) (*clientIPResolver, error) {
	result := &clientIPResolver{logger: logger}
	for _, value := range values {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return nil, err
		}
		result.trusted = append(result.trusted, prefix.Masked())
	}
	return result, nil
}

func (c *clientIPResolver) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peer := remoteIP(r.RemoteAddr)
		ip := peer
		if c.contains(peer) {
			chain := forwardedChain(r.Header.Get("X-Forwarded-For"))
			for i := len(chain) - 1; i >= 0; i-- {
				if !c.contains(chain[i]) {
					ip = chain[i]
					break
				}
			}
		}
		c.warnIgnoredForwarding(r, peer, ip)
		ctx := context.WithValue(r.Context(), clientIPKey{}, ip)
		ctx = auth.WithClientIP(ctx, ip.String())
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// warnIgnoredForwarding reports the deployment mistake that hides behind a proxy
// nobody trusts: forwarding headers arrive, they are ignored on purpose, and the
// client turns out to be the proxy — so every caller shares one rate-limit bucket
// and one audit identity. The header value is never logged (a caller controls it);
// the peer is what the operator has to add to TRUSTED_PROXY_CIDRS.
func (c *clientIPResolver) warnIgnoredForwarding(r *http.Request, peer, resolved netip.Addr) {
	if c.logger == nil || resolved != peer {
		return
	}
	header := ""
	for _, name := range []string{"X-Forwarded-For", "X-Real-IP", "Forwarded"} {
		if strings.TrimSpace(r.Header.Get(name)) != "" {
			header = name
			break
		}
	}
	if header == "" {
		return
	}
	c.warned.Do(func() {
		c.logger.Warn("forwarding header ignored: every client behind this proxy shares one identity",
			"peer", peer.String(), "header", header, "trusted_proxy_cidrs", len(c.trusted))
	})
}

func ClientIP(r *http.Request) netip.Addr {
	if value, ok := r.Context().Value(clientIPKey{}).(netip.Addr); ok {
		return value
	}
	return remoteIP(r.RemoteAddr)
}

func (c *clientIPResolver) contains(addr netip.Addr) bool {
	addr = addr.Unmap()
	for _, prefix := range c.trusted {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

func remoteIP(value string) netip.Addr {
	host, _, err := net.SplitHostPort(strings.TrimSpace(value))
	if err != nil {
		host = strings.TrimSpace(value)
	}
	addr, _ := netip.ParseAddr(strings.Trim(host, "[]"))
	return addr.Unmap()
}

func forwardedChain(value string) []netip.Addr {
	var result []netip.Addr
	for _, part := range strings.Split(value, ",") {
		addr, err := netip.ParseAddr(strings.TrimSpace(part))
		if err != nil {
			return nil
		}
		result = append(result, addr.Unmap())
	}
	return result
}

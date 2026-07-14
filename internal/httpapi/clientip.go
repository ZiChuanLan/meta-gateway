package httpapi

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

type clientIPKey struct{}

type clientIPResolver struct {
	trusted []netip.Prefix
}

func newClientIPResolver(values []string) (*clientIPResolver, error) {
	result := &clientIPResolver{}
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
		ip := remoteIP(r.RemoteAddr)
		if c.contains(ip) {
			chain := forwardedChain(r.Header.Get("X-Forwarded-For"))
			for i := len(chain) - 1; i >= 0; i-- {
				if !c.contains(chain[i]) {
					ip = chain[i]
					break
				}
			}
		}
		ctx := context.WithValue(r.Context(), clientIPKey{}, ip)
		next.ServeHTTP(w, r.WithContext(ctx))
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

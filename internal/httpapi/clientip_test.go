package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientIPIgnoresUntrustedForwardingHeader(t *testing.T) {
	resolver, _ := newClientIPResolver([]string{"10.0.0.0/8"})
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "198.51.100.10:1234"
	request.Header.Set("X-Forwarded-For", "203.0.113.7")
	resolver.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if got := ClientIP(r).String(); got != "198.51.100.10" {
			t.Fatalf("client IP=%s", got)
		}
	})).ServeHTTP(httptest.NewRecorder(), request)
}

func TestClientIPWalksTrustedProxyChain(t *testing.T) {
	resolver, _ := newClientIPResolver([]string{"10.0.0.0/8"})
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "10.0.0.2:1234"
	request.Header.Set("X-Forwarded-For", "203.0.113.7, 10.0.0.1")
	resolver.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if got := ClientIP(r).String(); got != "203.0.113.7" {
			t.Fatalf("client IP=%s", got)
		}
	})).ServeHTTP(httptest.NewRecorder(), request)
}

func TestClientIPFallsBackOnMalformedChain(t *testing.T) {
	resolver, _ := newClientIPResolver([]string{"10.0.0.0/8"})
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "10.0.0.2:1234"
	request.Header.Set("X-Forwarded-For", "not-an-ip")
	resolver.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if got := ClientIP(r).String(); got != "10.0.0.2" {
			t.Fatalf("client IP=%s", got)
		}
	})).ServeHTTP(httptest.NewRecorder(), request)
}

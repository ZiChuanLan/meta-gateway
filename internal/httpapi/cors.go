package httpapi

import (
	"net/http"
	"strings"
)

// corsExposedHeaders lists the response headers a browser caller may read.
// None of them are CORS-safelisted, so without this a JS client cannot see the
// rate-limit backoff, the request id it needs for a support ticket, the
// image-shim marker, or the plugin decision that rewrote its model.
const corsExposedHeaders = "retry-after, x-request-id, x-meta-image-shim, x-meta-hook-decision"

// corsFallbackAllowHeaders answers a preflight that did not state which request
// headers it intends to send.
const corsFallbackAllowHeaders = "authorization, content-type, accept, x-api-key, anthropic-version"

const corsAllowMethods = "GET, POST, PUT, PATCH, DELETE, OPTIONS"

// corsMaxAge caches the preflight response for a day. The downstream surface is
// chatty and a preflight in front of every call doubles the round trips.
const corsMaxAge = "86400"

// corsMiddleware opens one path prefix to browser callers.
//
// It has to run before any authentication middleware: a preflight carries no
// credentials by design, so answering it after auth would return 401 and the
// browser would block the real request with an opaque CORS error.
//
// An empty allowlist means "any origin" (Access-Control-Allow-Origin: *) and is
// the zero-config default. Naming origins switches to echo mode, which also
// emits Vary: Origin so a shared cache cannot hand one origin's grant to
// another. Entries are exact origins, or "*.example.com" to cover subdomains.
func corsMiddleware(prefix string, allowed []string) func(http.Handler) http.Handler {
	allowAll := len(allowed) == 0
	exact := make(map[string]struct{}, len(allowed))
	var suffixes []string
	for _, raw := range allowed {
		origin := strings.TrimSpace(raw)
		if origin == "" {
			continue
		}
		if origin == "*" {
			allowAll = true
			continue
		}
		if rest, ok := strings.CutPrefix(origin, "*."); ok {
			suffixes = append(suffixes, "."+strings.ToLower(rest))
			continue
		}
		exact[strings.ToLower(origin)] = struct{}{}
	}

	permitted := func(origin string) bool {
		if allowAll {
			return true
		}
		lower := strings.ToLower(origin)
		if _, ok := exact[lower]; ok {
			return true
		}
		for _, suffix := range suffixes {
			if strings.HasSuffix(lower, suffix) {
				return true
			}
		}
		return false
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Anything outside the configured surface is none of our business.
			if !strings.HasPrefix(r.URL.Path, prefix) {
				next.ServeHTTP(w, r)
				return
			}
			// A caller with no Origin is not a browser; CORS does not apply.
			origin := strings.TrimSpace(r.Header.Get("Origin"))
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}
			if !permitted(origin) {
				// Answer the preflight anyway so the browser reports the denial
				// against this origin instead of surfacing a bare 404/405.
				if isPreflight(r) {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				next.ServeHTTP(w, r)
				return
			}
			if allowAll {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			} else {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Add("Vary", "Origin")
			}
			w.Header().Set("Access-Control-Expose-Headers", corsExposedHeaders)
			if isPreflight(r) {
				w.Header().Set("Access-Control-Allow-Methods", corsAllowMethods)
				// Echo the requested headers rather than pinning a list: the
				// gateway speaks both the OpenAI and Anthropic wire formats, and
				// their clients disagree over which of anthropic-version /
				// x-api-key / openai-beta they send.
				requested := strings.TrimSpace(r.Header.Get("Access-Control-Request-Headers"))
				if requested == "" {
					requested = corsFallbackAllowHeaders
				}
				w.Header().Set("Access-Control-Allow-Headers", requested)
				w.Header().Set("Access-Control-Max-Age", corsMaxAge)
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// isPreflight reports whether r is a CORS preflight. A bare OPTIONS is not one,
// and must keep flowing to the router so it can answer with 405 as usual.
func isPreflight(r *http.Request) bool {
	return r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != ""
}

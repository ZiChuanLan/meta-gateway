package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/lan/meta-gateway/internal/auth"
	"github.com/lan/meta-gateway/internal/observability"
	"github.com/lan/meta-gateway/internal/ratelimit"
)

// Global login ceilings are flood breaks, not per-source throttles: they must sit
// far above what a single source can spend, because a ceiling one unauthenticated
// client can drain turns a shared bucket into a deployment-wide login lockout. The
// per-source control is the per-IP limiter next to each of these.
const (
	loginGlobalRatePerMinute = 600
	loginGlobalBurst         = 60
)

// Rejected admin probes are audited, and an unauthenticated client is what drives
// them, so the audit writer keeps its own per-client budget (see auditAdmin).
const (
	auditFailureRatePerMinute = 30
	auditFailureBurst         = 10
)

func rateLimitMiddleware(limiter *ratelimit.Limiter, key func(*http.Request) int64, scope string, metrics *observability.Registry) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ok, wait := limiter.Allow(key(r))
			if !ok {
				metrics.RateLimited(scope)
				seconds := int64((wait + time.Second - 1) / time.Second)
				w.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
				writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func downstreamRateKey(r *http.Request) int64 {
	id, _ := auth.DownstreamKeyID(r)
	return id
}

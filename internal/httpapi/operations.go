package httpapi

import (
	"context"
	"crypto/subtle"
	"log/slog"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/lan/meta-gateway/internal/observability"
	"github.com/lan/meta-gateway/internal/store"
)

func requestTelemetry(logger *slog.Logger, metrics *observability.Registry) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			wrapped := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
			started := time.Now()
			next.ServeHTTP(wrapped, r)
			route := chi.RouteContext(r.Context()).RoutePattern()
			if route == "" {
				route = "unmatched"
			}
			status := wrapped.Status()
			if status == 0 {
				status = http.StatusOK
			}
			elapsed := time.Since(started)
			metrics.ObserveHTTP(r.Method, route, status, elapsed)
			logger.InfoContext(r.Context(), "http request",
				"request_id", chimw.GetReqID(r.Context()), "method", r.Method, "route", route,
				"status", status, "duration_ms", elapsed.Milliseconds(), "client_ip", ClientIP(r).String())
		})
	}
}

func recoverMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if recover() != nil {
					logger.ErrorContext(r.Context(), "request panic", "category", "panic", "request_id", chimw.GetReqID(r.Context()))
					writeError(w, http.StatusInternalServerError, "internal server error")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

func auditAdmin(logger *slog.Logger, events *store.AuditEventStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			wrapped := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(wrapped, r)
			status := wrapped.Status()
			if status == 0 {
				status = http.StatusOK
			}
			if r.Method == http.MethodGet && status != http.StatusUnauthorized && status != http.StatusForbidden {
				return
			}
			route := chi.RouteContext(r.Context()).RoutePattern()
			action := auditAction(r.Method, route, status)
			resourceKind, resourceID := auditResource(r)
			outcome := "success"
			category := ""
			if status >= 400 {
				outcome = "failure"
				category = auditCategory(status)
			}
			event := &store.AuditEvent{RequestID: chimw.GetReqID(r.Context()), ActorKind: "admin", Action: action,
				ResourceKind: resourceKind, ResourceID: resourceID, Outcome: outcome, StatusCode: status, Category: category}
			if err := events.Insert(event); err != nil {
				logger.ErrorContext(r.Context(), "audit write failed", "category", "persistence")
			}
		})
	}
}

func auditAction(method, route string, status int) string {
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return "admin.auth"
	}
	if route == "" {
		return "admin.request"
	}
	verb := map[string]string{http.MethodPost: "create", http.MethodPut: "update", http.MethodPatch: "update", http.MethodDelete: "delete"}[method]
	if verb == "" {
		verb = "request"
	}
	return "admin." + auditResourceKind(route) + "." + verb
}

func auditResource(r *http.Request) (string, *int64) {
	route := chi.RouteContext(r.Context()).RoutePattern()
	kind := auditResourceKind(route)
	for _, name := range []string{"id", "credentialId", "channelId", "routeId", "siteId"} {
		if raw := chi.URLParam(r, name); raw != "" {
			if id, err := strconv.ParseInt(raw, 10, 64); err == nil && id > 0 {
				return kind, &id
			}
		}
	}
	return kind, nil
}

func auditResourceKind(route string) string {
	for _, item := range []struct{ fragment, kind string }{
		{"route-members", "route_member"}, {"downstream-keys", "downstream_key"}, {"audit-events", "audit_event"},
		{"credentials", "credential"}, {"discovery", "discovery"}, {"checkin", "checkin"}, {"exchange", "exchange"},
		{"backups", "backup"}, {"channels", "channel"}, {"routes", "route"}, {"sites", "site"},
	} {
		if strings.Contains(route, item.fragment) {
			return item.kind
		}
	}
	return "admin"
}

func auditCategory(status int) string {
	switch status {
	case 400:
		return "validation"
	case 401:
		return "unauthorized"
	case 403:
		return "forbidden"
	case 404:
		return "not_found"
	case 409:
		return "conflict"
	case 413:
		return "body_too_large"
	case 422:
		return "unavailable"
	case 429:
		return "rate_limited"
	default:
		if status >= 500 {
			return "internal"
		}
		return "rejected"
	}
}

func metricsAuthorized(token string, trusted []netip.Prefix, r *http.Request) bool {
	for _, prefix := range trusted {
		if prefix.Contains(ClientIP(r).Unmap()) {
			return true
		}
	}
	const prefix = "Bearer "
	header := r.Header.Get("Authorization")
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return false
	}
	presented := header[len(prefix):]
	return token != "" && len(presented) == len(token) && subtle.ConstantTimeCompare([]byte(presented), []byte(token)) == 1
}

func parsePrefixes(values []string) []netip.Prefix {
	result := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		if prefix, err := netip.ParsePrefix(value); err == nil {
			result = append(result, prefix.Masked())
		}
	}
	return result
}

func pingReady(db *store.DB, timeout time.Duration) bool {
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return db.PingContext(ctx) == nil
}

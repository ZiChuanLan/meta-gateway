package adapters

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// retryAfterFromHeader parses an upstream Retry-After response header (whole
// seconds or an HTTP-date) into a duration. Returns 0 when the header is
// absent, unparseable, or already expired. Semantics mirror
// proxy.retryAfterCooldown; 0 means "unknown" so callers fall back to their
// default pause.
func retryAfterFromHeader(header http.Header) time.Duration {
	if header == nil {
		return 0
	}
	value := strings.TrimSpace(header.Get("Retry-After"))
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil {
		if d := time.Until(when); d > 0 {
			return d
		}
	}
	return 0
}

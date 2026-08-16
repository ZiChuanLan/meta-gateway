// Package proxy orchestrates routing, retries, upstream relay, and attempt logs.
package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lan/meta-gateway/internal/adapters"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/relay"
)

// upstreamRequestID reads the upstream x-request-id header for the attempt.
// A nil result header (adapter-local failures) yields an empty string.
func upstreamRequestID(result *relay.Result) string {
	if result == nil || result.Header == nil {
		return ""
	}
	return strings.TrimSpace(result.Header.Get("X-Request-Id"))
}

// classifyForChannel is classify plus the channel's retry_config: custom
// status codes and error-text patterns can make a non-default failure
// retryable (or keep a default one retryable).
func adapterErrorStatus(err error, fallback int) int {
	switch {
	case errors.Is(err, adapters.ErrUnsupportedPath), errors.Is(err, adapters.ErrUnsupportedFeature):
		return http.StatusNotImplemented
	case errors.Is(err, adapters.ErrContentBlocked):
		return http.StatusBadRequest
	default:
		return fallback
	}
}

func adapterErrorCategory(err error) string {
	switch {
	case errors.Is(err, adapters.ErrInvalidURL):
		return "invalid_url"
	case errors.Is(err, adapters.ErrUnsupportedPath):
		return "unsupported_path"
	case errors.Is(err, adapters.ErrUnsupportedFeature):
		return "unsupported_feature"
	case errors.Is(err, adapters.ErrContentBlocked):
		return "content_blocked"
	default:
		return "adapter_request"
	}
}

func isAdapterError(err error) bool {
	return errors.Is(err, adapters.ErrInvalidURL) ||
		errors.Is(err, adapters.ErrUnsupportedPath) ||
		errors.Is(err, adapters.ErrUnsupportedFeature) ||
		errors.Is(err, adapters.ErrContentBlocked)
}

func classifyForChannel(result *relay.Result, cfg domain.RetryConfig) (string, bool) {
	if result.Err != nil {
		if errors.Is(result.Err, context.Canceled) || errors.Is(result.Err, context.DeadlineExceeded) {
			return "cancelled", false
		}
		switch {
		case errors.Is(result.Err, adapters.ErrInvalidURL):
			return "invalid_url", false
		case errors.Is(result.Err, adapters.ErrUnsupportedPath):
			return "unsupported_path", false
		case errors.Is(result.Err, adapters.ErrUnsupportedFeature):
			return "unsupported_feature", false
		case errors.Is(result.Err, adapters.ErrContentBlocked):
			return "content_blocked", false
		}
		// Transport errors stay retryable; patterns may add text-matched cases.
		if isRetryableForChannel(0, result.Err.Error(), cfg) {
			return "transport", true
		}
		return "transport", true
	}
	category := fmt.Sprintf("upstream_status_%d", result.StatusCode)
	if isRetryableForChannel(result.StatusCode, upstreamErrorText(result), cfg) {
		return category, true
	}
	return category, false
}

// modelNotFoundText returns the error text used for model-not-found
// detection: the OpenAI-style error.code (e.g. model_not_found) joined with
// the message, so both structured codes and free-text messages match.
func modelNotFoundText(result *relay.Result) string {
	if result == nil || result.Body == nil {
		return ""
	}
	raw, err := io.ReadAll(io.LimitReader(result.Body, 64<<10))
	_ = result.Body.Close()
	result.Body = io.NopCloser(bytes.NewReader(raw))
	if err != nil {
		return ""
	}
	var payload struct {
		Error struct {
			Message string `json:"message"`
			Code    string `json:"code"`
			Type    string `json:"type"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return string(raw)
	}
	if payload.Error.Code != "" {
		return payload.Error.Code + " " + payload.Error.Message
	}
	if payload.Error.Message != "" {
		return payload.Error.Message
	}
	return payload.Message
}

// isModelNotFoundError reports whether the upstream said this model does not
// exist (OpenAI code/type identifiers plus common message shapes). Mirror of
// the identifiers used by CLIProxyAPI's isModelNotFoundIdentifier: codes like
// model_not_found / unknown_model / model_does_not_exist and messages like
// "no such model X" / "unknown model X".
func isModelNotFoundError(status int, text string) bool {
	if status < 400 || status > 499 {
		return false
	}
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	normalized := strings.NewReplacer("-", "_", " ", "_").Replace(lower)
	for _, marker := range []string{
		"model_not_found", "model_not_found_error", "unknown_model",
		"model_does_not_exist", "model_not_exist",
	} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	for _, prefix := range []string{"no such model", "unknown model"} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

// upstreamErrorText extracts the upstream error message from a non-2xx relay
// result body (OpenAI-style {error:{message}}), for error-pattern matching.
// The body is restored after reading so the client still receives it.
func upstreamErrorText(result *relay.Result) string {
	if result == nil || result.Body == nil || result.StatusCode >= 200 && result.StatusCode < 300 {
		return ""
	}
	raw, err := io.ReadAll(io.LimitReader(result.Body, 64<<10))
	// Close the live upstream body before swapping in the replay buffer so the
	// connection returns to the pool.
	_ = result.Body.Close()
	// Restore the body for downstream consumers.
	result.Body = io.NopCloser(bytes.NewReader(raw))
	if err != nil {
		return ""
	}
	var payload struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return string(raw)
	}
	if payload.Error.Message != "" {
		return payload.Error.Message
	}
	return payload.Message
}

func isRetryableStatus(status int) bool {
	// AxonHub default set: 429 (rate limit) and 5xx (transient upstream
	// failures) are retryable. Other 4xx are NOT retried by default — a bad
	// request (auth, missing model, malformed payload) will not heal by
	// failing over. Channels can opt into additional codes/patterns via
	// retry_config.
	switch status {
	case http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusInternalServerError,
		http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout,
		// Cloudflare origin-down / overload codes.
		528, 529, 530:
		return true
	default:
		return status >= 500 && status < 600
	}
}

// isRetryableForChannel adds the channel's retry_config on top of the
// global default set (mirrors AxonHub retry.go): custom status codes and
// error-message patterns (substring or regex). Regex patterns arrive
// pre-compiled from ParseRetryConfig; nil entries are skipped.
func isRetryableForChannel(status int, errMsg string, cfg domain.RetryConfig) bool {
	if isRetryableStatus(status) {
		return true
	}
	for _, code := range cfg.StatusCodes {
		if code == status {
			return true
		}
	}
	if errMsg == "" {
		return false
	}
	for i, pattern := range cfg.ErrorPatterns {
		if pattern.Pattern == "" {
			continue
		}
		if pattern.Regex {
			compiled := cfg.CompiledPatterns()
			if i < len(compiled) && compiled[i] != nil && compiled[i].MatchString(errMsg) {
				return true
			}
			continue
		}
		if strings.Contains(errMsg, pattern.Pattern) {
			return true
		}
	}
	return false
}

// retryAfterCooldown extends the base cool-down with the upstream's Retry-After
// header when present (whole seconds or an HTTP-date). It never shrinks the base.
func retryAfterCooldown(header http.Header, now time.Time, base time.Duration) time.Duration {
	if header == nil {
		return base
	}
	value := strings.TrimSpace(header.Get("Retry-After"))
	if value == "" {
		return base
	}
	var penalty time.Duration
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		penalty = time.Duration(seconds) * time.Second
	} else if when, err := http.ParseTime(value); err == nil {
		penalty = when.Sub(now)
	}
	if penalty > base {
		return penalty
	}
	return base
}

package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/outbound"
)

// Defaults for a route-free check. They mirror the probe package's: a real
// upstream call, but a deliberately tiny one.
const (
	DirectTestTimeout       = 30 * time.Second
	DirectTestDefaultTokens = 1
	DirectTestMaxTokens     = 256
	DirectTestPrompt        = "hi"
	// Drain cap for the upstream body. A completion is small, so reading it
	// whole keeps the pooled connection reusable; the 256-byte excerpt in the
	// error message is a display concern, not a transport one.
	directTestDrainLimit  = 64 << 10
	directTestDetailLimit = 256
)

// DirectTestResult is the outcome of one route-free upstream check.
type DirectTestResult struct {
	Model      string `json:"model"`
	OK         bool   `json:"ok"`
	StatusCode int    `json:"status_code"`
	LatencyMs  int    `json:"latency_ms"`
	Error      string `json:"error,omitempty"`
}

// DirectChatTest sends a minimal chat completion straight to one channel,
// bypassing route selection entirely.
//
// Why this exists next to ForwardWithMeta: route-based probing (the probe
// package) can only reach pairs that are already route members, because the
// selector resolves a request by looking up an enabled route that matches the
// model name. That leaves the connection console unable to answer the one
// question an operator asks while wiring a channel up — "does this advertised
// model actually answer?" — since the candidates are precisely the models that
// have no route yet, and adopting them blind just to be able to test them is
// the workflow this replaces.
//
// What it reuses from the real forwarding path: the same forward adapter, the
// same credential pool (with its per-key model allowances), the same upstream
// URL resolution, the same auth headers and header overrides, and the same
// per-channel outbound proxy. So a model that answers here is a model that
// answers through /v1, including the non-OpenAI channel families.
//
// What it deliberately does NOT do, and this is the whole point of keeping it
// a separate method rather than a flag on ForwardWithMeta: it never touches
// mutable state. No selector, no channel gate, no cooldown or consecutive
// failure bookkeeping, no model blacklist, no proxy log, no usage metering.
// A 404 from a model name that does not exist is information, not a fault, and
// an operator poking at a half-configured channel must not be able to take
// anything out of rotation.
func (s *Service) DirectChatTest(ctx context.Context, channelID int64, model, prompt string, maxTokens int) DirectTestResult {
	result := DirectTestResult{Model: strings.TrimSpace(model)}
	model = result.Model

	if channelID <= 0 {
		result.Error = "channel is required"
		return result
	}
	if model == "" {
		result.Error = "model is required"
		return result
	}
	if len([]byte(model)) > 256 {
		result.Error = strings.TrimPrefix(ErrModelTooLong.Error(), "proxy: ")
		return result
	}
	if s.db == nil {
		result.Error = strings.TrimPrefix(ErrCredential.Error(), "proxy: ")
		return result
	}
	channel, err := s.db.Channel.GetByID(channelID)
	if err != nil {
		result.Error = "channel lookup failed"
		return result
	}
	if channel == nil {
		result.Error = "channel not found"
		return result
	}
	// A manually disabled channel is not testable: pretending it is would
	// suggest the gateway would serve it.
	if channel.Status != domain.StatusEnabled && channel.Status != domain.StatusAutoDisabled {
		result.Error = "channel is disabled"
		return result
	}
	keys, err := s.resolveAPIKeyPool(*channel, model)
	if err != nil || len(keys) == 0 {
		result.Error = strings.TrimPrefix(ErrCredential.Error(), "proxy: ")
		return result
	}

	if prompt == "" {
		prompt = DirectTestPrompt
	}
	if maxTokens <= 0 {
		maxTokens = DirectTestDefaultTokens
	}
	if maxTokens > DirectTestMaxTokens {
		maxTokens = DirectTestMaxTokens
	}
	body, err := json.Marshal(map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"stream":     false,
		"max_tokens": maxTokens,
	})
	if err != nil {
		result.Error = "build request: " + err.Error()
		return result
	}

	adapter := s.resolveForward(*channel)
	upstreamPath, requestBody, translateErr := adapter.TransformRequest("chat/completions", body)
	if translateErr != nil {
		result.Error = fmt.Sprintf("%s: %v", adapter.Name(), translateErr)
		return result
	}
	upstreamURL, urlErr := s.resolveUpstreamURL(*channel, upstreamPath, adapter)
	if urlErr != nil {
		result.Error = strings.TrimPrefix(urlErr.Error(), "proxy: ")
		return result
	}

	// Exactly one key, no rotation and no 401 refresh replay: this is a single
	// synthetic check, and silently walking the pool would make the reported
	// latency and verdict ambiguous.
	headers := adapter.AuthHeaders(keys[0])
	if overrideErr := mergeHeaderOverrides(headers, channel.HeaderOverride); overrideErr != nil {
		result.Error = "header override: " + overrideErr.Error()
		return result
	}

	if ctx == nil {
		ctx = context.Background()
	}
	if proxyURL := strings.TrimSpace(channel.ProxyURL); proxyURL != "" {
		ctx = outbound.WithChannelProxy(ctx, proxyURL)
	}
	ctx, cancel := context.WithTimeout(ctx, DirectTestTimeout)
	defer cancel()

	started := time.Now()
	res := s.relay.ForwardWithHeaders(ctx, http.MethodPost, upstreamURL, headers, requestBody)
	latency := int(time.Since(started).Milliseconds())
	if res != nil && res.LatencyMs > 0 {
		latency = res.LatencyMs
	}
	if latency < 0 {
		latency = 0
	}
	result.LatencyMs = latency

	if res == nil {
		result.Error = "empty result from relay"
		return result
	}
	result.StatusCode = res.StatusCode

	// Always drain and close: the upstream connection is pooled.
	detail := ""
	if res.Body != nil {
		drained, _ := io.ReadAll(io.LimitReader(res.Body, directTestDrainLimit))
		_ = res.Body.Close()
		detail = strings.TrimSpace(string(drained))
		if len(detail) > directTestDetailLimit {
			detail = detail[:directTestDetailLimit] + "…"
		}
	}

	switch {
	case res.Err != nil:
		result.Error = strings.TrimPrefix(res.Err.Error(), "proxy: ")
	case res.StatusCode >= 200 && res.StatusCode < 300:
		result.OK = true
	default:
		result.Error = fmt.Sprintf("upstream status %d", res.StatusCode)
		if detail != "" {
			result.Error += ": " + detail
		}
	}
	return result
}

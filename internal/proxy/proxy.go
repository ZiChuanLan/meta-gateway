// Package proxy orchestrates routing, retries, upstream relay, and attempt logs.
package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/lan/meta-gateway/internal/adapters"
	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/relay"
	"github.com/lan/meta-gateway/internal/routing"
	"github.com/lan/meta-gateway/internal/store"
)

var (
	ErrCredential       = errors.New("proxy: upstream credential unavailable")
	ErrPreferredChannel = errors.New("proxy: preferred channel not eligible")
)

type Selector interface {
	Select(ctx context.Context, model string, excluded map[int64]struct{}) (routing.Decision, error)
}

type Relay interface {
	ChatCompletionsContext(ctx context.Context, upstreamURL, apiKey string, body []byte, stream bool) *relay.Result
	ForwardContext(ctx context.Context, method, upstreamURL, apiKey string, body []byte) *relay.Result
	ForwardWithHeaders(ctx context.Context, method, upstreamURL string, headers http.Header, body []byte) *relay.Result
}

type Service struct {
	selector   Selector
	relay      Relay
	db         *store.DB
	enc        *crypto.Encrypter
	retryTimes atomic.Int64
	cooldownNs atomic.Int64
	now        func() time.Time
	registry   *adapters.Registry
}

type Request struct {
	RequestID string
	Model     string
	Body      []byte
	Stream    bool
	Method    string
	// OpenAIPath is the path under the upstream OpenAI root, e.g. "chat/completions".
	OpenAIPath string
	// PreferChannelID pins upstream selection (admin try). Zero means normal routing.
	PreferChannelID int64
	// DownstreamKeyID is the authenticated client key, used for usage metering.
	DownstreamKeyID int64
	// ContentType preserves client Content-Type for multipart passthrough.
	ContentType string
	// PromptTokens/CompletionTokens/TotalTokens optional post-response accounting.
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// AttemptMeta describes which upstream was used for an admin try / last attempt.
type AttemptMeta struct {
	ChannelID   int64  `json:"channel_id"`
	ChannelName string `json:"channel_name"`
	MemberID    int64  `json:"member_id"`
	Priority    int    `json:"priority"`
	Weight      int    `json:"weight"`
}

func New(selector Selector, upstream Relay, db *store.DB, enc *crypto.Encrypter, retryTimes int, cooldown time.Duration) *Service {
	if retryTimes < 0 {
		retryTimes = 0
	}
	if cooldown < 0 {
		cooldown = 0
	}
	service := &Service{selector: selector, relay: upstream, db: db, enc: enc, now: time.Now}
	service.retryTimes.Store(int64(retryTimes))
	service.cooldownNs.Store(int64(cooldown))
	return service
}

// SetAdapterRegistry installs the platform adapter registry used to resolve
// per-channel forward adapters (OpenAI passthrough, Anthropic, Gemini, …).
func (s *Service) SetAdapterRegistry(registry *adapters.Registry) {
	s.registry = registry
}

// resolveForward returns the forward adapter for a channel, falling back to
// the OpenAI passthrough adapter when no registry is installed.
func (s *Service) resolveForward(channel domain.Channel) adapters.ForwardAdapter {
	platform := ""
	if channel.SiteID != nil {
		if site, err := s.db.Site.GetByID(*channel.SiteID); err == nil && site != nil {
			platform = site.Platform
		}
	}
	if s.registry != nil {
		return s.registry.ResolveForward(channel.TypeHint, platform)
	}
	return adapters.OpenAIPassthroughAdapter{}
}

// SetRetryPolicy hot-updates cross-channel retry count and cool-down base.
func (s *Service) SetRetryPolicy(retryTimes int, cooldown time.Duration) {
	if retryTimes < 0 {
		retryTimes = 0
	}
	if cooldown < 0 {
		cooldown = 0
	}
	s.retryTimes.Store(int64(retryTimes))
	s.cooldownNs.Store(int64(cooldown))
}

func (s *Service) ChatCompletions(ctx context.Context, req Request) *relay.Result {
	result, _ := s.ForwardWithMeta(ctx, req)
	return result
}

func (s *Service) Forward(ctx context.Context, req Request) *relay.Result {
	result, _ := s.ForwardWithMeta(ctx, req)
	return result
}

// ChatCompletionsWithMeta is the same as ChatCompletions but also reports which upstream was used.
func (s *Service) ChatCompletionsWithMeta(ctx context.Context, req Request) (*relay.Result, *AttemptMeta) {
	return s.ForwardWithMeta(ctx, req)
}

// ForwardWithMeta selects a channel, resolves credentials, and forwards to the OpenAI-compatible path.
func (s *Service) ForwardWithMeta(ctx context.Context, req Request) (*relay.Result, *AttemptMeta) {
	if strings.TrimSpace(req.OpenAIPath) == "" {
		req.OpenAIPath = "chat/completions"
	}
	if strings.TrimSpace(req.Method) == "" {
		req.Method = http.MethodPost
	}
	excluded := make(map[int64]struct{})
	var last *relay.Result
	var lastMeta *AttemptMeta
	maxAttempts := int(s.retryTimes.Load())
	cooldown := time.Duration(s.cooldownNs.Load())
	if req.PreferChannelID > 0 {
		// Admin pin: only hit the chosen channel once (no cross-channel retry).
		maxAttempts = 0
	}
	for attempt := 0; attempt <= maxAttempts; attempt++ {
		decision, err := s.selector.Select(ctx, req.Model, excluded)
		if err != nil {
			if last != nil {
				return last, lastMeta
			}
			return &relay.Result{Err: err}, nil
		}
		candidate := decision.Selected
		if req.PreferChannelID > 0 {
			pinned, ok := pickPreferred(decision, req.PreferChannelID)
			if !ok {
				return &relay.Result{Err: ErrPreferredChannel}, nil
			}
			candidate = pinned
		}
		meta := &AttemptMeta{
			ChannelID:   candidate.Channel.ID,
			ChannelName: candidate.Channel.Name,
			MemberID:    candidate.Member.ID,
			Priority:    candidate.Member.Priority,
			Weight:      candidate.Member.Weight,
		}
		var result *relay.Result
		var category string
		var retryable bool
		adapter := s.resolveForward(candidate.Channel)

		// Channel-scoped model aliases: when the matched route carries a
		// mapping_json of {"real":"…"}, clients requested the alias and we
		// must rewrite the body back to the upstream's real model name.
		mappedBody := req.Body
		if mappingJSON := strings.TrimSpace(decision.RouteMappingJSON); mappingJSON != "" {
			mappedBody = rewriteModelName(req.Body, req.Model, mappingJSON)
		}

		upstreamPath, requestBody, translateErr := adapter.TransformRequest(req.OpenAIPath, mappedBody)
		if translateErr != nil {
			// Unsupported path or conversion failure: hard failure on this
			// channel (no retry — a broken mapping will not heal).
			result = &relay.Result{Err: fmt.Errorf("proxy: %s translate: %w", adapter.Name(), translateErr)}
			category, retryable = classify(result)
			s.recordAttempt(req, candidate, attempt+1, result, category)
			if err := s.db.RouteMember.RecordFailure(candidate.Member.ID, s.now(), cooldown, category); err != nil {
				log.Printf("proxy: record failure member_id=%d: %v", candidate.Member.ID, err)
			}
			excluded[candidate.Channel.ID] = struct{}{}
			last = preserve(result)
			lastMeta = meta
			continue
		}
		upstreamURL, err := s.resolveUpstreamURL(candidate.Channel, upstreamPath, adapter)
		if err != nil {
			// Channel config is broken (e.g. empty base URL): count it as a
			// retryable failure so the request fails over to the next channel
			// instead of aborting the whole attempt loop.
			result = &relay.Result{Err: err}
			category = "invalid_base_url"
			retryable = true
			s.recordAttempt(req, candidate, attempt+1, result, category)
			if err := s.db.RouteMember.RecordFailure(candidate.Member.ID, s.now(), cooldown, category); err != nil {
				log.Printf("proxy: record failure member_id=%d: %v", candidate.Member.ID, err)
			}
			excluded[candidate.Channel.ID] = struct{}{}
			last = preserve(result)
			lastMeta = meta
			continue
		}
		// Aggregate all enabled site API keys; failover keys before leaving the channel.
		apiKeys, err := s.resolveAPIKeyPool(candidate.Channel)
		if err != nil || len(apiKeys) == 0 {
			result = &relay.Result{Err: ErrCredential}
			category = "no_credential"
			retryable = true
			s.recordAttempt(req, candidate, attempt+1, result, category)
			if err := s.db.RouteMember.RecordFailure(candidate.Member.ID, s.now(), cooldown, category); err != nil {
				log.Printf("proxy: record failure member_id=%d: %v", candidate.Member.ID, err)
			}
			excluded[candidate.Channel.ID] = struct{}{}
			last = preserve(result)
			lastMeta = meta
			continue
		}

		for keyIndex, apiKey := range apiKeys {
			headers := adapter.AuthHeaders(apiKey)
			if headers.Get("Content-Type") == "" && strings.TrimSpace(req.ContentType) != "" {
				headers.Set("Content-Type", req.ContentType)
			}
			result = s.relay.ForwardWithHeaders(ctx, req.Method, upstreamURL, headers, requestBody)
			// Convert upstream 2xx bodies back to the OpenAI contract.
			if result != nil && result.Err == nil && result.StatusCode >= 200 && result.StatusCode < 300 && result.Body != nil {
				if req.Stream {
					// Reshape native/upstream SSE into OpenAI chat.completion.chunk.
					wrapped, wrapErr := adapter.WrapStream(req.OpenAIPath, result.Body)
					if wrapErr != nil {
						result = &relay.Result{StatusCode: result.StatusCode, Header: result.Header, LatencyMs: result.LatencyMs, Err: wrapErr}
					} else {
						result.Body = wrapped
						if result.Header == nil {
							result.Header = make(http.Header)
						}
						result.Header.Set("Content-Type", "text/event-stream")
					}
				} else {
					raw, readErr := io.ReadAll(io.LimitReader(result.Body, 8<<20))
					_ = result.Body.Close()
					if readErr != nil {
						result = &relay.Result{StatusCode: result.StatusCode, Header: result.Header, LatencyMs: result.LatencyMs, Err: readErr}
					} else if converted, convErr := adapter.TransformResponse(req.OpenAIPath, raw); convErr != nil {
						// Conversion failed: surface the upstream body untouched so
						// the client still sees the real upstream error payload.
						result.Body = io.NopCloser(bytes.NewReader(raw))
					} else {
						result.Body = io.NopCloser(bytes.NewReader(converted))
						if result.Header == nil {
							result.Header = make(http.Header)
						}
						result.Header.Set("Content-Type", "application/json")
					}
				}
			}
			streamInterrupted := false
			if req.Stream && result.Err == nil && result.StatusCode >= 200 && result.StatusCode < 300 {
				first, peekErr := peekFirstChunk(result.Body)
				if peekErr != nil {
					// Upstream answered 200 and then died before emitting any
					// data. The client has not received a byte yet, so this is a
					// normal retryable failure — fail over to the next key/channel.
					_ = result.Body.Close()
					result = &relay.Result{
						StatusCode: result.StatusCode,
						Header:     result.Header,
						LatencyMs:  result.LatencyMs,
						Err:        fmt.Errorf("proxy: stream closed before first byte: %w", peekErr),
					}
					streamInterrupted = true
				} else {
					// Replay the buffered prefix, then continue streaming the rest.
					result.Body = io.NopCloser(io.MultiReader(bytes.NewReader(first), result.Body))
				}
			}
			category, retryable = classify(result)
			if streamInterrupted {
				category = "stream_interrupted"
				retryable = true
			}
			// Only the last key attempt for this channel is logged at the attempt counter
			// used for cross-channel retry; intermediate key fails stay on the same attempt.
			if keyIndex == len(apiKeys)-1 || (result.Err == nil && !retryable) {
				s.recordAttempt(req, candidate, attempt+1, result, category)
			}
			if result.Err == nil && !retryable {
				break
			}
			if errors.Is(result.Err, context.Canceled) || errors.Is(result.Err, context.DeadlineExceeded) || ctx.Err() != nil {
				break
			}
			// Retryable: try next key in the pool before failing over to another channel.
			if result.Body != nil {
				_ = result.Body.Close()
			}
		}
		if result == nil {
			return &relay.Result{Err: ErrCredential}, meta
		}

		if result.Err == nil && !retryable {
			if last != nil && last.Body != nil {
				_ = last.Body.Close()
			}
			if err := s.db.RouteMember.RecordSuccess(candidate.Member.ID); err != nil {
				log.Printf("proxy: record success member_id=%d: %v", candidate.Member.ID, err)
			}
			return result, meta
		}
		if errors.Is(result.Err, context.Canceled) || errors.Is(result.Err, context.DeadlineExceeded) || ctx.Err() != nil {
			return result, meta
		}
		if retryable {
			penalty := retryAfterCooldown(result.Header, s.now(), cooldown)
			if err := s.db.RouteMember.RecordFailure(candidate.Member.ID, s.now(), penalty, category); err != nil {
				log.Printf("proxy: record failure member_id=%d: %v", candidate.Member.ID, err)
			}
		}
		if !retryable || req.PreferChannelID > 0 {
			return result, meta
		}

		excluded[candidate.Channel.ID] = struct{}{}
		if last != nil && last.Body != nil {
			_ = last.Body.Close()
		}
		last = preserve(result)
		lastMeta = meta
		if result.Body != nil {
			_ = result.Body.Close()
		}
	}
	if last != nil {
		return last, lastMeta
	}
	return &relay.Result{Err: routing.ErrNoEligible}, lastMeta
}

func pickPreferred(decision routing.Decision, channelID int64) (domain.RoutingCandidate, bool) {
	for _, evaluation := range decision.Candidates {
		if evaluation.Eligible && evaluation.Candidate.Channel.ID == channelID {
			return evaluation.Candidate, true
		}
	}
	return domain.RoutingCandidate{}, false
}

// resolveAPIKeyPool builds the ordered list of plaintext API keys for a channel.
// Prefer the bound credential first, then every other enabled api_key on the same site.
func (s *Service) resolveAPIKeyPool(channel domain.Channel) ([]string, error) {
	seen := make(map[int64]struct{})
	var keys []string

	appendCredential := func(credential *domain.Credential) {
		if credential == nil {
			return
		}
		if _, exists := seen[credential.ID]; exists {
			return
		}
		if credential.Status != domain.StatusEnabled || len(credential.SecretEnc) == 0 {
			return
		}
		if !strings.EqualFold(credential.Kind, "api_key") {
			return
		}
		plaintext, err := s.enc.Decrypt(string(credential.SecretEnc))
		if err != nil || len(plaintext) == 0 {
			return
		}
		seen[credential.ID] = struct{}{}
		keys = append(keys, string(plaintext))
	}

	if channel.CredentialID != nil {
		bound, err := s.db.Credential.GetByID(*channel.CredentialID)
		if err == nil {
			appendCredential(bound)
		}
	}
	if channel.SiteID != nil {
		pool, err := s.db.Credential.ListEnabledAPIKeysBySite(*channel.SiteID)
		if err != nil {
			if len(keys) == 0 {
				return nil, ErrCredential
			}
			return keys, nil
		}
		for index := range pool {
			appendCredential(&pool[index])
		}
	}
	if len(keys) == 0 {
		return nil, ErrCredential
	}
	return keys, nil
}

func (s *Service) resolveAPIKey(channel domain.Channel) (string, error) {
	keys, err := s.resolveAPIKeyPool(channel)
	if err != nil || len(keys) == 0 {
		return "", ErrCredential
	}
	return keys[0], nil
}

func (s *Service) resolveUpstreamURL(channel domain.Channel, apiPath string, adapter adapters.ForwardAdapter) (string, error) {
	baseURL := strings.TrimSpace(channel.BaseURL)
	if baseURL == "" {
		if channel.SiteID == nil {
			return "", fmt.Errorf("proxy: channel base url unavailable")
		}
		site, err := s.db.Site.GetByID(*channel.SiteID)
		if err != nil || site == nil || strings.TrimSpace(site.BaseURL) == "" {
			return "", fmt.Errorf("proxy: channel base url unavailable")
		}
		baseURL = site.BaseURL
	}
	upstreamURL, err := adapter.BuildUpstreamURL(baseURL, apiPath)
	if err != nil {
		return "", fmt.Errorf("proxy: invalid base url")
	}
	return upstreamURL, nil
}

// rewriteModelName rewrites the JSON "model" field of a request body from the
// client-facing alias back to the upstream's real model name. mappingJSON is
// the route's mapping_json value, expected to be {"real":"upstream-model"}.
// It is a no-op when the body is not JSON, the field is absent, or it does not
// match the requested alias.
func rewriteModelName(body []byte, requestedModel, mappingJSON string) []byte {
	if len(body) == 0 || requestedModel == "" || mappingJSON == "" {
		return body
	}
	var mapping struct {
		Real string `json:"real"`
	}
	if err := json.Unmarshal([]byte(mappingJSON), &mapping); err != nil || mapping.Real == "" {
		return body
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		// Not JSON (multipart etc.): leave untouched.
		return body
	}
	current, ok := payload["model"].(string)
	if !ok || current != requestedModel {
		return body
	}
	payload["model"] = mapping.Real
	rewritten, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return rewritten
}

func (s *Service) recordAttempt(req Request, candidate domain.RoutingCandidate, attempt int, result *relay.Result, category string) {
	status := result.StatusCode
	if result.Err != nil {
		status = http.StatusBadGateway
	} else if status == 0 {
		status = http.StatusOK
	}
	errorBrief := ""
	if result.Err != nil || isRetryableStatus(result.StatusCode) {
		errorBrief = category
	}
	_, err := s.db.ProxyLog.Insert(&domain.ProxyLog{
		RequestID:        req.RequestID,
		ChannelID:        candidate.Channel.ID,
		RouteID:          candidate.Member.RouteID,
		Model:            req.Model,
		Status:           status,
		LatencyMs:        result.LatencyMs,
		Attempt:          attempt,
		ErrorBrief:       errorBrief,
		DownstreamKeyID:  req.DownstreamKeyID,
		PromptTokens:     req.PromptTokens,
		CompletionTokens: req.CompletionTokens,
		TotalTokens:      req.TotalTokens,
		Stream:           req.Stream,
		Path:             req.OpenAIPath,
	})
	if err != nil {
		log.Printf("proxy: record attempt request_id=%s channel_id=%d attempt=%d: %v", req.RequestID, candidate.Channel.ID, attempt, err)
	}
}

// RecordUsage persists metered tokens for a completed relay response.
func (s *Service) RecordUsage(req Request, channelID int64, status int, tokens struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}) {
	total := tokens.TotalTokens
	if total <= 0 {
		total = tokens.PromptTokens + tokens.CompletionTokens
	}
	if total <= 0 {
		return
	}
	if s.db == nil || s.db.Usage == nil {
		return
	}
	_, err := s.db.Usage.Insert(&domain.UsageRecord{
		RequestID:        req.RequestID,
		DownstreamKeyID:  req.DownstreamKeyID,
		ChannelID:        channelID,
		Model:            req.Model,
		Path:             req.OpenAIPath,
		Stream:           req.Stream,
		PromptTokens:     tokens.PromptTokens,
		CompletionTokens: tokens.CompletionTokens,
		TotalTokens:      total,
		Status:           status,
	})
	if err != nil {
		log.Printf("proxy: record usage request_id=%s: %v", req.RequestID, err)
	}
	if req.DownstreamKeyID > 0 && total > 0 && s.db.DownstreamKey != nil {
		if err := s.db.DownstreamKey.AddUsage(req.DownstreamKeyID, total); err != nil {
			log.Printf("proxy: add key usage request_id=%s: %v", req.RequestID, err)
		}
	}
	if s.db.ProxyLog != nil && req.RequestID != "" {
		if err := s.db.ProxyLog.UpdateTokensByRequestID(req.RequestID, tokens.PromptTokens, tokens.CompletionTokens, total); err != nil {
			log.Printf("proxy: update log tokens request_id=%s: %v", req.RequestID, err)
		}
	}
}

func classify(result *relay.Result) (string, bool) {
	if result.Err != nil {
		if errors.Is(result.Err, context.Canceled) || errors.Is(result.Err, context.DeadlineExceeded) {
			return "cancelled", false
		}
		return "transport", true
	}
	if isRetryableStatus(result.StatusCode) {
		return fmt.Sprintf("upstream_status_%d", result.StatusCode), true
	}
	return "", false
}

func isRetryableStatus(status int) bool {
	// Any 4xx is retryable: a channel that rejects the request (bad auth, model
	// unavailable, quota, malformed upstream mapping) should fail over to the
	// next channel instead of surfacing the error immediately.
	if status >= 400 && status < 500 {
		return true
	}
	switch status {
	case http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusInternalServerError,
		http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout,
		// Cloudflare origin-down / overload codes.
		528, 529, 530:
		return true
	default:
		return false
	}
}

func preserve(result *relay.Result) *relay.Result {
	if result == nil || result.Body == nil {
		return result
	}
	body, err := io.ReadAll(io.LimitReader(result.Body, 10*1024*1024))
	if err != nil {
		return &relay.Result{StatusCode: result.StatusCode, Header: result.Header, LatencyMs: result.LatencyMs, Err: fmt.Errorf("proxy: preserve upstream response: %w", err)}
	}
	return &relay.Result{StatusCode: result.StatusCode, Header: result.Header.Clone(), LatencyMs: result.LatencyMs, Body: io.NopCloser(bytes.NewReader(body))}
}

// maxStreamFirstChunkBytes bounds how much of a stream prefix we buffer before
// committing the response to the client.
const maxStreamFirstChunkBytes = 256 * 1024

// peekFirstChunk reads the leading bytes of an upstream stream response. SSE
// frames end with a blank line, so reading until "\n\n" (or a bounded amount of
// data) lets the gateway detect a 200 that immediately died and fail over to
// the next channel instead of surfacing a silent truncated response.
func peekFirstChunk(body io.Reader) ([]byte, error) {
	var buffered bytes.Buffer
	buffer := make([]byte, 4096)
	for {
		readN, readErr := body.Read(buffer)
		if readN > 0 {
			buffered.Write(buffer[:readN])
			if buffered.Len() >= maxStreamFirstChunkBytes || bytes.Contains(buffered.Bytes(), []byte("\n\n")) {
				return buffered.Bytes(), nil
			}
		}
		if readErr != nil {
			if readErr == io.EOF && buffered.Len() > 0 {
				return buffered.Bytes(), nil
			}
			return nil, readErr
		}
	}
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

// RecordStreamFailure marks the member that served a stream as failed after the
// upstream connection broke mid-stream. The partial response is already on the
// wire, so the current request cannot be retried, but cooling the member down
// makes the next request fail over to a healthier channel.
func (s *Service) RecordStreamFailure(memberID int64) {
	cooldown := time.Duration(s.cooldownNs.Load())
	if err := s.db.RouteMember.RecordFailure(memberID, s.now(), cooldown, "stream_interrupted"); err != nil {
		log.Printf("proxy: record stream failure member_id=%d: %v", memberID, err)
	}
}

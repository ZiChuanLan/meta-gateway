// Package proxy orchestrates routing, retries, upstream relay, and attempt logs.
package proxy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/lan/meta-gateway/internal/adapters"
	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/relay"
	"github.com/lan/meta-gateway/internal/routing"
	"github.com/lan/meta-gateway/internal/store"
)

var ErrCredential = errors.New("proxy: upstream credential unavailable")

type Selector interface {
	Select(ctx context.Context, model string, excluded map[int64]struct{}) (routing.Decision, error)
}

type Relay interface {
	ChatCompletionsContext(ctx context.Context, upstreamURL, apiKey string, body []byte, stream bool) *relay.Result
}

type Service struct {
	selector   Selector
	relay      Relay
	db         *store.DB
	enc        *crypto.Encrypter
	retryTimes int
	cooldown   time.Duration
	now        func() time.Time
}

type Request struct {
	RequestID string
	Model     string
	Body      []byte
	Stream    bool
}

func New(selector Selector, upstream Relay, db *store.DB, enc *crypto.Encrypter, retryTimes int, cooldown time.Duration) *Service {
	if retryTimes < 0 {
		retryTimes = 0
	}
	if cooldown < 0 {
		cooldown = 0
	}
	return &Service{selector: selector, relay: upstream, db: db, enc: enc, retryTimes: retryTimes, cooldown: cooldown, now: time.Now}
}

func (s *Service) ChatCompletions(ctx context.Context, req Request) *relay.Result {
	excluded := make(map[int64]struct{})
	var last *relay.Result
	for attempt := 0; attempt <= s.retryTimes; attempt++ {
		decision, err := s.selector.Select(ctx, req.Model, excluded)
		if err != nil {
			if last != nil {
				return last
			}
			return &relay.Result{Err: err}
		}
		candidate := decision.Selected
		apiKey, err := s.resolveAPIKey(candidate.Channel)
		if err != nil {
			return &relay.Result{Err: ErrCredential}
		}

		upstreamURL, err := s.resolveChatURL(candidate.Channel)
		if err != nil {
			return &relay.Result{Err: err}
		}
		result := s.relay.ChatCompletionsContext(ctx, upstreamURL, apiKey, req.Body, req.Stream)
		category, retryable := classify(result)
		s.recordAttempt(req, candidate, attempt+1, result, category)

		if result.Err == nil && !retryable {
			if last != nil && last.Body != nil {
				_ = last.Body.Close()
			}
			if err := s.db.RouteMember.RecordSuccess(candidate.Member.ID); err != nil {
				log.Printf("proxy: record success member_id=%d: %v", candidate.Member.ID, err)
			}
			return result
		}
		if errors.Is(result.Err, context.Canceled) || errors.Is(result.Err, context.DeadlineExceeded) || ctx.Err() != nil {
			return result
		}
		if retryable {
			if err := s.db.RouteMember.RecordFailure(candidate.Member.ID, s.now(), s.cooldown, category); err != nil {
				log.Printf("proxy: record failure member_id=%d: %v", candidate.Member.ID, err)
			}
		}
		if !retryable {
			return result
		}

		excluded[candidate.Channel.ID] = struct{}{}
		if last != nil && last.Body != nil {
			_ = last.Body.Close()
		}
		last = preserve(result)
		if result.Body != nil {
			_ = result.Body.Close()
		}
	}
	if last != nil {
		return last
	}
	return &relay.Result{Err: routing.ErrNoEligible}
}

func (s *Service) resolveAPIKey(channel domain.Channel) (string, error) {
	if channel.CredentialID == nil {
		return "", ErrCredential
	}
	credential, err := s.db.Credential.GetByID(*channel.CredentialID)
	if err != nil || credential == nil || credential.Status != domain.StatusEnabled {
		return "", ErrCredential
	}
	plaintext, err := s.enc.Decrypt(string(credential.SecretEnc))
	if err != nil {
		return "", ErrCredential
	}
	return string(plaintext), nil
}

func (s *Service) resolveChatURL(channel domain.Channel) (string, error) {
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
	upstreamURL, err := adapters.JoinOpenAIPath(baseURL, "chat/completions")
	if err != nil {
		return "", fmt.Errorf("proxy: invalid base url")
	}
	return upstreamURL, nil
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
		RequestID:  req.RequestID,
		ChannelID:  candidate.Channel.ID,
		Model:      req.Model,
		Status:     status,
		LatencyMs:  result.LatencyMs,
		Attempt:    attempt,
		ErrorBrief: errorBrief,
	})
	if err != nil {
		log.Printf("proxy: record attempt request_id=%s channel_id=%d attempt=%d: %v", req.RequestID, candidate.Channel.ID, attempt, err)
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
	switch status {
	case http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusInternalServerError,
		http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
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

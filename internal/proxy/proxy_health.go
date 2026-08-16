// Package proxy orchestrates routing, retries, upstream relay, and attempt logs.
package proxy

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/relay"
	"github.com/lan/meta-gateway/internal/usage"
	"github.com/lan/meta-gateway/internal/webhook"
)

// recordMemberFailure records a member failure (member cooldown + channel
// consecutive counter) and auto-disables the channel once the channel-level
// consecutive failures reach the configured threshold.
func (s *Service) recordMemberFailure(memberID, channelID int64, model string, cooldown time.Duration, category string) {
	if err := s.db.RouteMember.RecordFailure(memberID, s.now(), cooldown, category); err != nil {
		log.Printf("proxy: record failure member_id=%d: %v", memberID, err)
	}
	s.recordChannelFailure(channelID)
	s.observeError(channelID, model)
}

// recordChannelFailure increments the channel consecutive-failure counter and
// auto-disables the channel once the threshold is reached.
func (s *Service) recordChannelFailure(channelID int64) {
	threshold := int(s.autoDisableThreshold.Load())
	if threshold <= 0 || channelID <= 0 {
		return
	}
	count, err := s.db.Channel.RecordRelayFailure(channelID)
	if err != nil {
		log.Printf("proxy: channel relay failure channel_id=%d: %v", channelID, err)
		return
	}
	if count >= threshold {
		if err := s.db.Channel.AutoDisable(channelID); err != nil {
			log.Printf("proxy: auto disable channel_id=%d: %v", channelID, err)
		} else if s.notifier != nil {
			name := ""
			if ch, err := s.db.Channel.GetByID(channelID); err == nil && ch != nil {
				name = ch.Name
			}
			s.notifier.Notify(context.Background(), webhook.ChannelDisabled, channelID, name,
				fmt.Sprintf("%d consecutive failures", count))
			// Request-failure alert through the full matrix (bark/serverchan/
			// telegram/smtp too, not just the legacy webhook URL).
			s.notifier.SendAlert(context.Background(), webhook.AlertWarning, "请求失败告警",
				fmt.Sprintf("渠道 #%d (%s) 连续 %d 次失败，已自动禁用。", channelID, name, count))
		}
	}
}

// recordMemberSuccess resets the channel consecutive-failure counter.
func (s *Service) recordMemberSuccess(channelID int64) {
	if channelID <= 0 {
		return
	}
	if err := s.db.Channel.RecordRelaySuccess(channelID); err != nil {
		log.Printf("proxy: channel relay success channel_id=%d: %v", channelID, err)
	}
}

func (s *Service) recordAttempt(req Request, candidate domain.RoutingCandidate, attempt int, result *relay.Result, category string, keyFP string) {
	status := result.StatusCode
	if status == 0 && result.Err != nil {
		status = http.StatusBadGateway
	} else if status == 0 {
		status = http.StatusOK
	}
	errorBrief := ""
	if result.Err != nil || isRetryableStatus(result.StatusCode) || category == "refresh_retry" {
		errorBrief = category
	}
	_, err := s.db.ProxyLog.Insert(&domain.ProxyLog{
		RequestID:             req.RequestID,
		ChannelID:             candidate.Channel.ID,
		RouteID:               candidate.Member.RouteID,
		Model:                 req.Model,
		Status:                status,
		LatencyMs:             result.LatencyMs,
		Attempt:               attempt,
		ErrorBrief:            errorBrief,
		DownstreamKeyID:       req.DownstreamKeyID,
		PromptTokens:          req.PromptTokens,
		CompletionTokens:      req.CompletionTokens,
		TotalTokens:           req.TotalTokens,
		Stream:                req.Stream,
		Path:                  req.OpenAIPath,
		SessionKey:            req.SessionKey,
		ReasoningEffort:       req.ReasoningEffort,
		MappedReasoningEffort: req.MappedReasoningEffort,
		KeyFingerprint:        keyFP,
		UpstreamRequestID:     upstreamRequestID(result),
	})
	if err != nil {
		log.Printf("proxy: record attempt request_id=%s channel_id=%d attempt=%d: %v", req.RequestID, candidate.Channel.ID, attempt, err)
	}
}

// RecordUsage persists metered tokens for a completed relay response.
func (s *Service) RecordUsage(req Request, channelID int64, status int, tokens usage.Tokens) {
	// Stable-first promotion: successful traffic on a grayscale channel counts
	// toward graduation; the store clears the mark when the threshold is met
	// with no consecutive failures.
	if status >= 200 && status < 300 {
		if threshold := int(s.grayPromoteRequests.Load()); threshold > 0 && s.db != nil && s.db.Channel != nil {
			if promoted, err := s.db.Channel.RecordGraySuccess(channelID, threshold); err != nil {
				log.Printf("proxy: gray success channel_id=%d: %v", channelID, err)
			} else if promoted {
				log.Printf("proxy: channel %d promoted from stable-first grayscale", channelID)
			}
		}
	}
	total := tokens.TotalTokens
	if total <= 0 {
		total = tokens.PromptTokens + tokens.CompletionTokens
	}
	if total <= 0 || s.db == nil {
		return
	}
	// Billing: cost = key unit prices × model ratio, computed and persisted at
	// record time so bills are stable even if prices are edited later.
	record := &domain.UsageRecord{
		RequestID:           req.RequestID,
		DownstreamKeyID:     req.DownstreamKeyID,
		ChannelID:           channelID,
		Model:               req.Model,
		Path:                req.OpenAIPath,
		Stream:              req.Stream,
		PromptTokens:        tokens.PromptTokens,
		CompletionTokens:    tokens.CompletionTokens,
		TotalTokens:         total,
		CacheReadTokens:     tokens.CacheReadTokens,
		CacheCreationTokens: tokens.CacheCreationTokens,
		Status:              status,
	}
	// Tenant group for group-quota accrual (same transaction).
	if req.DownstreamKeyID > 0 && s.db.DownstreamKey != nil {
		if key, err := s.db.DownstreamKey.GetByID(req.DownstreamKeyID); err == nil && key != nil {
			record.GroupName = key.GroupName
		}
	}
	record.Cost = s.billingCost(req, tokens)
	// Usage row, downstream-key quota increment, and proxy-log token backfill
	// commit in one transaction (store.RecordRelayUsage), so a partial failure
	// can never leave metered usage without its quota charge.
	if err := s.db.RecordRelayUsage(record, req.DownstreamKeyID); err != nil {
		log.Printf("proxy: record usage request_id=%s: %v", req.RequestID, err)
	}
}

// billingCost computes the persisted cost for a usage record: per-1k unit
// prices of the downstream key, multiplied by the model's billing ratio.
// Cache-read tokens are billed at the prompt rate. Failures are never fatal;
// a price lookup error degrades to 0 cost rather than dropping the record.
func (s *Service) billingCost(req Request, tokens usage.Tokens) float64 {
	if s.db == nil {
		return 0
	}
	ratio := 1.0
	if s.db.ModelRatio != nil {
		if r, err := s.db.ModelRatio.GetRatio(req.Model); err == nil {
			ratio = r
		} else {
			log.Printf("proxy: billing ratio model=%s: %v", req.Model, err)
		}
	}
	pricePrompt, priceCompletion := 0.0, 0.0
	if req.DownstreamKeyID > 0 && s.db.DownstreamKey != nil {
		if key, err := s.db.DownstreamKey.GetByID(req.DownstreamKeyID); err == nil && key != nil {
			pricePrompt, priceCompletion = key.PricePromptPer1k, key.PriceCompletionPer1k
		}
	}
	prompt := float64(tokens.PromptTokens + tokens.CacheReadTokens + tokens.CacheCreationTokens)
	completion := float64(tokens.CompletionTokens)
	return (prompt/1000.0*pricePrompt + completion/1000.0*priceCompletion) * ratio
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

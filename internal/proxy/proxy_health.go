// Package proxy orchestrates routing, retries, upstream relay, and attempt logs.
package proxy

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/relay"
	"github.com/lan/meta-gateway/internal/usage"
	"github.com/lan/meta-gateway/internal/webhook"
)

// recordMemberFailure records a member failure (member cooldown + channel
// consecutive counter) and auto-disables the channel once the channel-level
// consecutive failures reach the configured threshold.
//
// Probe traffic is excluded: a failing probe is the answer the operator asked
// for, not a fault, so it must not cool a member down, bump the channel
// consecutive counter, or nudge the error rate.
func (s *Service) recordMemberFailure(req Request, memberID, channelID int64, model string, cooldown time.Duration, category string) {
	if req.Probe {
		return
	}
	if !s.faultProtectionEnabled.Load() {
		return
	}
	if err := s.db.RouteMember.RecordFailure(memberID, s.now(), cooldown, category); err != nil {
		log.Printf("proxy: record failure member_id=%d: %v", memberID, err)
	}
	s.recordChannelFailure(channelID)
	s.observeError(channelID, model)
}

// transportPenalty implements the jitter exemption for transport failures
// (connection refused, TLS, timeouts): the FIRST failure of a consecutive
// streak earns no member cooldown, while a repeat inside the same streak
// (no success in between) earns the full one. The error-aware score already
// deprioritizes the channel from the first failure on.
func (s *Service) transportPenalty(memberID int64, base time.Duration) time.Duration {
	s.transportMu.Lock()
	defer s.transportMu.Unlock()
	if s.transportFails[memberID] <= 0 {
		return 0
	}
	return base
}

// observeTransportFailure advances the member's consecutive transport-failure
// streak (cleared by resetTransportFails on a successful relay).
func (s *Service) observeTransportFailure(memberID int64) {
	s.transportMu.Lock()
	defer s.transportMu.Unlock()
	if s.transportFails == nil {
		s.transportFails = make(map[int64]int)
	}
	s.transportFails[memberID]++
}

// resetTransportFails clears the member's consecutive transport-failure streak
// after a successful relay.
func (s *Service) resetTransportFails(memberID int64) {
	s.transportMu.Lock()
	defer s.transportMu.Unlock()
	delete(s.transportFails, memberID)
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
		} else if notifier := s.notifier.Load(); notifier != nil {
			name := ""
			if ch, err := s.db.Channel.GetByID(channelID); err == nil && ch != nil {
				name = ch.Name
			}
			notifier.Notify(context.Background(), webhook.ChannelDisabled, channelID, name,
				fmt.Sprintf("%d consecutive failures", count))
			// Request-failure alert through the full matrix (bark/serverchan/
			// telegram/smtp too, not just the legacy webhook URL).
			notifier.SendAlert(context.Background(), webhook.AlertWarning, "请求失败告警",
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
	// Probes are synthetic; logging them would flood the proxy log with
	// traffic no client asked for. Their outcome lives in probe_results.
	if req.Probe {
		return
	}
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
		ErrorDetail:           attemptErrorDetail(result),
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

// attemptErrorDetail extracts a short excerpt of what the upstream actually
// returned for a failed attempt — the error body text for HTTP failures, or
// the transport error string when nothing arrived. Successes and live stream
// bodies are left untouched (only non-2xx bodies are buffered, so reading
// them is safe).
func attemptErrorDetail(result *relay.Result) string {
	if result == nil {
		return ""
	}
	var text string
	switch {
	case result.Err != nil:
		text = result.Err.Error()
	case result.StatusCode >= 400 && result.Body != nil:
		text = modelNotFoundText(result)
	default:
		return ""
	}
	text = strings.TrimSpace(text)
	if len(text) > 600 {
		text = text[:600]
		for len(text) > 0 && !utf8.ValidString(text) {
			text = text[:len(text)-1]
		}
	}
	return text
}

// RecordUsage persists metered tokens for a completed relay response.
func (s *Service) RecordUsage(req Request, channelID int64, status int, tokens usage.Tokens) {
	// Stable-first promotion: successful traffic on a grayscale channel counts
	// toward graduation; the store clears the mark when the threshold is met
	// with no consecutive failures.
	if status >= 200 && status < 300 {
		modelGrayHandled := false
		if req.RouteID > 0 && s.db != nil && s.db.Route != nil {
			if route, err := s.db.Route.GetByID(req.RouteID); err != nil {
				log.Printf("proxy: model gray lookup route=%d: %v", req.RouteID, err)
			} else if route != nil && route.StableFirst != nil {
				modelGrayHandled = true
				if *route.StableFirst && req.GrayAttempt {
					threshold := int(s.grayPromoteRequests.Load())
					if route.StableFirstPromoteRequests != nil {
						threshold = *route.StableFirstPromoteRequests
					}
					if promoted, err := s.db.Route.RecordGraySuccess(req.RouteID, threshold); err != nil {
						log.Printf("proxy: model gray success route=%d: %v", req.RouteID, err)
					} else if promoted {
						log.Printf("proxy: model route %d promoted from stable-first grayscale", req.RouteID)
					}
				}
			}
		}
		if !modelGrayHandled {
			if threshold := int(s.grayPromoteRequests.Load()); threshold > 0 && s.db != nil && s.db.Channel != nil {
				if promoted, err := s.db.Channel.RecordGraySuccess(channelID, threshold); err != nil {
					log.Printf("proxy: gray success channel_id=%d: %v", channelID, err)
				} else if promoted {
					log.Printf("proxy: channel %d promoted from stable-first grayscale", channelID)
				}
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
	if !s.faultProtectionEnabled.Load() {
		return
	}
	cooldown := time.Duration(s.cooldownNs.Load())
	if err := s.db.RouteMember.RecordFailure(memberID, s.now(), cooldown, "stream_interrupted"); err != nil {
		log.Printf("proxy: record stream failure member_id=%d: %v", memberID, err)
	}
}

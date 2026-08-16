// Package proxy orchestrates routing, retries, upstream relay, and attempt logs.
package proxy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/webhook"
)

// resolveAPIKeyPool builds the ordered list of plaintext API keys for a channel.
// Prefer the bound credential first, then every other enabled api_key on the same site.
// Keys that hit the per-key auto-disable threshold are excluded until they heal.
// Keys whose models_csv allowlist does not cover the requested model are skipped
// (empty model = no filtering). With key-pool rotation disabled, only the bound
// key (or the first pool key) is used.
func (s *Service) resolveAPIKeyPool(channel domain.Channel, model string) ([]string, error) {
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
		// Bearer-style kinds only: api_key plus session/access_token (the
		// latter can 401 → refresh-retry through the check-in machinery).
		kind := strings.ToLower(strings.TrimSpace(credential.Kind))
		if kind != "api_key" && kind != "session" && kind != "access_token" {
			return
		}
		if !modelAllowedByKey(model, credential.ModelsCSV) {
			return
		}
		plaintext, err := s.enc.Decrypt(string(credential.SecretEnc))
		if err != nil || len(plaintext) == 0 {
			return
		}
		// Per-key auto-disable: keys on the disabled list are skipped until
		// their penalty expires (AxonHub-style, avoids nuking the whole
		// channel for one bad key). Scoped to this channel.
		if s.keyDisabled(channel.ID, string(plaintext)) {
			return
		}
		seen[credential.ID] = struct{}{}
		keys = append(keys, string(plaintext))
	}

	if !s.keyPoolRotation.Load() {
		// Rotation off: never rotate through the pool — bound key first, or
		// the first enabled pool key as a fallback.
		if channel.CredentialID != nil {
			bound, err := s.db.Credential.GetByID(*channel.CredentialID)
			if err == nil {
				appendCredential(bound)
			}
		} else if channel.SiteID != nil {
			pool, err := s.db.Credential.ListEnabledAPIKeysBySite(*channel.SiteID)
			if err == nil && len(pool) > 0 {
				appendCredential(&pool[0])
			}
		}
		if len(keys) == 0 {
			return nil, ErrCredential
		}
		return keys, nil
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

// modelAllowedByKey reports whether a key's model allowlist (models_csv)
// permits serving the model. Empty allowlist = all models; entries support
// "*" suffix wildcards ("gpt-4*" matches "gpt-4o"). An empty model skips
// filtering entirely.
func modelAllowedByKey(model, modelsCSV string) bool {
	if strings.TrimSpace(model) == "" || strings.TrimSpace(modelsCSV) == "" {
		return true
	}
	for _, part := range strings.Split(modelsCSV, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if strings.HasSuffix(part, "*") {
			if strings.HasPrefix(model, strings.TrimSuffix(part, "*")) {
				return true
			}
		} else if part == model {
			return true
		}
	}
	return false
}

// keyFingerprint hashes an upstream api key so the in-memory failure tables
// never hold (or log) the plaintext secret.
func keyFingerprint(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:8])
}

// recordKeyFailure increments the per channel × key × status counter and
// returns true when the auto-disable threshold was crossed (the key is then
// excluded from the pool until a success or the penalty TTL expires).
func (s *Service) recordKeyFailure(channelID int64, key string, status int) bool {
	threshold := int(s.keyFailThreshold.Load())
	if threshold <= 0 {
		return false // key auto-exclusion disabled
	}
	fp := keyFingerprint(key)
	now := s.now()
	s.keyErrMu.Lock()
	defer s.keyErrMu.Unlock()
	// Expire stale penalties (30 minutes) so a fixed key heals automatically.
	// Scoped per channel: one channel's exclusion must not be lifted by another.
	for dk, until := range s.disabledKeys {
		if dk.channelID == channelID && now.After(until) {
			delete(s.disabledKeys, dk)
		}
	}
	if s.keyErrCounts[channelID] == nil {
		s.keyErrCounts[channelID] = make(map[string]map[int]int)
	}
	if s.keyErrCounts[channelID][fp] == nil {
		s.keyErrCounts[channelID][fp] = make(map[int]int)
	}
	s.keyErrCounts[channelID][fp][status]++
	if s.keyErrCounts[channelID][fp][status] >= threshold {
		delete(s.keyErrCounts[channelID], fp)
		s.disabledKeys[disabledKey{channelID: channelID, fp: fp}] = now.Add(30 * time.Minute)
		return true
	}
	return false
}

// recordKeySuccess clears a key's failure counters and lifts its disable on
// the same channel only (another channel using the same key must not heal it).
func (s *Service) recordKeySuccess(channelID int64, key string) {
	fp := keyFingerprint(key)
	s.keyErrMu.Lock()
	defer s.keyErrMu.Unlock()
	delete(s.disabledKeys, disabledKey{channelID: channelID, fp: fp})
	if s.keyErrCounts[channelID] != nil {
		delete(s.keyErrCounts[channelID], fp)
	}
}

// keyDisabled reports whether the key is currently excluded from the pool on
// the given channel.
func (s *Service) keyDisabled(channelID int64, key string) bool {
	fp := keyFingerprint(key)
	s.keyErrMu.Lock()
	defer s.keyErrMu.Unlock()
	until, ok := s.disabledKeys[disabledKey{channelID: channelID, fp: fp}]
	if !ok {
		return false
	}
	if s.now().After(until) {
		delete(s.disabledKeys, disabledKey{channelID: channelID, fp: fp})
		return false
	}
	return true
}

// cascadeChannelIfAllKeysDisabled implements the AxonHub all-keys-down rule:
// when the per-key disabled set leaves the channel with no usable key (the
// pool is empty because every key is disabled), the channel itself is
// auto-disabled — a bad-key storm must not leave a half-broken channel in the
// routing pool with zero credentials.
func (s *Service) cascadeChannelIfAllKeysDisabled(channel domain.Channel) {
	if s.autoDisableThreshold.Load() <= 0 || channel.ID <= 0 {
		return
	}
	// Pool still resolves keys → other keys remain usable, no cascade.
	if keys, err := s.resolveAPIKeyPool(channel, ""); err == nil && len(keys) > 0 {
		return
	}
	// Pool empty: distinguish "channel has no keys at all" (nothing to
	// cascade) from "every enabled key is now disabled" (cascade).
	if channel.SiteID == nil {
		return
	}
	all, err := s.db.Credential.ListEnabledAPIKeysBySite(*channel.SiteID)
	if err != nil || len(all) == 0 {
		return
	}
	if err := s.db.Channel.AutoDisable(channel.ID); err != nil {
		log.Printf("proxy: cascade disable channel_id=%d (all keys disabled): %v", channel.ID, err)
		return
	}
	log.Printf("proxy: auto-disabled channel %d: all api keys disabled", channel.ID)
	if s.notifier != nil {
		go s.notifier.Notify(context.Background(), webhook.ChannelDisabled, channel.ID, channel.Name, "all api keys disabled")
		s.notifier.SendAlert(context.Background(), webhook.AlertWarning, "请求失败告警", fmt.Sprintf("渠道 #%d (%s) 所有 API Key 均被禁用，渠道已级联禁用。", channel.ID, channel.Name))
	}
}

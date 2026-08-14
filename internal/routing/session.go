package routing

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// maxSessionHeaderLength bounds the explicit session header value so a single
// header cannot grow the sticky map with unbounded keys.
const maxSessionHeaderLength = 256

// SessionKeyFromRequest derives a sticky-session key for a relay request.
// An explicit session header wins when present; otherwise a content digest of
// the first user message identifies the conversation. The digest is stable
// across turns (the first user message does not change as the conversation
// grows), so stateless clients resume on the same upstream channel.
func SessionKeyFromRequest(body []byte, headerValue string) string {
	headerValue = strings.TrimSpace(headerValue)
	if headerValue != "" {
		if len(headerValue) > maxSessionHeaderLength {
			headerValue = headerValue[:maxSessionHeaderLength]
		}
		return headerValue
	}
	return SessionKeyFromBody(body)
}

// SessionKeyFromBody derives a sticky-session key from a request body,
// following the identity chain: prompt_cache_key / session_id fields →
// conversation id (metadata.user_id, then conversation_id) → first user
// message content hash. Each level is more specific than the last; the first
// non-empty hit wins. Prefixes keep the levels distinguishable in the sticky
// map so a conversation that later gains an explicit id still binds fresh.
func SessionKeyFromBody(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var payload struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
		Metadata struct {
			UserID string `json:"user_id"`
		} `json:"metadata"`
		ConversationID string `json:"conversation_id"`
		PromptCacheKey string `json:"prompt_cache_key"`
		SessionID      string `json:"session_id"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	// 1. Explicit cache/identity keys (agents that manage prompt caching
	// across turns — e.g. Claude Code — pin the same channel for the cache).
	if key := strings.TrimSpace(payload.PromptCacheKey); key != "" {
		return "c:" + key
	}
	if key := strings.TrimSpace(payload.SessionID); key != "" {
		return "x:" + key
	}
	// 2. Conversation id: metadata.user_id is the standard conversation
	// carrier (Anthropic/Claude Code); conversation_id is the OpenAI-side
	// alias.
	if key := strings.TrimSpace(payload.Metadata.UserID); key != "" {
		return "u:" + key
	}
	if key := strings.TrimSpace(payload.ConversationID); key != "" {
		return "n:" + key
	}
	// 3. First user message content hash: stable across turns (the first user
	// message does not change as the conversation grows), so stateless
	// clients resume on the same upstream channel.
	for _, message := range payload.Messages {
		if message.Role != "user" {
			continue
		}
		digest := sha256.Sum256(message.Content)
		return "s:" + hex.EncodeToString(digest[:])
	}
	return ""
}

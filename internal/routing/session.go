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

// SessionKeyFromBody digests the first user message of an OpenAI-style chat
// body. Returns "" when the body is not JSON or has no user message.
func SessionKeyFromBody(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var payload struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	for _, message := range payload.Messages {
		if message.Role != "user" {
			continue
		}
		digest := sha256.Sum256(message.Content)
		return "s:" + hex.EncodeToString(digest[:])
	}
	return ""
}

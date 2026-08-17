// Package proxy orchestrates routing, retries, upstream relay, and attempt logs.
package proxy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

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
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		// Not JSON (multipart etc.): leave untouched.
		return body
	}
	var current string
	rawModel, ok := payload["model"]
	if !ok || json.Unmarshal(rawModel, &current) != nil || current != requestedModel {
		return body
	}
	real, err := json.Marshal(mapping.Real)
	if err != nil {
		return body
	}
	payload["model"] = real
	rewritten, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return rewritten
}

// reasoningEffortLevels is the ordered set of OpenAI-style reasoning effort
// values understood by the gateway. A client-requested effort beyond a
// channel's declared max is downgraded to the max at forward time.
var reasoningEffortLevels = []string{
	"none", "minimal", "low", "medium", "high", "xhigh", "max",
}

// downgradeReasoningEffort rewrites a request body's reasoning_effort when it
// exceeds the channel's declared maximum. Returns the rewritten body and a
// "from→to" note, or (nil, "") when no downgrade applies (missing field,
// unknown values, or already at/below the max).
func downgradeReasoningEffort(body []byte, maxEffort string) ([]byte, string) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, ""
	}
	raw, ok := payload["reasoning_effort"]
	if !ok {
		return nil, ""
	}
	var effort string
	if err := json.Unmarshal(raw, &effort); err != nil {
		return nil, ""
	}
	effort = strings.ToLower(strings.TrimSpace(effort))
	maxEffort = strings.ToLower(strings.TrimSpace(maxEffort))
	if effort == "" || maxEffort == "" || effort == maxEffort {
		return nil, ""
	}
	effortIndex, maxIndex := -1, -1
	for i, level := range reasoningEffortLevels {
		if level == effort {
			effortIndex = i
		}
		if level == maxEffort {
			maxIndex = i
		}
	}
	// Unknown values pass through untouched (never guess); already-at/below max
	// needs no rewrite.
	if effortIndex < 0 || maxIndex < 0 || effortIndex <= maxIndex {
		return nil, ""
	}
	payload["reasoning_effort"] = json.RawMessage(fmt.Sprintf("%q", maxEffort))
	rewritten, err := json.Marshal(payload)
	if err != nil {
		return nil, ""
	}
	return rewritten, effort + "→" + maxEffort
}

// injectSystemPrompt prepends a system message to an OpenAI chat/completions
// body. Non-JSON or non-chat bodies are returned unchanged.
func injectSystemPrompt(body []byte, prompt string) []byte {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return body
	}
	messages, ok := payload["messages"].([]any)
	if !ok {
		return body
	}
	system := map[string]any{"role": "system", "content": prompt}
	// Skip if an identical system message is already first.
	if len(messages) > 0 {
		if first, ok := messages[0].(map[string]any); ok {
			if role, _ := first["role"].(string); role == "system" {
				if existing, _ := first["content"].(string); existing == prompt {
					return body
				}
			}
		}
	}
	updated := make([]any, 0, len(messages)+1)
	updated = append(updated, system)
	updated = append(updated, messages...)
	payload["messages"] = updated
	rewritten, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return rewritten
}

// forbiddenOverrideHeaders cannot be overridden by channel config (transport
// level or authentication-critical).
var forbiddenOverrideHeaders = map[string]struct{}{
	"host":                {},
	"content-length":      {},
	"transfer-encoding":   {},
	"connection":          {},
	"proxy-authorization": {},
	"proxy-connection":    {},
	"te":                  {},
	"trailer":             {},
	"upgrade":             {},
}

// mergeHeaderOverrides applies a channel's header_override JSON onto headers.
// Values replace existing ones; hop-by-hop transport names are ignored.
func mergeHeaderOverrides(headers http.Header, raw string) error {
	overrides, err := parseHeaderOverrides(raw)
	if err != nil {
		return err
	}
	for name, value := range overrides {
		key := http.CanonicalHeaderKey(strings.TrimSpace(name))
		if _, blocked := forbiddenOverrideHeaders[strings.ToLower(key)]; blocked {
			continue
		}
		headers.Set(key, value)
	}
	return nil
}

// ValidateHeaderOverrides validates the persisted channel header map using
// the same rules as the forwarding path.
func ValidateHeaderOverrides(raw string) error {
	_, err := parseHeaderOverrides(raw)
	return err
}

func parseHeaderOverrides(raw string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var overrides map[string]string
	if err := json.Unmarshal([]byte(raw), &overrides); err != nil {
		return nil, fmt.Errorf("invalid header_override JSON: %w", err)
	}
	if overrides == nil {
		return nil, fmt.Errorf("header_override must be a JSON object")
	}
	for name, value := range overrides {
		if !validHeaderName(strings.TrimSpace(name)) {
			return nil, fmt.Errorf("invalid header_override name %q", name)
		}
		if !validHeaderValue(value) {
			return nil, fmt.Errorf("invalid header_override value for %q", name)
		}
	}
	return overrides, nil
}

func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for index := 0; index < len(name); index++ {
		char := name[index]
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || strings.ContainsRune("!#$%&'*+-.^_`|~", rune(char)) {
			continue
		}
		return false
	}
	return true
}

func validHeaderValue(value string) bool {
	for index := 0; index < len(value); index++ {
		char := value[index]
		if char == '\t' || char >= 0x20 && char != 0x7f {
			continue
		}
		return false
	}
	return true
}

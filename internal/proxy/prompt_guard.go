package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/lan/meta-gateway/internal/store"
)

// GuardHit describes the first rule that matched a request body.
type GuardHit struct {
	Rule    string  `json:"rule"`
	Action  string  `json:"action"`
	Pattern string  `json:"pattern"`
	Message string  `json:"message,omitempty"`
	Exclude []int64 `json:"exclude,omitempty"`
	Masked  bool    `json:"masked,omitempty"`
}

// ApplyPromptGuards walks every string value in a JSON body and applies the
// enabled guard rules. Returns the (possibly rewritten) body, a hit when any
// rule matched, or an error for an invalid rule pattern (rules are validated
// at save time, so this is defensive). Non-JSON bodies pass through with no
// hit. A rule whose pattern is empty is skipped.
func ApplyPromptGuards(body []byte, rules []store.PromptGuardRule) ([]byte, *GuardHit, error) {
	if len(rules) == 0 {
		return body, nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		// Non-JSON (multipart, plain text) or malformed: do not block the
		// request, guards apply to structured chat bodies only.
		return body, nil, nil
	}
	masked, hit, err := walkGuardValues(doc, rules, 0)
	if err != nil {
		return body, nil, err
	}
	if hit == nil {
		return body, nil, nil
	}
	if !hit.Masked {
		return body, hit, nil
	}
	out, err := json.Marshal(masked)
	if err != nil {
		return body, hit, nil // body unchanged but the hit is real
	}
	return out, hit, nil
}

// walkGuardValues recursively visits string values. depth guards against
// pathological nesting (an attacker-controlled body must not blow the stack).
func walkGuardValues(node any, rules []store.PromptGuardRule, depth int) (any, *GuardHit, error) {
	if depth > 64 {
		return node, nil, nil
	}
	switch n := node.(type) {
	case map[string]any:
		var maskHit *GuardHit
		for key, value := range n {
			updated, hit, err := walkGuardValues(value, rules, depth+1)
			if err != nil {
				return nil, nil, err
			}
			if hit != nil {
				if !hit.Masked {
					return nil, hit, nil
				}
				n[key] = updated
				if maskHit == nil {
					maskHit = hit
				}
			}
		}
		return n, maskHit, nil
	case []any:
		var maskHit *GuardHit
		for index, value := range n {
			updated, hit, err := walkGuardValues(value, rules, depth+1)
			if err != nil {
				return nil, nil, err
			}
			if hit != nil {
				if !hit.Masked {
					return nil, hit, nil
				}
				n[index] = updated
				if maskHit == nil {
					maskHit = hit
				}
			}
		}
		return n, maskHit, nil
	case string:
		var maskHit *GuardHit
		for _, rule := range rules {
			if !rule.Enabled || strings.TrimSpace(rule.Pattern) == "" {
				continue
			}
			re, err := regexp.Compile(rule.Pattern)
			if err != nil {
				return nil, nil, fmt.Errorf("prompt guard rule %q: %w", rule.Name, err)
			}
			if !re.MatchString(n) {
				continue
			}
			hit := &GuardHit{Rule: rule.Name, Action: rule.Action, Pattern: rule.Pattern}
			switch rule.Action {
			case "reject":
				hit.Message = "request rejected by content policy (rule " + rule.Name + ")"
				return nil, hit, nil
			case "exclude":
				for _, part := range strings.Split(rule.ExcludeChannels, ",") {
					id, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
					if err == nil && id > 0 {
						hit.Exclude = append(hit.Exclude, id)
					}
				}
				return nil, hit, nil
			default: // mask
				replacement := rule.Replacement
				if replacement == "" {
					replacement = "[REDACTED]"
				}
				n = re.ReplaceAllString(n, replacement)
				hit.Masked = true
				if maskHit == nil {
					maskHit = hit
				}
			}
		}
		return n, maskHit, nil
	default:
		return node, nil, nil
	}
}

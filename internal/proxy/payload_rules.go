package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// PayloadRules is the channel-level request body rewrite engine. Each rule
// carries match conditions (model wildcard, protocol, header presence, JSON
// path conditions) and a chain of actions (set / delete / filter). Rules are
// applied in order; every matching rule's actions run, and a filter action
// short-circuits the request with a synthesized upstream-style error.
//
// JSON shape (channels.payload_rules):
//
//	[
//	  {
//	    "name": "cap max tokens",
//	    "match": {
//	      "model": "gpt-*",
//	      "protocol": "openai",
//	      "headers": {"x-tenant": "beta"},
//	      "payload": {"max_tokens": {"exists": true}, "stream": {"eq": true}}
//	    },
//	    "actions": [
//	      {"op": "set", "path": "max_tokens", "value": 8000},
//	      {"op": "delete", "path": "messages.0.tool_choice"}
//	    ]
//	  },
//	  {"name": "block images", "match": {"payload": {"messages.#.image_url": {"exists": true}}}, "actions": [{"op": "filter", "reason": "images blocked on this channel"}]}
//	]
type PayloadRule struct {
	Name    string          `json:"name"`
	Match   PayloadMatch    `json:"match"`
	Actions []PayloadAction `json:"actions"`
}

type PayloadMatch struct {
	Model    string                 `json:"model"`    // glob: * and ? wildcards; empty = any
	Protocol string                 `json:"protocol"` // "openai" | "anthropic"; empty = any
	Headers  map[string]string      `json:"header"`   // header name → required substring (case-insensitive); empty = any
	Payload  map[string]PayloadCond `json:"payload"`  // JSON path → condition
}

// PayloadCond is a JSON-path condition. Exactly one of Exists / Eq may be set.
type PayloadCond struct {
	Exists *bool  `json:"exists,omitempty"` // require path present (true) or absent (false)
	Eq     *Value `json:"eq,omitempty"`     // require path equals this value
	Neq    *Value `json:"neq,omitempty"`    // require path differs from this value
}

// Value is a JSON literal for comparisons (string, number, bool, null).
type Value struct {
	Str  *string  `json:"str,omitempty"`
	Num  *float64 `json:"num,omitempty"`
	Bool *bool    `json:"bool,omitempty"`
	Null bool     `json:"null,omitempty"`
}

type PayloadAction struct {
	Op     string `json:"op"` // "set" | "delete" | "filter"
	Path   string `json:"path"`
	Value  *Value `json:"value"`
	Reason string `json:"reason"`
}

// ApplyPayloadRules runs the rule chain over a request body.
// Returns the (possibly rewritten) body, a filter result (non-nil when a
// filter action fired), and any hard error (malformed rules → passthrough
// with a log, never a request failure).
func ApplyPayloadRules(body []byte, rulesJSON, model, protocol string, headers map[string]string) ([]byte, *PayloadFilter, error) {
	trimmed := strings.TrimSpace(rulesJSON)
	if trimmed == "" || trimmed == "[]" {
		return body, nil, nil
	}
	var rules []PayloadRule
	if err := json.Unmarshal([]byte(trimmed), &rules); err != nil {
		return body, nil, fmt.Errorf("payload rules parse: %w", err)
	}
	if len(rules) == 0 {
		return body, nil, nil
	}
	// Lazily decode the body only when a rule may need it (cheap skip when
	// every match is model/protocol only).
	var doc map[string]any
	var dirty bool
	decodeDoc := func() (map[string]any, error) {
		if doc != nil {
			return doc, nil
		}
		dec := json.NewDecoder(bytes.NewReader(body))
		dec.UseNumber()
		if err := dec.Decode(&doc); err != nil {
			return nil, fmt.Errorf("payload rules body decode: %w", err)
		}
		return doc, nil
	}
	for _, rule := range rules {
		if !matchPayloadRule(rule.Match, model, protocol, headers, decodeDoc) {
			continue
		}
		for _, action := range rule.Actions {
			switch action.Op {
			case "filter":
				reason := action.Reason
				if reason == "" {
					reason = "request filtered by channel payload rule"
				}
				return body, &PayloadFilter{Rule: rule.Name, Reason: reason}, nil
			case "set", "delete":
				d, err := decodeDoc()
				if err != nil {
					return body, nil, err
				}
				if action.Op == "set" {
					val, err := action.Value.ToAny()
					if err != nil {
						return body, nil, fmt.Errorf("payload rule %q set value: %w", rule.Name, err)
					}
					if err := jsonPathSet(d, action.Path, val); err != nil {
						return body, nil, fmt.Errorf("payload rule %q set %s: %w", rule.Name, action.Path, err)
					}
				} else {
					if err := jsonPathDelete(d, action.Path); err != nil {
						return body, nil, fmt.Errorf("payload rule %q delete %s: %w", rule.Name, action.Path, err)
					}
				}
				dirty = true
			}
		}
	}
	// Only re-encode when a rule actually mutated the document; a decoded
	// but non-matching run must hand back the original bytes untouched
	// (Go map re-encoding would only shuffle key order).
	if !dirty {
		return body, nil, nil
	}
	encoded, err := json.Marshal(doc)
	if err != nil {
		return body, nil, fmt.Errorf("payload rules re-encode: %w", err)
	}
	return encoded, nil, nil
}

// PayloadFilter reports a filter action firing; the proxy synthesizes an
// upstream-style error response for it.
type PayloadFilter struct {
	Rule   string `json:"rule,omitempty"`
	Reason string `json:"reason"`
}

func (f *PayloadFilter) Error() string {
	return f.Reason
}

func matchPayloadRule(m PayloadMatch, model, protocol string, headers map[string]string, decodeDoc func() (map[string]any, error)) bool {
	if m.Model != "" && !globMatch(m.Model, model) {
		return false
	}
	if m.Protocol != "" && !strings.EqualFold(m.Protocol, protocol) {
		return false
	}
	// Header conditions: client headers arrive canonicalized (X-Meta-Client);
	// match case-insensitively on both name and value.
	for name, want := range m.Headers {
		got := ""
		for key, value := range headers {
			if strings.EqualFold(key, name) {
				got = value
				break
			}
		}
		if !strings.Contains(strings.ToLower(got), strings.ToLower(want)) {
			return false
		}
	}
	if len(m.Payload) > 0 {
		doc, err := decodeDoc()
		if err != nil {
			return false
		}
		for path, cond := range m.Payload {
			if !matchPayloadCond(doc, path, cond) {
				return false
			}
		}
	}
	return true
}

func matchPayloadCond(doc map[string]any, path string, cond PayloadCond) bool {
	val, found := jsonPathGet(doc, path)
	if cond.Exists != nil {
		if *cond.Exists != found {
			return false
		}
		if !found {
			return true
		}
	}
	if cond.Eq != nil {
		want, err := cond.Eq.ToAny()
		if err != nil || !found || !valuesEqual(val, want) {
			return false
		}
	}
	if cond.Neq != nil {
		want, err := cond.Neq.ToAny()
		if err != nil || (found && valuesEqual(val, want)) {
			return false
		}
	}
	return true
}

func valuesEqual(a, b any) bool {
	switch av := a.(type) {
	case json.Number:
		bf, err := b.(json.Number).Float64()
		if err != nil {
			return false
		}
		af, _ := av.Float64()
		return af == bf
	case float64:
		bf, ok := b.(float64)
		return ok && av == bf
	default:
		return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
	}
}

// ToAny materializes a Value into a JSON-comparable Go value.
func (v *Value) ToAny() (any, error) {
	if v == nil {
		return nil, fmt.Errorf("empty value")
	}
	switch {
	case v.Str != nil:
		return *v.Str, nil
	case v.Num != nil:
		return json.Number(strconv.FormatFloat(*v.Num, 'g', -1, 64)), nil
	case v.Bool != nil:
		return *v.Bool, nil
	case v.Null:
		return nil, nil
	}
	return nil, fmt.Errorf("empty value")
}

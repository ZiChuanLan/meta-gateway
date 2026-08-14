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
	Model    string            `json:"model"`    // glob: * and ? wildcards; empty = any
	Protocol string            `json:"protocol"` // "openai" | "anthropic"; empty = any
	Headers  map[string]string `json:"header"`   // header name → required substring (case-insensitive); empty = any
	Payload  map[string]PayloadCond `json:"payload"` // JSON path → condition
}

// PayloadCond is a JSON-path condition. Exactly one of Exists / Eq may be set.
type PayloadCond struct {
	Exists *bool       `json:"exists,omitempty"` // require path present (true) or absent (false)
	Eq     *Value      `json:"eq,omitempty"`     // require path equals this value
	Neq    *Value      `json:"neq,omitempty"`    // require path differs from this value
}

// Value is a JSON literal for comparisons (string, number, bool, null).
type Value struct {
	Str   *string `json:"str,omitempty"`
	Num   *float64 `json:"num,omitempty"`
	Bool  *bool    `json:"bool,omitempty"`
	Null  bool     `json:"null,omitempty"`
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

// globMatch matches * (any run) and ? (single char) against s.
func globMatch(pattern, s string) bool {
	if pattern == "" {
		return true
	}
	// Iterative wildcard match (no backtracking blowup).
	var p, si, star, mark int
	star = -1
	for si < len(s) {
		if p < len(pattern) && (pattern[p] == '?' || pattern[p] == s[si]) {
			p++
			si++
		} else if p < len(pattern) && pattern[p] == '*' {
			star = p
			mark = si
			p++
		} else if star >= 0 {
			p = star + 1
			mark++
			si = mark
		} else {
			return false
		}
	}
	for p < len(pattern) && pattern[p] == '*' {
		p++
	}
	return p == len(pattern)
}

// jsonPathGet resolves a dot/bracket path ("messages.0.content", "a[0].b",
// "messages.#.image_url" — # = any array element) and reports whether the
// node exists. For # paths the first matching element's value is returned.
func jsonPathGet(doc map[string]any, path string) (any, bool) {
	return jsonPathGetNode(doc, splitJSONPath(path))
}

func jsonPathGetNode(node any, parts []string) (any, bool) {
	if len(parts) == 0 {
		return node, true
	}
	head := parts[0]
	switch n := node.(type) {
	case map[string]any:
		v, ok := n[head]
		if !ok {
			return nil, false
		}
		return jsonPathGetNode(v, parts[1:])
	case []any:
		if head == "#" {
			for _, item := range n {
				if v, ok := jsonPathGetNode(item, parts[1:]); ok {
					return v, true
				}
			}
			return nil, false
		}
		idx, err := strconv.Atoi(head)
		if err != nil || idx < 0 || idx >= len(n) {
			return nil, false
		}
		return jsonPathGetNode(n[idx], parts[1:])
	default:
		return nil, false
	}
}

func jsonPathSet(doc map[string]any, path string, value any) error {
	_, err := jsonPathSetNode(doc, splitJSONPath(path), value)
	return err
}

func jsonPathSetNode(node any, parts []string, value any) (any, error) {
	if len(parts) == 0 {
		return value, nil
	}
	head := parts[0]
	switch n := node.(type) {
	case map[string]any:
		child, exists := n[head]
		if !exists {
			child = newContainerFor(nextPart(parts, 0))
		}
		updated, err := jsonPathSetNode(child, parts[1:], value)
		if err != nil {
			return nil, err
		}
		n[head] = updated
		return n, nil
	case []any:
		idx, err := strconv.Atoi(head)
		if err != nil {
			return nil, fmt.Errorf("non-numeric index %q in array", head)
		}
		for len(n) <= idx {
			n = append(n, newContainerFor(nextPart(parts, 0)))
		}
		updated, err := jsonPathSetNode(n[idx], parts[1:], value)
		if err != nil {
			return nil, err
		}
		n[idx] = updated
		return n, nil
	default:
		return nil, fmt.Errorf("cannot descend through %T at %q", node, head)
	}
}

func jsonPathDelete(doc map[string]any, path string) error {
	_, err := jsonPathDeleteNode(doc, splitJSONPath(path))
	return err
}

func jsonPathDeleteNode(node any, parts []string) (any, error) {
	if len(parts) == 0 {
		return node, nil
	}
	head := parts[0]
	switch n := node.(type) {
	case map[string]any:
		child, ok := n[head]
		if !ok {
			return n, nil // absent is a no-op
		}
		if len(parts) == 1 {
			delete(n, head)
			return n, nil
		}
		updated, err := jsonPathDeleteNode(child, parts[1:])
		if err != nil {
			return nil, err
		}
		n[head] = updated
		return n, nil
	case []any:
		idx, err := strconv.Atoi(head)
		if err != nil || idx < 0 || idx >= len(n) {
			return n, nil
		}
		if len(parts) == 1 {
			return append(n[:idx], n[idx+1:]...), nil
		}
		updated, err := jsonPathDeleteNode(n[idx], parts[1:])
		if err != nil {
			return nil, err
		}
		n[idx] = updated
		return n, nil
	default:
		return n, nil
	}
}

func newContainerFor(nextPart string) any {
	if _, err := strconv.Atoi(nextPart); err == nil {
		return []any{}
	}
	return map[string]any{}
}

func nextPart(parts []string, i int) string {
	if i+1 < len(parts) {
		return parts[i+1]
	}
	return ""
}

// splitJSONPath splits "a.b[0].c" into ["a","b","0","c"].
func splitJSONPath(path string) []string {
	var out []string
	var cur strings.Builder
	for i := 0; i < len(path); i++ {
		switch path[i] {
		case '.':
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		case '[':
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		case ']':
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteByte(path[i])
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

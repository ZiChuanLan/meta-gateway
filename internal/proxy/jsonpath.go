package proxy

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// JSON-path helpers shared by the channel-level body rewriting features:
// payload_rules (match conditions + set/delete actions) and upstream_map
// (request_map / response_map field mappings). They live in their own file
// because both engines must agree byte-for-byte on what a path means —
// "messages.0.content" and "messages.#.image_url" must resolve identically
// for a rule's match and for its action.

// ValidateJSONPath reports whether a path is well formed: balanced brackets, no
// empty segment ("a..b", a leading/trailing dot, or "a.[0]") and no stray
// bracket inside a segment. It exists so the admin API can reject a typo the
// engine would otherwise treat as an absent node — silently doing nothing is the
// worst possible response to an operator's mapping.
func ValidateJSONPath(path string) error {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return errors.New("path is required")
	}
	depth := 0
	segment := ""
	flush := func() error {
		if segment == "" {
			return fmt.Errorf("path %q has an empty segment", trimmed)
		}
		segment = ""
		return nil
	}
	for i := 0; i < len(trimmed); i++ {
		switch c := trimmed[i]; c {
		case '.':
			if depth > 0 {
				segment += string(c)
				continue
			}
			if err := flush(); err != nil {
				return err
			}
		case '[':
			if depth > 0 {
				return fmt.Errorf("path %q nests brackets", trimmed)
			}
			if err := flush(); err != nil {
				return err
			}
			depth++
		case ']':
			if depth == 0 {
				return fmt.Errorf("path %q closes a bracket that was never opened", trimmed)
			}
			depth--
		case ' ':
			if depth == 0 {
				return fmt.Errorf("path %q contains a space", trimmed)
			}
			segment += string(c)
		default:
			segment += string(c)
		}
	}
	if depth != 0 {
		return fmt.Errorf("path %q has an unclosed bracket", trimmed)
	}
	// The trailing segment is only required when the path does not end with a
	// bracket index ("a[0]" has no trailing dot segment).
	if segment == "" && !strings.HasSuffix(trimmed, "]") {
		return fmt.Errorf("path %q ends with an empty segment", trimmed)
	}
	return nil
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

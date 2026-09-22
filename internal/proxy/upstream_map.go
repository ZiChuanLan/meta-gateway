package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/lan/meta-gateway/internal/adapters"
)

// UpstreamMap is the channel-level endpoint/field mapping engine. It exists
// because two very different upstream families cannot be reached through the
// OpenAI-shaped assumptions baked into the forward adapters:
//
//  1. Providers that do NOT serve under a `/v1` root. The passthrough adapter
//     joins base URL + "/v1/" + path, so `https://open.bigmodel.cn/api/paas/v4`
//     turns into the non-existent `/api/paas/v4/v1/models`. PathOverride /
//     PathMap relocates the endpoint without touching the host.
//  2. Providers whose wire contract is not OpenAI chat at all (TypeSafe's
//     POST /v1/systemone takes {state, model, questions} and answers with
//     {answers:{...}}). RequestMap / ResponseMap move fields between the two
//     shapes, so such an upstream sits behind the ordinary relay path.
//
// The design deliberately reuses the payload_rules vocabulary: the same
// dot/bracket paths ("messages.0.content", "answers.#.choice"), the same
// wildcard matcher, the same literal Value shape. One set of path semantics for
// the whole console, not two.
//
// Scope of a map: each direction runs ONCE over the body that direction
// received. RequestMap sees the client's (post-rewrite) request body;
// ResponseMap sees the upstream's (post-adapter) response body. Mappings are
// therefore independent of each other and cannot source from another hop's
// output — a deliberate limit that keeps the relay path unbuffered and the
// ordering impossible to get wrong.
//
// Everything here fails open: a malformed map is ignored (with a log) and the
// request is forwarded untouched. A gateway must never drop a request because
// of its own rewriting layer.
type UpstreamMap struct {
	// PathOverride, when non-empty, replaces the OpenAI path outright
	// ("chat/completions" → "systemone").
	PathOverride string
	// PathMap redirects individual paths; a key may end in "*" to match a
	// prefix (the longest matching key wins). Values may contain "{path}" (the
	// remaining path segments, wildcard part included) and "{model}".
	PathMap map[string]string
	// RequestMap rewrites the outbound body; ResponseMap rewrites the inbound
	// body. Both use the same field-mapping language.
	RequestMap  []UpstreamFieldMap
	ResponseMap []UpstreamFieldMap

	// prefixMap is PathMap's wildcard entries, longest prefix first, so
	// resolution is deterministic and independent of Go map order.
	prefixMap []pathPrefix
}

type pathPrefix struct {
	prefix string
	target string
}

// UpstreamFieldMap moves one value inside a JSON body. Exactly one of the four
// forms is used, decided by which fields are set:
//
//	{"from": "p", "to": "q"}                   copy the node at p into q
//	{"from": "p", "to": "q", "move": true}     move it (copy, then delete p)
//	{"to": "q", "template": "…"}               build the value at q from text
//	{"to": "q", "value": {"num": 8}}           write a literal at q
//
// `to` defaults to `from` when omitted. A copy preserves the JSON type of the
// source (numbers stay numbers); a template always produces a string, which is
// what makes a value the upstream expects as text predictable. A template may
// reference any node of the same body ("{messages.0.content}") and may embed
// JSON literals verbatim, since only a path-shaped brace body is treated as a
// reference; `\{` and `\}` emit literal braces. An array or object referenced by
// a template renders as compact JSON, so structure survives the interpolation.
type UpstreamFieldMap struct {
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
	// Move deletes From after copying it into To (a literal rename).
	Move bool `json:"move,omitempty"`
	// Template renders its result from "{path}" references, e.g.
	// "[{\"role\":\"user\",\"content\":\"{messages.0.content}\"}]" to wrap a
	// user message into a state array. A path-shaped brace body is a reference;
	// embedded JSON literals pass through verbatim; \{ and \} emit literal braces.
	Template string `json:"template,omitempty"`
	// Value is a JSON literal (same shape payload_rules uses: str/num/bool/null).
	Value *Value `json:"value,omitempty"`
}

// ParseUpstreamMap decodes the channel columns into a usable engine. Malformed
// JSON yields an empty map (fail open, never an error): the columns are
// operator-authored and the admin API rejects what it can validate, so a bad
// row left over from a manual DB edit must not break routing.
func ParseUpstreamMap(pathOverride, pathMapJSON, requestMapJSON, responseMapJSON string) UpstreamMap {
	m := UpstreamMap{PathOverride: strings.TrimSpace(pathOverride)}
	if pm := decodePathMap(pathMapJSON); len(pm) > 0 {
		m.PathMap = pm
	}
	m.RequestMap = decodeFieldMaps(requestMapJSON)
	m.ResponseMap = decodeFieldMaps(responseMapJSON)
	m.prefixMap = buildPrefixMap(m.PathMap)
	return m
}

// Empty reports whether the channel carries no mapping at all.
func (m UpstreamMap) Empty() bool {
	return m.PathOverride == "" && len(m.PathMap) == 0 && len(m.RequestMap) == 0 && len(m.ResponseMap) == 0
}

func decodePathMap(raw string) map[string]string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "{}" {
		return nil
	}
	var out map[string]string
	if err := json.Unmarshal([]byte(trimmed), &out); err != nil {
		return nil
	}
	clean := make(map[string]string, len(out))
	for from, to := range out {
		key := strings.Trim(strings.TrimSpace(from), "/")
		target := strings.TrimSpace(to)
		if key == "" || target == "" {
			continue
		}
		clean[key] = target
	}
	if len(clean) == 0 {
		return nil
	}
	return clean
}

func decodeFieldMaps(raw string) []UpstreamFieldMap {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "[]" {
		return nil
	}
	var out []UpstreamFieldMap
	if err := json.Unmarshal([]byte(trimmed), &out); err != nil {
		return nil
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func buildPrefixMap(pathMap map[string]string) []pathPrefix {
	var prefixes []pathPrefix
	for from, to := range pathMap {
		if strings.HasSuffix(from, "*") {
			prefixes = append(prefixes, pathPrefix{prefix: strings.TrimSuffix(from, "*"), target: to})
		}
	}
	// Longest prefix wins, and equal lengths resolve in a stable order so two
	// wildcards of the same size never swap behaviour between processes.
	sort.Slice(prefixes, func(i, j int) bool {
		if len(prefixes[i].prefix) != len(prefixes[j].prefix) {
			return len(prefixes[i].prefix) > len(prefixes[j].prefix)
		}
		return prefixes[i].prefix < prefixes[j].prefix
	})
	return prefixes
}

// ResolvePath maps an OpenAI-relative path ("chat/completions") to the path this
// channel actually serves. model is available to "{model}" substitutions.
func (m UpstreamMap) ResolvePath(path, model string) string {
	if override := NormalizeEndpointPath(m.PathOverride); override != "" {
		return substitutePath(override, "", model)
	}
	normalized := strings.Trim(strings.TrimSpace(path), "/")
	if normalized == "" {
		return path
	}
	if target, ok := m.PathMap[normalized]; ok {
		return substitutePath(target, "", model)
	}
	for _, entry := range m.prefixMap {
		if !strings.HasPrefix(normalized, entry.prefix) {
			continue
		}
		remainder := strings.TrimPrefix(normalized, entry.prefix)
		return substitutePath(entry.target, remainder, model)
	}
	return path
}

// NormalizeEndpointPath canonicalizes an endpoint-path override. The two forms
// exist because operators write the same intent two ways, and guessing wrong
// silently sends requests to a 404:
//
//	""              no override
//	"systemone"      → "/v1/systemone"   a bare name keeps the conventional /v1 slot
//	"models/{model}" → "/v1/models/{model}"  a placeholder does not count as a segment
//	"/api/v3/x"      → "/api/v3/x"       a leading slash is absolute: verbatim
//	"api/v3/x"       → "/api/v3/x"       multi-segment is treated as absolute too,
//	                                   since a nested path cannot be a slot guess
//
// The absolute form is what a provider serving outside its base root needs
// (e.g. base https://host + "/api/paas/v4/chat/completions").
func NormalizeEndpointPath(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "/") {
		return "/" + strings.Trim(trimmed, "/")
	}
	if len(pathSegments(trimmed)) <= 1 {
		return "/v1/" + strings.Trim(trimmed, "/")
	}
	return "/" + strings.Trim(trimmed, "/")
}

// pathSegments counts the non-placeholder segments of a path, so "models/{model}"
// is one segment (a bare-name override) while "api/v3/chat" is three (absolute).
func pathSegments(path string) []string {
	stripped := path
	for {
		start := strings.IndexByte(stripped, '{')
		if start < 0 {
			break
		}
		end := strings.IndexByte(stripped[start:], '}')
		if end < 0 {
			break
		}
		stripped = stripped[:start] + stripped[start+end+1:]
	}
	var segments []string
	for _, segment := range strings.Split(stripped, "/") {
		if strings.TrimSpace(segment) != "" {
			segments = append(segments, segment)
		}
	}
	return segments
}

// substitutePath expands the {path} / {model} placeholders of a target.
func substitutePath(target, remainder, model string) string {
	out := strings.ReplaceAll(target, "{path}", strings.Trim(remainder, "/"))
	out = strings.ReplaceAll(out, "{model}", model)
	return strings.TrimSpace(out)
}

// MapRequest applies the request field maps to an outbound body. changed reports
// whether the body was rewritten; err is advisory and non-fatal.
func (m UpstreamMap) MapRequest(body []byte) ([]byte, bool, error) {
	return applyFieldMaps(body, m.RequestMap)
}

// MapResponse applies the response field maps to an inbound body.
func (m UpstreamMap) MapResponse(body []byte) ([]byte, bool, error) {
	return applyFieldMaps(body, m.ResponseMap)
}

// ReshapeResponse folds the channel's response mapping into the adapter's own
// response transform, in the only order that works for both:
//
//  1. adapter.TransformResponse   upstream shape → OpenAI shape, so a map's
//     paths describe the client-facing document
//  2. MapResponse                 field moves / writes / unwrapping
//
// It is the identity function for a channel with no response maps.
func (m UpstreamMap) ReshapeResponse(raw []byte, path string, adapter adapters.ForwardAdapter) ([]byte, error) {
	transformed, err := adapter.TransformResponse(path, raw)
	if err != nil {
		return nil, err
	}
	if len(m.ResponseMap) == 0 {
		return transformed, nil
	}
	mapped, _, mapErr := m.MapResponse(transformed)
	if mapErr != nil {
		// Advisory: a mapping typo must not fail the relay call.
		log.Printf("proxy: response map path=%s: %v", path, mapErr)
	}
	return mapped, nil
}

// Raw-frame paths ("[chat/completions]") are rejected by the validator with a
// pointer to the supported alternative, so an operator who reaches for the
// whole-body idea is told what to write instead.
func rejectFramedPath(column, path string) error {
	if _, framed := SplitFramedPath(path); framed {
		return fmt.Errorf("%s: bracket paths (\"[...]\") are not supported; map individual fields instead, e.g. {\"from\":\"choices.0.message.content\",\"to\":\"state\"}", column)
	}
	return nil
}

// frameMarker spells the bracket-path marker.
func frameMarker(path string) string {
	return "[" + strings.Trim(strings.TrimSpace(path), "/") + "]"
}

// SplitFramedPath reports whether a path is a bracket path and returns the inner
// path it stands for. Only the validator uses it today (the runtime rejects the
// form), and it lives here so the spelling stays in one place if the form is
// ever implemented.
func SplitFramedPath(path string) (string, bool) {
	trimmed := strings.TrimSpace(path)
	if len(trimmed) < 3 || !strings.HasPrefix(trimmed, "[") || !strings.HasSuffix(trimmed, "]") {
		return "", false
	}
	inner := strings.Trim(strings.TrimSpace(trimmed[1:len(trimmed)-1]), "/")
	if inner == "" {
		return "", false
	}
	return inner, true
}

// applyFieldMaps runs one field-map chain over a JSON body. Non-JSON bodies pass
// through untouched (there is nothing to reshape).
func applyFieldMaps(body []byte, maps []UpstreamFieldMap) ([]byte, bool, error) {
	if len(maps) == 0 || len(body) == 0 {
		return body, false, nil
	}
	doc, err := decodeMapDoc(body)
	if err != nil {
		return body, false, nil
	}
	var warnings []string
	changed := false
	for _, entry := range maps {
		wrote, err := applyFieldMap(doc, entry)
		if err != nil {
			warnings = append(warnings, err.Error())
			continue
		}
		if wrote {
			changed = true
		}
	}
	if !changed {
		return body, false, joinWarnings(warnings)
	}
	encoded, err := json.Marshal(doc)
	if err != nil {
		return body, false, fmt.Errorf("upstream map re-encode: %w", err)
	}
	return encoded, true, joinWarnings(warnings)
}

func joinWarnings(warnings []string) error {
	if len(warnings) == 0 {
		return nil
	}
	return fmt.Errorf("%s", strings.Join(warnings, "; "))
}

func applyFieldMap(doc map[string]any, entry UpstreamFieldMap) (bool, error) {
	from := strings.TrimSpace(entry.From)
	to := strings.TrimSpace(entry.To)
	if to == "" {
		to = from
	}
	switch {
	case from != "" && entry.Value != nil:
		return false, fmt.Errorf("upstream map %q: use either from or value, not both", to)
	case from != "" && entry.Template != "":
		return false, fmt.Errorf("upstream map %q: use either from or template, not both", to)
	case entry.Value != nil && entry.Template != "":
		return false, fmt.Errorf("upstream map %q: use either value or template, not both", to)
	case entry.Template != "":
		// Template-only: build the destination from text. This is the form that
		// turns a typed upstream answer into a plain string field.
		rendered, ok := renderTemplate(entry.Template, doc)
		if !ok {
			return false, fmt.Errorf("upstream map %q: template references a missing path", to)
		}
		if err := jsonPathSet(doc, to, rendered); err != nil {
			return false, fmt.Errorf("upstream map %q: %w", to, err)
		}
		return true, nil
	case from != "":
		value, found := jsonPathGet(doc, from)
		if !found {
			// Absent source: leave the destination alone rather than writing a
			// null the upstream may reject. Mapping optional fields (tools,
			// temperature) depends on this being a silent no-op.
			return false, nil
		}
		if err := jsonPathSet(doc, to, value); err != nil {
			return false, fmt.Errorf("upstream map %q: %w", to, err)
		}
		if entry.Move && from != to {
			if err := jsonPathDelete(doc, from); err != nil {
				return true, fmt.Errorf("upstream map %q: move delete %s: %w", to, from, err)
			}
		}
		return true, nil
	case entry.Value != nil:
		value, err := entry.Value.ToAny()
		if err != nil {
			return false, fmt.Errorf("upstream map %q: %w", to, err)
		}
		if err := jsonPathSet(doc, to, value); err != nil {
			return false, fmt.Errorf("upstream map %q: %w", to, err)
		}
		return true, nil
	default:
		return false, fmt.Errorf("upstream map: entry %q has neither from nor value", to)
	}
}

// templateToken is one piece of a parsed template: either literal text or a
// reference to a node of the same body.
type templateToken struct {
	literal string
	ref     string
}

// scanTemplate splits a template into literal runs and path references.
//
// The rules exist so that a template can embed a JSON object literal without
// escaping every brace, which is the main use of the feature (wrapping a client
// message into an upstream's own envelope):
//
//	"{path}"           a reference, substituted with the node at path
//	"{\"role\":\"user\"}" literal text — the brace body is not path-shaped
//	"\{"  "\}"  "\\"      escapes for a literal brace / backslash
//	"{a..b}"           an ERROR: path-shaped but not a valid path, so a typo
//	                    cannot silently turn into output text
//
// Only a path-shaped brace body is treated as a reference; anything containing
// whitespace, quotes, commas, colons or braces is literal. That is what makes
// embedded JSON work without an escaping rule per brace pair.
func scanTemplate(tpl string) ([]templateToken, error) {
	var tokens []templateToken
	var literal strings.Builder
	flush := func() {
		if literal.Len() > 0 {
			tokens = append(tokens, templateToken{literal: literal.String()})
			literal.Reset()
		}
	}
	for i := 0; i < len(tpl); {
		switch {
		case tpl[i] == '\\' && i+1 < len(tpl):
			switch tpl[i+1] {
			case '{', '}', '\\':
				literal.WriteByte(tpl[i+1])
				i += 2
			default:
				return nil, fmt.Errorf("template has an invalid escape \\%c (only \\{, \\} and \\\\ are defined)", tpl[i+1])
			}
		case tpl[i] == '{':
			end := strings.IndexByte(tpl[i+1:], '}')
			if end < 0 {
				if IsTemplatePathShape(tpl[i+1:]) {
					return nil, fmt.Errorf("template has an unclosed reference starting at %q", tpl[i:])
				}
				literal.WriteByte('{')
				i++
				continue
			}
			inner := tpl[i+1 : i+1+end]
			if !IsTemplatePathShape(inner) {
				// A JSON object literal: not a reference, emit the brace itself and
				// keep scanning inside it.
				literal.WriteByte('{')
				i++
				continue
			}
			if err := ValidateJSONPath(inner); err != nil {
				return nil, fmt.Errorf("template reference %q: %w", inner, err)
			}
			flush()
			tokens = append(tokens, templateToken{ref: strings.TrimSpace(inner)})
			i += end + 2
		default:
			literal.WriteByte(tpl[i])
			i++
		}
	}
	flush()
	return tokens, nil
}

// IsTemplatePathShape reports whether a brace body could be a path reference.
// A body containing whitespace, quotes, commas, colons or braces is JSON (or
// prose) rather than a path, so it stays literal text.
func IsTemplatePathShape(body string) bool {
	if body == "" {
		return false
	}
	for i := 0; i < len(body); i++ {
		switch body[i] {
		case ' ', '\t', '\r', '\n', '"', '\'', ',', ':', '{', '}':
			return false
		}
	}
	return true
}

// TemplateReferences lists the references a template will resolve. It is the
// admin API's validation entry point.
func TemplateReferences(tpl string) ([]string, error) {
	tokens, err := scanTemplate(tpl)
	if err != nil {
		return nil, err
	}
	var refs []string
	for _, token := range tokens {
		if token.ref != "" {
			refs = append(refs, token.ref)
		}
	}
	return refs, nil
}

// renderTemplate replays a template, replacing every reference with the string
// form of the node at that path. ok is false when a reference is missing, so a
// typo surfaces in the log instead of silently writing a half-built value.
func renderTemplate(tpl string, doc map[string]any) (string, bool) {
	tokens, err := scanTemplate(tpl)
	if err != nil {
		return "", false
	}
	var out strings.Builder
	for _, token := range tokens {
		if token.ref == "" {
			out.WriteString(token.literal)
			continue
		}
		value, found := jsonPathGet(doc, token.ref)
		if !found {
			return "", false
		}
		out.WriteString(stringifyTemplateValue(value))
	}
	return out.String(), true
}

// stringifyTemplateValue renders a JSON node for text interpolation: strings
// verbatim, containers and scalars as compact JSON so an array or object can be
// inlined into a template without losing structure.
func stringifyTemplateValue(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case json.Number:
		return v.String()
	case bool:
		if v {
			return "true"
		}
		return "false"
	default:
		encoded, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(encoded)
	}
}

func decodeMapDoc(body []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	doc := map[string]any{}
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	return doc, nil
}

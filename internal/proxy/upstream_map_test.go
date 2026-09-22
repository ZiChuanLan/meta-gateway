package proxy

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lan/meta-gateway/internal/adapters"
)

func TestParseUpstreamMapEmptyForms(t *testing.T) {
	cases := []struct {
		name                                     string
		override, pathMap, requestMap, responseM string
		wantEmpty                                bool
	}{
		{name: "all blank", wantEmpty: true},
		{name: "canonical empties", pathMap: "{}", requestMap: "[]", responseM: "[]", wantEmpty: true},
		{name: "malformed json is ignored", pathMap: "{broken", requestMap: "not json", wantEmpty: true},
		{name: "blank keys dropped", pathMap: `{"":"models"}`, wantEmpty: true},
		{name: "override alone", override: "systemone", wantEmpty: false},
		{name: "path map alone", pathMap: `{"models":"models"}`, wantEmpty: false},
		{name: "request map alone", requestMap: `[{"from":"a","to":"b"}]`, wantEmpty: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := ParseUpstreamMap(tc.override, tc.pathMap, tc.requestMap, tc.responseM)
			if got := m.Empty(); got != tc.wantEmpty {
				t.Fatalf("Empty() = %v, want %v (%+v)", got, tc.wantEmpty, m)
			}
		})
	}
}

func TestResolvePathOverrideAndMap(t *testing.T) {
	cases := []struct {
		name     string
		m        UpstreamMap
		path     string
		model    string
		wantPath string
	}{
		{
			name:     "override replaces the endpoint outright",
			m:        ParseUpstreamMap("systemone", "", "", ""),
			path:     "chat/completions",
			wantPath: "/v1/systemone",
		},
		{
			name:     "override may embed {model}",
			m:        ParseUpstreamMap("models/{model}", "", "", ""),
			path:     "chat/completions",
			model:    "jev-latest",
			wantPath: "/v1/models/jev-latest",
		},
		{
			name:     "leading slash makes the override absolute",
			m:        ParseUpstreamMap("/api/v3/chat", "", "", ""),
			path:     "chat/completions",
			wantPath: "/api/v3/chat",
		},
		{
			name:     "multi-segment override is absolute",
			m:        ParseUpstreamMap("api/v3/chat", "", "", ""),
			path:     "chat/completions",
			wantPath: "/api/v3/chat",
		},
		{
			name:     "exact map key wins",
			m:        ParseUpstreamMap("", `{"chat/completions":"chat/completions"}`, "", ""),
			path:     "chat/completions",
			wantPath: "chat/completions",
		},
		{
			name:     "unmapped path passes through",
			m:        ParseUpstreamMap("", `{"models":"models"}`, "", ""),
			path:     "embeddings",
			wantPath: "embeddings",
		},
		{
			name:     "wildcard carries the remainder through {path}",
			m:        ParseUpstreamMap("", `{"files/*":"v2/files/{path}"}`, "", ""),
			path:     "files/abc/def",
			wantPath: "v2/files/abc/def",
		},
		{
			name:     "longest wildcard prefix wins",
			m:        ParseUpstreamMap("", `{"a/*":"shallow/{path}","a/b/*":"deep/{path}"}`, "", ""),
			path:     "a/b/c",
			wantPath: "deep/c",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.m.ResolvePath(tc.path, tc.model); got != tc.wantPath {
				t.Fatalf("ResolvePath(%q) = %q, want %q", tc.path, got, tc.wantPath)
			}
		})
	}
}

func TestMapRequestCopyMoveLiteralTemplate(t *testing.T) {
	m := ParseUpstreamMap("", "", `[
		{"from":"messages.0.content","to":"state"},
		{"from":"temperature","to":"temp","move":true},
		{"to":"model","value":{"str":"jev-latest"}},
		{"to":"questions","template":"[{\"role\":\"user\",\"content\":\"{state}\"}]"},
		{"from":"absent.field","to":"never.written"}
	]`, "")

	body := []byte(`{"model":"x","messages":[{"role":"user","content":"hello"}],"temperature":0.2}`)
	out, changed, err := m.MapRequest(body)
	if err != nil {
		t.Fatalf("MapRequest: %v", err)
	}
	if !changed {
		t.Fatal("body should have been rewritten")
	}
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["state"] != "hello" {
		t.Fatalf("state = %v", doc["state"])
	}
	if _, exists := doc["temperature"]; exists {
		t.Fatal("move must delete the source")
	}
	if doc["temp"] != 0.2 {
		t.Fatalf("temp = %v (number type must survive the copy)", doc["temp"])
	}
	if doc["model"] != "jev-latest" {
		t.Fatalf("model = %v", doc["model"])
	}
	if doc["questions"] != `[{"role":"user","content":"hello"}]` {
		t.Fatalf("questions = %v", doc["questions"])
	}
	// An absent source must not invent a destination: writing null would be a
	// silent wire change the upstream may reject.
	if _, exists := doc["never"]; exists {
		t.Fatal("absent source must not create the destination")
	}
}

func TestMapRequestNoMatchReturnsOriginalBytes(t *testing.T) {
	m := ParseUpstreamMap("", "", `[{"from":"absent","to":"also_absent"}]`, "")
	body := []byte(`{"b":2,"a":1}`)
	out, changed, err := m.MapRequest(body)
	if err != nil {
		t.Fatalf("MapRequest: %v", err)
	}
	if changed {
		t.Fatal("nothing matched, changed must be false")
	}
	if string(out) != string(body) {
		t.Fatalf("body must pass through byte-identical, got %s", out)
	}
}

func TestMapResponseTypedScriptExample(t *testing.T) {
	// The TypeSafe shape used in the console placeholder: answers.ask.choice must
	// land in the OpenAI content slot while the rest of the envelope is dropped.
	m := ParseUpstreamMap("", "", "", `[
		{"from":"always_present","to":"scratch"},
		{"to":"choices.0.message.content","template":"{answers.ask.noul}"},
		{"to":"choices.0.message.role","value":{"str":"assistant"}}
	]`)
	body := []byte(`{"model":"jev-1.13.0","always_present":true,"answers":{"ask":{"type":"noul","noul":0.95}}}`)
	out, changed, err := m.MapResponse(body)
	if err != nil {
		t.Fatalf("MapResponse: %v", err)
	}
	if !changed {
		t.Fatal("response should have been rewritten")
	}
	var doc struct {
		Choices []struct {
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Answers map[string]any `json:"answers"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Choices) != 1 {
		t.Fatalf("choices = %+v", doc.Choices)
	}
	if doc.Choices[0].Message.Role != "assistant" {
		t.Fatalf("role = %q", doc.Choices[0].Message.Role)
	}
	if doc.Choices[0].Message.Content != "0.95" {
		t.Fatalf("content = %q (a template always renders text)", doc.Choices[0].Message.Content)
	}
	// The source envelope is not deleted by a copy: maps are copies, not moves,
	// unless move is explicit.
	if _, exists := doc.Answers["ask"]; !exists {
		t.Fatal("copy must leave the source in place")
	}
}

func TestMapRequestTemplateMissingReferenceIsAdvisory(t *testing.T) {
	m := ParseUpstreamMap("", "", `[{"to":"state","template":"{messages.0.content}"}]`, "")
	out, changed, err := m.MapRequest([]byte(`{"model":"x"}`))
	if err == nil {
		t.Fatal("a missing template reference must be reported")
	}
	if changed {
		t.Fatal("nothing may be written when a reference is missing")
	}
	if string(out) != `{"model":"x"}` {
		t.Fatalf("body must pass through untouched, got %s", out)
	}
}

func TestMapRequestEscapedBracesAndContainerInlining(t *testing.T) {
	m := ParseUpstreamMap("", "", `[{"to":"state","template":"\\{literal\\} {messages}"}]`, "")
	out, _, err := m.MapRequest([]byte(`{"messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatalf("MapRequest: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	want := `{literal} [{"content":"hi","role":"user"}]`
	if doc["state"] != want {
		t.Fatalf("state = %v, want %v", doc["state"], want)
	}
}

// A template must be able to embed JSON literals without escaping every brace:
// that is how a client message gets wrapped into an upstream's own envelope.
func TestMapRequestTemplateEmbedsJSONLiteral(t *testing.T) {
	m := ParseUpstreamMap("", "", `[{"to":"state","template":"[{\"role\":\"user\",\"content\":\"{messages.0.content}\"}]"}]`, "")
	out, _, err := m.MapRequest([]byte(`{"messages":[{"role":"user","content":"hello there"}]}`))
	if err != nil {
		t.Fatalf("MapRequest: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	var parsed []map[string]string
	if err := json.Unmarshal([]byte(doc["state"].(string)), &parsed); err != nil {
		t.Fatalf("state is not valid JSON: %v (%v)", err, doc["state"])
	}
	if len(parsed) != 1 || parsed[0]["role"] != "user" || parsed[0]["content"] != "hello there" {
		t.Fatalf("parsed state = %+v", parsed)
	}
}

// Substituted values are inserted verbatim — they are NOT JSON-escaped. This is
// deliberate (the template author controls the surrounding syntax, and escaping
// would corrupt the common plain-text case), so the behaviour is pinned here
// rather than left to be rediscovered as a bug report: a value containing a
// double quote produces invalid JSON inside a JSON template, and the operator
// must map the raw node with `from`/`to` instead of interpolating it.
func TestTemplateSubstitutionIsNotEscaped(t *testing.T) {
	m := ParseUpstreamMap("", "", `[{"to":"state","template":"{\"c\":\"{messages.0.content}\"}"}]`, "")
	out, _, err := m.MapRequest([]byte(`{"messages":[{"role":"user","content":"he said \"hi\""}]}`))
	if err != nil {
		t.Fatalf("MapRequest: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	rendered, _ := doc["state"].(string)
	if !strings.Contains(rendered, `he said "hi"`) {
		t.Fatalf("rendered = %q (substitution must be verbatim)", rendered)
	}
	var inner map[string]string
	if err := json.Unmarshal([]byte(rendered), &inner); err == nil {
		t.Fatal("expected the unescaped quote to break the embedded JSON; if this now parses, escaping was added and the docs must change")
	}
}

func TestReshapeResponseOrderAndIdentity(t *testing.T) {
	adapter := adapters.OpenAIPassthroughAdapter{}

	// No response map: exactly the adapter's own transform.
	plain := ParseUpstreamMap("", "", "", "")
	raw := []byte(`{"choices":[{"message":{"content":"hi"}}]}`)
	out, err := plain.ReshapeResponse(raw, "chat/completions", adapter)
	if err != nil {
		t.Fatalf("ReshapeResponse: %v", err)
	}
	if string(out) != string(raw) {
		t.Fatalf("unmapped response must be untouched, got %s", out)
	}

	// With a response map: the map sees the ADAPTER-TRANSFORMED document, which
	// is the invariant that makes paths describe the client-facing contract.
	mapped := ParseUpstreamMap("", "", "", `[{"from":"choices.0.message.content","to":"echo"}]`)
	out, err = mapped.ReshapeResponse(raw, "chat/completions", adapter)
	if err != nil {
		t.Fatalf("ReshapeResponse: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["echo"] != "hi" {
		t.Fatalf("echo = %v", doc["echo"])
	}
}

func TestStringifyTemplateValue(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{in: "text", want: "text"},
		{in: json.Number("42"), want: "42"},
		{in: true, want: "true"},
		{in: false, want: "false"},
		{in: nil, want: ""},
		{in: map[string]any{"a": json.Number("1")}, want: `{"a":1}`},
		{in: []any{"x"}, want: `["x"]`},
	}
	for _, tc := range cases {
		if got := stringifyTemplateValue(tc.in); got != tc.want {
			t.Errorf("stringifyTemplateValue(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestValidateUpstreamMapAcceptsAndCanonicalizes(t *testing.T) {
	override, pathMap, requestMap, responseMap, err := ValidateUpstreamMap(
		"/systemone/",
		`{"chat/completions":"chat/completions","models":"models"}`,
		`[{"from":"messages.0.content","to":"state"},{"to":"model","value":{"str":"jev-latest"}},{"to":"q","template":"{state}"}]`,
		`[{"from":"answers.ask.noul","to":"choices.0.message.content","move":true}]`,
	)
	if err != nil {
		t.Fatalf("ValidateUpstreamMap: %v", err)
	}
	if override != "systemone" {
		t.Fatalf("override = %q (slashes must be trimmed)", override)
	}
	// Re-encoded with sorted keys so an unchanged mapping round-trips identically.
	if pathMap != `{"chat/completions":"chat/completions","models":"models"}` {
		t.Fatalf("pathMap = %s", pathMap)
	}
	if !strings.Contains(requestMap, `"from":"messages.0.content"`) {
		t.Fatalf("requestMap = %s", requestMap)
	}
	if !strings.Contains(responseMap, `"move":true`) {
		t.Fatalf("responseMap = %s", responseMap)
	}

	// Empty forms canonicalize to "".
	_, pathMap, requestMap, responseMap, err = ValidateUpstreamMap("", "{}", "[]", "  []")
	if err != nil {
		t.Fatalf("empty forms: %v", err)
	}
	if pathMap != "" || requestMap != "" || responseMap != "" {
		t.Fatalf("empties must canonicalize to blank: %q %q %q", pathMap, requestMap, responseMap)
	}
}

func TestValidateUpstreamMapRejectsTypos(t *testing.T) {
	cases := []struct {
		name                                       string
		override, pathMap, requestMap, responseMap string
	}{
		{name: "override with whitespace", override: "chat completions"},
		{name: "bracket path rejected in from", requestMap: `[{"from":"[chat/completions]","to":"state"}]`},
		{name: "bracket path rejected in to", requestMap: `[{"to":"[state]","value":{"str":"x"}}]`},
		{name: "path map not an object", pathMap: `["models"]`},
		{name: "path map empty key", pathMap: `{"":"models"}`},
		{name: "path map empty target", pathMap: `{"models":""}`},
		{name: "path map with query", pathMap: `{"models":"models?x=1"}`},
		{name: "field map not an array", requestMap: `{"from":"a"}`},
		{name: "entry without from or value", requestMap: `[{"to":"a"}]`},
		{name: "from and value together", requestMap: `[{"from":"a","to":"b","value":{"str":"x"}}]`},
		{name: "from and template together", requestMap: `[{"from":"a","to":"b","template":"{a}"}]`},
		{name: "value and template together", requestMap: `[{"to":"a","value":{"str":"x"},"template":"{b}"}]`},
		{name: "malformed from path", requestMap: `[{"from":"a..b","to":"c"}]`},
		{name: "unclosed bracket in path", requestMap: `[{"from":"a[0","to":"c"}]`},
		{name: "template reference is not a path", requestMap: `[{"to":"a","template":"{a..b}"}]`},
		{name: "template has an unclosed brace", requestMap: `[{"to":"a","template":"{a"}]`},
		{name: "template invalid escape", requestMap: `[{"to":"a","template":"\\n"}]`},
		{name: "empty value literal", requestMap: `[{"to":"a","value":{}}]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, _, _, err := ValidateUpstreamMap(tc.override, tc.pathMap, tc.requestMap, tc.responseMap); err == nil {
				t.Fatal("expected a validation error")
			}
		})
	}
}

func TestValidateJSONPath(t *testing.T) {
	valid := []string{"a", "a.b", "messages.0.content", "messages.#.image_url", "a[0].b", "#", "answers.ask.noul", "a[0][1]"}
	for _, path := range valid {
		if err := ValidateJSONPath(path); err != nil {
			t.Errorf("ValidateJSONPath(%q) = %v, want nil", path, err)
		}
	}
	invalid := []string{"", " ", "a..b", ".a", "a.", "a[0", "a]0[", "a b", "a[b[c]]", "a.[0]"}
	for _, path := range invalid {
		if err := ValidateJSONPath(path); err == nil {
			t.Errorf("ValidateJSONPath(%q) = nil, want an error", path)
		}
	}
}

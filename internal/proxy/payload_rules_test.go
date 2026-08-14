package proxy

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestGlobMatch(t *testing.T) {
	cases := []struct{ pattern, s string; want bool }{
		{"gpt-*", "gpt-4o", true},
		{"gpt-*", "claude-3", false},
		{"*", "anything", true},
		{"deepseek-?-flash", "deepseek-v-flash", true},
		{"deepseek-?-flash", "deepseek-xx-flash", false},
		{"a*b*c", "aXbYc", true},
		{"a*b*c", "aXbY", false},
		{"", "anything", true}, // empty pattern handled by caller
	}
	for _, c := range cases {
		if got := globMatch(c.pattern, c.s); got != c.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", c.pattern, c.s, got, c.want)
		}
	}
}

func TestJSONPathGetSetDelete(t *testing.T) {
	body := `{"model":"gpt-4o","max_tokens":100,"messages":[{"role":"user","content":"hi","tool_choice":{"type":"auto"}}],"nested":{"a":{"b":1}}}`
	var doc map[string]any
	dec := json.NewDecoder(bytes.NewReader([]byte(body)))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		t.Fatal(err)
	}

	if v, ok := jsonPathGet(doc, "max_tokens"); !ok || v.(json.Number).String() != "100" {
		t.Fatalf("get max_tokens = %v %v", v, ok)
	}
	if v, ok := jsonPathGet(doc, "messages.0.content"); !ok || v.(string) != "hi" {
		t.Fatalf("get messages.0.content = %v %v", v, ok)
	}
	if _, ok := jsonPathGet(doc, "messages.0.missing"); ok {
		t.Fatal("missing path reported present")
	}
	if v, ok := jsonPathGet(doc, "nested.a.b"); !ok || v.(json.Number).String() != "1" {
		t.Fatalf("get nested.a.b = %v %v", v, ok)
	}

	if err := jsonPathSet(doc, "max_tokens", json.Number("8000")); err != nil {
		t.Fatal(err)
	}
	if v, _ := jsonPathGet(doc, "max_tokens"); v.(json.Number).String() != "8000" {
		t.Fatalf("set max_tokens = %v", v)
	}
	// Auto-create nested containers.
	if err := jsonPathSet(doc, "new.deep.path", "x"); err != nil {
		t.Fatal(err)
	}
	if v, ok := jsonPathGet(doc, "new.deep.path"); !ok || v.(string) != "x" {
		t.Fatalf("auto-create = %v %v", v, ok)
	}
	// Array auto-extend.
	if err := jsonPathSet(doc, "list.3.name", "fourth"); err != nil {
		t.Fatal(err)
	}
	if v, ok := jsonPathGet(doc, "list.3.name"); !ok || v.(string) != "fourth" {
		t.Fatalf("array extend = %v %v", v, ok)
	}

	if err := jsonPathDelete(doc, "nested.a.b"); err != nil {
		t.Fatal(err)
	}
	if _, ok := jsonPathGet(doc, "nested.a.b"); ok {
		t.Fatal("delete left value behind")
	}
	// Array element delete compacts.
	if err := jsonPathDelete(doc, "messages.0"); err != nil {
		t.Fatal(err)
	}
	if _, ok := jsonPathGet(doc, "messages.0"); ok {
		t.Fatal("array delete did not compact")
	}
}

func TestApplyPayloadRules(t *testing.T) {
	body := []byte(`{"model":"gpt-4o","max_tokens":100,"messages":[{"role":"user","content":"hi"}]}`)

	// Rule matching model glob + protocol + payload condition, set + delete.
	rules := `[
	  {"name":"cap","match":{"model":"gpt-*","protocol":"openai","payload":{"max_tokens":{"exists":true}}},
	   "actions":[{"op":"set","path":"max_tokens","value":{"num":8000}},{"op":"delete","path":"messages.0.content"}]}
	]`
	out, filter, err := ApplyPayloadRules(body, rules, "gpt-4o", "openai", nil)
	if err != nil {
		t.Fatal(err)
	}
	if filter != nil {
		t.Fatal("unexpected filter")
	}
	if !strings.Contains(string(out), `"max_tokens":8000`) {
		t.Fatalf("set not applied: %s", out)
	}
	if strings.Contains(string(out), `"content":"hi"`) {
		t.Fatalf("delete not applied: %s", out)
	}

	// Non-matching model → passthrough untouched.
	out, _, err = ApplyPayloadRules(body, rules, "claude-3", "openai", nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(body) {
		t.Fatalf("non-matching rule rewrote body: %s", out)
	}

	// Protocol mismatch → passthrough.
	out, _, _ = ApplyPayloadRules(body, rules, "gpt-4o", "anthropic", nil)
	if string(out) != string(body) {
		t.Fatalf("protocol mismatch rewrote body: %s", out)
	}

	// Header condition.
	headerRules := `[{"name":"tenant","match":{"header":{"x-tenant":"beta"}},"actions":[{"op":"set","path":"max_tokens","value":{"num":1}}]}]`
	out, _, _ = ApplyPayloadRules(body, headerRules, "gpt-4o", "openai", map[string]string{"x-tenant": "beta"})
	if !strings.Contains(string(out), `"max_tokens":1`) {
		t.Fatalf("header match not applied: %s", out)
	}
	out, _, _ = ApplyPayloadRules(body, headerRules, "gpt-4o", "openai", map[string]string{"x-tenant": "prod"})
	if string(out) != string(body) {
		t.Fatalf("header mismatch rewrote body: %s", out)
	}

	// Filter action short-circuits.
	filterRules := `[{"name":"block","match":{"payload":{"messages.#.content.#.image_url":{"exists":true}}},"actions":[{"op":"filter","reason":"images blocked"}]}]`
	imageBody := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"x"}}]}]}`)
	_, filter, err = ApplyPayloadRules(imageBody, filterRules, "gpt-4o", "openai", nil)
	if err != nil {
		t.Fatal(err)
	}
	if filter == nil || filter.Reason != "images blocked" {
		t.Fatalf("filter not fired: %+v", filter)
	}
	// Non-image body passes the filter rule untouched.
	out, filter, _ = ApplyPayloadRules(body, filterRules, "gpt-4o", "openai", nil)
	if filter != nil || string(out) != string(body) {
		t.Fatalf("filter misfired: filter=%+v out=%s", filter, out)
	}

	// Empty / absent rules passthrough.
	out, filter, err = ApplyPayloadRules(body, "", "gpt-4o", "openai", nil)
	if err != nil || filter != nil || string(out) != string(body) {
		t.Fatalf("empty rules: err=%v filter=%+v", err, filter)
	}
	out, _, _ = ApplyPayloadRules(body, "[]", "gpt-4o", "openai", nil)
	if string(out) != string(body) {
		t.Fatalf("[] rules rewrote body: %s", out)
	}
}

func TestApplyPayloadRulesMalformed(t *testing.T) {
	body := []byte(`{"model":"gpt-4o"}`)
	// Malformed rules JSON → error surfaced (proxy logs + passthrough).
	_, _, err := ApplyPayloadRules(body, `{not json`, "gpt-4o", "openai", nil)
	if err == nil {
		t.Fatal("malformed rules must error")
	}
	// Malformed body with payload-conditional rule → safe passthrough, no
	// error: an unparseable body must never block the request, the rule is
	// simply skipped (the upstream still gets the original bytes).
	rules := `[{"match":{"payload":{"max_tokens":{"exists":true}}},"actions":[{"op":"set","path":"max_tokens","value":{"num":1}}]}]`
	out, _, err := ApplyPayloadRules([]byte(`{broken`), rules, "gpt-4o", "openai", nil)
	if err != nil {
		t.Fatalf("malformed body must passthrough, got error: %v", err)
	}
	if string(out) != `{broken` {
		t.Fatalf("malformed body rewritten: %s", out)
	}
}

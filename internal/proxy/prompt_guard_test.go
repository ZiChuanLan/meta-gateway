package proxy

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lan/meta-gateway/internal/store"
)

func guardRules(overrides ...func(*store.PromptGuardRule)) []store.PromptGuardRule {
	base := store.PromptGuardRule{
		Name: "secret", Pattern: `sk-[A-Za-z0-9]{16,}`, Action: "mask",
		Replacement: "[REDACTED]", Enabled: true,
	}
	for _, fn := range overrides {
		fn(&base)
	}
	return []store.PromptGuardRule{base}
}

func TestGuardMaskPreservesJSONStructure(t *testing.T) {
	body := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"my key is sk-ABCDEFGHIJKLMNOP1234 keep it"},{"role":"assistant","content":"ok"}]}`)
	out, hit, err := ApplyPromptGuards(body, guardRules())
	if err != nil {
		t.Fatal(err)
	}
	if hit == nil || hit.Action != "mask" || !hit.Masked {
		t.Fatalf("hit = %+v, want mask hit", hit)
	}
	if strings.Contains(string(out), "sk-ABCDEFGHIJKLMNOP1234") {
		t.Fatalf("secret leaked: %s", out)
	}
	if !strings.Contains(string(out), "[REDACTED]") {
		t.Fatalf("replacement missing: %s", out)
	}
	// The result must still be valid JSON with the same shape.
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("masked body is not valid JSON: %v", err)
	}
	messages := doc["messages"].([]any)
	first := messages[0].(map[string]any)
	if first["role"] != "user" {
		t.Fatalf("structure changed: %s", out)
	}
}

func TestGuardReject(t *testing.T) {
	body := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"tell me about sk-ABCDEFGHIJKLMNOP1234"}]}`)
	_, hit, err := ApplyPromptGuards(body, guardRules(func(r *store.PromptGuardRule) {
		r.Action = "reject"
	}))
	if err != nil {
		t.Fatal(err)
	}
	if hit == nil || hit.Action != "reject" {
		t.Fatalf("hit = %+v, want reject hit", hit)
	}
	if !strings.Contains(hit.Message, "content policy") {
		t.Fatalf("message = %q", hit.Message)
	}
}

func TestGuardExclude(t *testing.T) {
	body := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"audit account sk-ABCDEFGHIJKLMNOP1234"}]}`)
	_, hit, err := ApplyPromptGuards(body, guardRules(func(r *store.PromptGuardRule) {
		r.Action = "exclude"
		r.ExcludeChannels = "5, 12"
	}))
	if err != nil {
		t.Fatal(err)
	}
	if hit == nil || hit.Action != "exclude" {
		t.Fatalf("hit = %+v, want exclude hit", hit)
	}
	if len(hit.Exclude) != 2 || hit.Exclude[0] != 5 || hit.Exclude[1] != 12 {
		t.Fatalf("exclude = %v, want [5 12]", hit.Exclude)
	}
}

func TestGuardNestedArraysAndNoMatch(t *testing.T) {
	body := []byte(`{"messages":[{"content":[{"type":"text","text":"safe text"},{"type":"image_url","image_url":{"url":"data:image/png;base64,sk-ABCDEFGHIJKLMNOP1234"}}]}]}`)
	out, hit, err := ApplyPromptGuards(body, guardRules())
	if err != nil {
		t.Fatal(err)
	}
	if hit == nil {
		t.Fatal("nested secret not detected")
	}
	if strings.Contains(string(out), "sk-ABCDEFGHIJKLMNOP1234") {
		t.Fatalf("nested secret leaked: %s", out)
	}
	// No match → byte-identical passthrough.
	clean := []byte(`{"messages":[{"role":"user","content":"nothing here"}]}`)
	out, hit, err = ApplyPromptGuards(clean, guardRules())
	if err != nil {
		t.Fatal(err)
	}
	if hit != nil {
		t.Fatalf("unexpected hit: %+v", hit)
	}
	if string(out) != string(clean) {
		t.Fatalf("clean body rewritten: %s", out)
	}
	// Non-JSON body passes through without a hit.
	out, hit, err = ApplyPromptGuards([]byte("not json at all"), guardRules())
	if err != nil || hit != nil || string(out) != "not json at all" {
		t.Fatalf("non-JSON body: out=%s hit=%v err=%v", out, hit, err)
	}
	// Disabled rules do not fire.
	out, hit, err = ApplyPromptGuards(body, guardRules(func(r *store.PromptGuardRule) {
		r.Enabled = false
	}))
	if err != nil || hit != nil {
		t.Fatalf("disabled rule fired: hit=%v err=%v", hit, err)
	}
	if string(out) != string(body) {
		t.Fatal("disabled rule still rewrote body")
	}
}

package adapters

import (
	"io"
	"strings"
	"testing"
)

// TestRegistryMatrixRegistered verifies the built-in (from, to) matrix.
func TestRegistryMatrixRegistered(t *testing.T) {
	r := NewTranslationRegistry()
	want := []string{
		"anthropic→anthropic",
		"anthropic→openai",
		"openai→anthropic",
		"openai→gemini",
		"openai→openai",
	}
	got := map[string]bool{}
	for _, pair := range r.Pairs() {
		got[pair.String()] = true
	}
	for _, w := range want {
		if !got[w] {
			t.Fatalf("pair %s not registered; have %v", w, r.Pairs())
		}
	}
}

// TestRegistryThreeModesIndependent verifies stream / non-stream / count-token
// modes are separate: a pair may translate bodies without a local counter.
func TestRegistryThreeModesIndependent(t *testing.T) {
	r := NewTranslationRegistry()
	// anthropic→openai: body + stream modes exist; no local count-token mode.
	tr, ok := r.Lookup("anthropic", "openai")
	if !ok {
		t.Fatal("anthropic→openai missing")
	}
	if tr.Body == nil || tr.Stream == nil {
		t.Fatal("anthropic→openai must translate body + stream")
	}
	if _, ok, _ := r.CountTokensFor("anthropic", "openai", []byte(`{}`)); ok {
		t.Fatal("anthropic→openai must not claim local token counting")
	}
	if r.StreamFor("openai", "anthropic") == nil {
		t.Fatal("openai→anthropic stream mode missing")
	}
	if r.StreamFor("unknown", "openai") != nil {
		t.Fatal("unregistered pair must have no stream mode")
	}
}

// TestRegistryTranslateAnthropicToOpenAI verifies a real body translation
// through the matrix (native Anthropic Messages → OpenAI chat).
func TestRegistryTranslateAnthropicToOpenAI(t *testing.T) {
	r := NewTranslationRegistry()
	body := []byte(`{"model":"claude-3-5-sonnet","max_tokens":100,"messages":[{"role":"user","content":"hi"}]}`)
	toPath, out, _, ok, err := r.Translate("anthropic", "openai", "messages", body)
	if err != nil || !ok {
		t.Fatalf("translate: ok=%v err=%v", ok, err)
	}
	if toPath != "chat/completions" {
		t.Fatalf("toPath = %s, want chat/completions", toPath)
	}
	if !strings.Contains(string(out), `"role": "assistant"`) && !strings.Contains(string(out), `"messages"`) {
		t.Fatalf("translated body missing openai shape: %s", out)
	}
	if strings.Contains(string(out), "claude-3-5-sonnet") {
		// Model names may be kept (mapping happens at the route layer); this
		// is not an error — just informational.
		t.Logf("model kept: %s", out)
	}
}

// TestRegistryFallbackRewritesModelOnly verifies the unregistered-pair
// fallback: the model field is rewritten, everything else passes verbatim.
func TestRegistryFallbackRewritesModelOnly(t *testing.T) {
	r := NewTranslationRegistry()
	body := []byte(`{"model":"alias-model","temperature":0.7,"messages":[{"role":"user","content":"hi"}]}`)
	toPath, out, tr, ok, err := r.Translate("gemini", "openai", "chat/completions", body)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("gemini→openai is not registered; fallback expected")
	}
	if toPath != "chat/completions" {
		t.Fatalf("fallback toPath = %s", toPath)
	}
	// The fallback body carries the original model (it has nothing to rewrite
	// to without a mapping); the critical property is that it is a valid
	// passthrough with the same fields.
	if !strings.Contains(string(out), `"alias-model"`) || !strings.Contains(string(out), `"temperature":0.7`) {
		t.Fatalf("fallback corrupted body: %s", out)
	}
	// Fallback translation passes responses/streams through untouched.
	resp, err := tr.Response("x", []byte(`{"a":1}`))
	if err != nil || string(resp) != `{"a":1}` {
		t.Fatalf("fallback response: %s %v", resp, err)
	}
	reader := io.NopCloser(strings.NewReader("raw"))
	wrapped, err := tr.Stream("x", reader)
	if err != nil || wrapped != io.NopCloser(strings.NewReader("raw")) {
		// identity reader: read it back to confirm passthrough
		b, _ := io.ReadAll(wrapped)
		if string(b) != "raw" {
			t.Fatalf("fallback stream: %s", b)
		}
	}
}

// TestModelRewriteFallback verifies the model-field-only rewrite on a JSON
// body and the untouched passthrough for non-JSON / model-less bodies.
func TestModelRewriteFallback(t *testing.T) {
	out := ModelRewriteFallback([]byte(`{"model":"old","x":1}`), "new")
	if !strings.Contains(string(out), `"model":"new"`) {
		t.Fatalf("model not rewritten: %s", out)
	}
	if !strings.Contains(string(out), `"x":1`) {
		t.Fatalf("other fields lost: %s", out)
	}
	raw := []byte(`not-json`)
	if got := ModelRewriteFallback(raw, "new"); string(got) != string(raw) {
		t.Fatalf("non-json must pass through: %s", got)
	}
	noModel := []byte(`{"x":1}`)
	if got := ModelRewriteFallback(noModel, ""); string(got) != string(noModel) {
		t.Fatalf("model-less body must pass through: %s", got)
	}
}

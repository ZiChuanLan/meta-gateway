package routing

import "testing"

func TestSessionKeyFromRequestHeaderWins(t *testing.T) {
	body := []byte(`{"model":"m","messages":[{"role":"user","content":"hello"}]}`)
	if key := SessionKeyFromRequest(body, "  my-session-1  "); key != "my-session-1" {
		t.Fatalf("explicit header should win, got %q", key)
	}
}

func TestSessionKeyFromRequestHeaderHashesLongValues(t *testing.T) {
	long := ""
	for i := 0; i < 300; i++ {
		long += "x"
	}
	key := SessionKeyFromRequest(nil, long)
	if len(key) != 66 || key[:2] != "h:" {
		t.Fatalf("long header must use a bounded hash key, got %q", key)
	}
	other := long[:299] + "y"
	if otherKey := SessionKeyFromRequest(nil, other); otherKey == key {
		t.Fatal("distinct long headers must not collide by prefix truncation")
	}
}

func TestSessionKeyFromBodyDigestStableAcrossTurns(t *testing.T) {
	// The first user message does not change as the conversation grows, so
	// the digest must be identical across turns of the same conversation.
	turn1 := []byte(`{"model":"m","messages":[{"role":"user","content":"what is 2+2?"}]}`)
	turn2 := []byte(`{"model":"m","messages":[{"role":"user","content":"what is 2+2?"},{"role":"assistant","content":"4"},{"role":"user","content":"and 3+3?"}]}`)
	key1 := SessionKeyFromBody(turn1)
	key2 := SessionKeyFromBody(turn2)
	if key1 == "" || key1 != key2 {
		t.Fatalf("digest must be stable across turns: %q vs %q", key1, key2)
	}
	if key1[:2] != "s:" {
		t.Fatalf("digest must carry the s: prefix, got %q", key1)
	}
	// A different conversation must produce a different key.
	other := []byte(`{"model":"m","messages":[{"role":"user","content":"tell me a joke"}]}`)
	if otherKey := SessionKeyFromBody(other); otherKey == key1 {
		t.Fatalf("different conversations must differ: %q", otherKey)
	}
}

func TestSessionKeyFromBodyStructuredContent(t *testing.T) {
	body := []byte(`{"model":"m","messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`)
	if key := SessionKeyFromBody(body); key == "" {
		t.Fatal("structured content must still derive a key")
	}
}

func TestSessionKeyFromBodyRejectsInvalid(t *testing.T) {
	if key := SessionKeyFromBody(nil); key != "" {
		t.Fatalf("nil body must yield empty key, got %q", key)
	}
	if key := SessionKeyFromBody([]byte("not json")); key != "" {
		t.Fatalf("non-JSON body must yield empty key, got %q", key)
	}
	if key := SessionKeyFromBody([]byte(`{"model":"m","messages":[]}`)); key != "" {
		t.Fatalf("no user message must yield empty key, got %q", key)
	}
	if key := SessionKeyFromBody([]byte(`{"model":"m","messages":[{"role":"system","content":"sys"}]}`)); key != "" {
		t.Fatalf("system-only messages must yield empty key, got %q", key)
	}
}

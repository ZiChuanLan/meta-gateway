package proxy

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
)

// `keep` is the body allowlist a strictly-validating upstream needs. TypeSafe
// System One is the measured case: it answers 400 to a body carrying a single
// unknown key (even `temperature`), so the OpenAI client body — which always
// brings `messages`, and usually `stream` / `temperature` — is unusable until
// the envelope is reduced to exactly what the upstream knows.
func TestRequestMapKeepReducesTheBodyToAnAllowlist(t *testing.T) {
	upstream := ParseUpstreamMap("v1/systemone", "", `[
  {"from":"messages.0.content","to":"state"},
  {"keep":["model","state"]},
  {"to":"questions.answer.type","value":{"str":"noul"}}
]`, "")
	if upstream.Empty() {
		t.Fatal("map parsed as empty")
	}
	inbound := []byte(`{"model":"jev-latest","messages":[{"role":"user","content":"the moon is made of rock"}],"stream":true,"temperature":0.7,"max_tokens":64}`)
	outbound, changed, warn := upstream.MapRequest(inbound)
	if !changed {
		t.Fatal("body was not rewritten")
	}
	if warn != nil {
		t.Fatalf("unexpected mapping warnings: %v", warn)
	}
	var doc map[string]any
	if err := json.Unmarshal(outbound, &doc); err != nil {
		t.Fatalf("mapped body is not JSON: %v", err)
	}
	want := []string{"model", "questions", "state"}
	if len(doc) != len(want) {
		t.Fatalf("mapped body kept %d keys (%v), want exactly %v", len(doc), keysOf(doc), want)
	}
	for _, key := range want {
		if _, ok := doc[key]; !ok {
			t.Fatalf("mapped body lost %q: %v", key, keysOf(doc))
		}
	}
	// The value read before the allowlist still has to be the read value — the
	// entry order in the array is the order of operations.
	if doc["state"] != "the moon is made of rock" {
		t.Fatalf("state = %v", doc["state"])
	}
	if doc["model"] != "jev-latest" {
		t.Fatalf("model = %v", doc["model"])
	}
}

// keep with nothing left to drop must report "unchanged" so the relay can hand
// back the original bytes instead of a re-encoded document.
func TestRequestMapKeepOnAnAlreadyMinimalBodyIsANoOp(t *testing.T) {
	upstream := ParseUpstreamMap("", "", `[{"keep":["model"]}]`, "")
	_, changed, err := upstream.MapRequest([]byte(`{"model":"jev-latest"}`))
	if err != nil || changed {
		t.Fatalf("changed=%v err=%v, want a no-op", changed, err)
	}
}

func TestValidateUpstreamMapKeep(t *testing.T) {
	cases := []struct {
		name    string
		request string
		wantErr string
	}{
		{
			name:    "keep cannot carry a from path",
			request: `[{"from":"messages.0.content","keep":["model"]}]`,
			wantErr: "mutually exclusive",
		},
		{
			name:    "keep cannot carry a literal",
			request: `[{"keep":["model"],"value":{"str":"x"}}]`,
			wantErr: "mutually exclusive",
		},
		{
			name:    "keep takes top-level keys only",
			request: `[{"keep":["messages.0.content"]}]`,
			wantErr: "not a top-level key",
		},
		{
			name:    "keep needs at least one real key",
			request: `[{"keep":["  "]}]`,
			wantErr: "at least one key",
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, _, _, _, err := ValidateUpstreamMap("", "", tt.request, "")
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want one containing %q", err, tt.wantErr)
			}
		})
	}

	// The canonical form round-trips: a valid keep survives validation and is
	// what the admin API stores.
	if _, _, request, _, err := ValidateUpstreamMap("", "", `[{"keep":["model","state"]}]`, ""); err != nil {
		t.Fatalf("valid keep rejected: %v", err)
	} else if !strings.Contains(request, `"keep":["model","state"]`) {
		t.Fatalf("keep was not preserved: %s", request)
	}
}

// The shipped TypeSafe profile is what every console-created TypeSafe channel
// gets, so its body must be exactly what the upstream accepts. Asserting the key
// set here is what keeps the "extra key → 400" trap from coming back.
func TestTypeSafeProfileProducesAnAllowedBody(t *testing.T) {
	profile, ok := LookupProviderProfile("typesafe")
	if !ok {
		t.Fatal("no typesafe profile")
	}
	upstream := ParseUpstreamMap(profile.PathOverride, "", profile.RequestMap, profile.ResponseMap)
	outbound, changed, warn := upstream.MapRequest([]byte(
		`{"model":"jev-latest","messages":[{"role":"system","content":"be brief"},{"role":"user","content":"the moon is made of rock"}],"stream":true,"temperature":0.7}`))
	if !changed {
		t.Fatal("profile request map did nothing")
	}
	if warn != nil {
		t.Fatalf("profile request map warned: %v", warn)
	}
	var doc map[string]any
	if err := json.Unmarshal(outbound, &doc); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if got := keysOf(doc); len(got) != 3 {
		t.Fatalf("keys = %v, want exactly model/questions/state", got)
	}
	questions, ok := doc["questions"].(map[string]any)
	if !ok {
		t.Fatalf("questions is %T, want a map (TypeSafe rejects an array)", doc["questions"])
	}
	if _, ok := questions["answer"]; !ok {
		t.Fatalf("questions is keyed by question id, got %v", questions)
	}

	// The documented caveat: System One scores a state against a question, so a
	// leading system message IS the state. This asserts the behaviour the
	// profile comment promises rather than letting it drift silently.
	if doc["state"] != "be brief" {
		t.Fatalf("state = %v", doc["state"])
	}
}

// Every provider profile is a compile-time contract: if one of them can no
// longer be applied to a channel the console would create silently mapping-free
// channels.
func TestProviderProfilesApplyToAChannel(t *testing.T) {
	profile, ok := LookupProviderProfile("typesafe")
	if !ok {
		t.Fatal("no typesafe profile")
	}
	ch := &domain.Channel{}
	if !ApplyProviderProfile(ch, profile.Type) {
		t.Fatal("profile was not applied")
	}
	upstream := ParseUpstreamMap(ch.UpstreamPathOverride, ch.UpstreamPathMap, ch.UpstreamRequestMap, ch.UpstreamResponseMap)
	if upstream.Empty() {
		t.Fatal("applied profile produced an empty mapping")
	}
	if got := upstream.ResolvePath("chat/completions", "jev-latest"); got != "/v1/systemone" {
		t.Fatalf("chat path resolves to %q", got)
	}
}

func keysOf(doc map[string]any) []string {
	keys := make([]string, 0, len(doc))
	for key := range doc {
		keys = append(keys, key)
	}
	return keys
}

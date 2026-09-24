package proxy_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/proxy"
)

// A profile is a protocol contract, so a broken one must fail here rather than
// degrade to "path rewritten, body unchanged" at request time — that failure
// mode is invisible to the operator and looks exactly like an upstream error.
func TestProviderProfilesAreValidMappings(t *testing.T) {
	for _, provider := range []string{"typesafe", "TypeSafe", " typesafe-systemone "} {
		profile, ok := proxy.LookupProviderProfile(provider)
		if !ok {
			t.Fatalf("no profile for %q", provider)
		}
		override, _, requestMap, responseMap, err := proxy.ValidateUpstreamMap(
			profile.PathOverride, "", profile.RequestMap, profile.ResponseMap)
		if err != nil {
			t.Fatalf("profile %s is invalid: %v", profile.Type, err)
		}
		if override == "" {
			t.Fatalf("profile %s has no usable path override", profile.Type)
		}
		if requestMap == "" || responseMap == "" {
			t.Fatalf("profile %s has an empty field map", profile.Type)
		}
	}
}

// The TypeSafe mappings are the actual product: measured against the live API on
// 2026-09-23, `POST /v1/systemone` needs {state, questions:{<id>:{type:…}}} and
// answers with a NUMBER under answers.<id>.noul. Both facts are asserted here
// because a hand-written guess gets each of them wrong (an array for questions,
// a string for the answer).
func TestTypeSafeProfileMatchesTheMeasuredProtocol(t *testing.T) {
	profile, ok := proxy.LookupProviderProfile("typesafe")
	if !ok {
		t.Fatal("typesafe profile missing")
	}
	if got := proxy.NormalizeEndpointPath(profile.PathOverride); got != "/v1/systemone" {
		t.Fatalf("path override resolves to %q, want /v1/systemone", got)
	}

	var requests []proxy.UpstreamFieldMap
	if err := json.Unmarshal([]byte(profile.RequestMap), &requests); err != nil {
		t.Fatalf("request map: %v", err)
	}
	if len(requests) == 0 || requests[0].From != "messages.0.content" || requests[0].To != "state" {
		t.Fatalf("request map does not move the prompt into state: %+v", requests)
	}
	foundQuestion := false
	for _, entry := range requests {
		if strings.HasPrefix(entry.To, "questions.") {
			foundQuestion = true
		}
	}
	if !foundQuestion {
		t.Fatalf("request map never writes questions.*: %+v", requests)
	}

	var responses []proxy.UpstreamFieldMap
	if err := json.Unmarshal([]byte(profile.ResponseMap), &responses); err != nil {
		t.Fatalf("response map: %v", err)
	}
	if content := findMap(responses, "choices.0.message.content"); content == nil ||
		!strings.Contains(content.Template, "answers.") {
		t.Fatalf("response map does not template the answer into content: %+v", responses)
	}
	if findMap(responses, "usage.prompt_tokens") == nil || findMap(responses, "usage.completion_tokens") == nil {
		t.Fatalf("response map does not carry upstream usage: %+v", responses)
	}
}

func TestApplyProviderProfileFillsAnEmptyChannel(t *testing.T) {
	ch := &domain.Channel{TypeHint: "typesafe"}
	if !proxy.ApplyProviderProfile(ch, ch.TypeHint) {
		t.Fatal("profile was not applied to an empty channel")
	}
	if ch.UpstreamPathOverride != "v1/systemone" {
		t.Fatalf("override = %q", ch.UpstreamPathOverride)
	}
	if ch.UpstreamRequestMap == "" || ch.UpstreamResponseMap == "" {
		t.Fatal("field maps were not filled")
	}

	// Idempotent: a second save must not rewrite the row.
	again := &domain.Channel{
		TypeHint:             "typesafe",
		UpstreamPathOverride: ch.UpstreamPathOverride,
		UpstreamPathMap:      ch.UpstreamPathMap,
		UpstreamRequestMap:   ch.UpstreamRequestMap,
		UpstreamResponseMap:  ch.UpstreamResponseMap,
	}
	if proxy.ApplyProviderProfile(again, "typesafe") {
		t.Fatal("profile rewrote an already-filled channel")
	}
}

// The save-time split of a pasted full endpoint URL writes the path override
// before the profile runs. That is the same intent, so the profile must still
// fill the field maps — otherwise "type the provider and paste the documented
// URL" would silently leave the channel unable to chat.
func TestApplyProviderProfileKeepsGoingAfterAURLSplit(t *testing.T) {
	ch := &domain.Channel{TypeHint: "typesafe", UpstreamPathOverride: "/v1/systemone"}
	if !proxy.ApplyProviderProfile(ch, "typesafe") {
		t.Fatal("profile did not fill the field maps after a URL split")
	}
	if ch.UpstreamRequestMap == "" || ch.UpstreamResponseMap == "" {
		t.Fatal("field maps missing")
	}
	if ch.UpstreamPathOverride != "v1/systemone" {
		t.Fatalf("override = %q", ch.UpstreamPathOverride)
	}
}

// The reasoning vocabulary is a runtime capability, so it must be reachable
// even when the save-time mapping is the operator's own: a channel pointed at
// System One with the New API type still gets the provider's rung set, which is
// what stops `reasoning_effort=max` from reaching an API that only knows
// low/medium/high/xhigh/none.
func TestAcceptedReasoningLevelsMatchTypeAndEndpoint(t *testing.T) {
	want := "none,low,medium,high,xhigh"
	for _, tc := range []struct{ name, providerType, path string }{
		{"declared provider", "typesafe", ""},
		{"provider alias", "Typesafe-SystemOne", ""},
		{"endpoint path", "new-api", "v1/systemone"},
		{"endpoint path with leading slash", "new-api", "/v1/systemone"},
		{"endpoint path from the map", "custom", "/api/v1/systemone"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := proxy.AcceptedReasoningLevels(tc.providerType, tc.path)
			if strings.Join(got, ",") != want {
				t.Fatalf("levels = %v, want %s", got, want)
			}
		})
	}

	// Everything else keeps the transparent default: no opinion, values pass
	// through exactly as the client wrote them.
	for _, tc := range []struct{ name, providerType, path string }{
		{"plain openai", "openai-compatible", "chat/completions"},
		{"unknown endpoint", "new-api", "v1/chat/completions"},
		{"nothing to go on", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := proxy.AcceptedReasoningLevels(tc.providerType, tc.path); len(got) != 0 {
				t.Fatalf("levels = %v, want none", got)
			}
		})
	}
}

func TestApplyProviderProfileLeavesHandWrittenMappingsAlone(t *testing.T) {
	ch := &domain.Channel{
		TypeHint:           "typesafe",
		UpstreamRequestMap: `[{"from":"messages.0.content","to":"prompt"}]`,
	}
	if proxy.ApplyProviderProfile(ch, "typesafe") {
		t.Fatal("profile overwrote a hand-written request map")
	}
	if ch.UpstreamPathOverride != "" || ch.UpstreamResponseMap != "" {
		t.Fatalf("profile touched a hand-built channel: %+v", ch)
	}
}

func TestApplyProviderProfileIgnoresUnknownProviders(t *testing.T) {
	ch := &domain.Channel{TypeHint: "openai-compatible"}
	if proxy.ApplyProviderProfile(ch, "openai-compatible") {
		t.Fatal("a plain OpenAI-compatible channel must not get a mapping")
	}
	if ch.UpstreamPathOverride != "" || ch.UpstreamRequestMap != "" {
		t.Fatalf("channel was modified: %+v", ch)
	}
}

func findMap(entries []proxy.UpstreamFieldMap, to string) *proxy.UpstreamFieldMap {
	for i := range entries {
		if entries[i].To == to {
			return &entries[i]
		}
	}
	return nil
}

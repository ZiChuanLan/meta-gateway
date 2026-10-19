package plugins

import (
	"encoding/json"
	"testing"
	"time"
)

// The event point is declared like any other point, but its default budget is
// the one sized for a call per streamed frame rather than per request.
func TestEventHookDeclaredWithPerEventBudget(t *testing.T) {
	set := &HookSet{Event: &HookDeclaration{Path: "/hooks/event", MatchModels: []string{"m"}}}
	if set.Empty() {
		t.Fatal("a hook set carrying only an event declaration reports empty")
	}
	if err := ValidateHookSet(set); err != nil {
		t.Fatalf("validate: %v", err)
	}
	points := set.declarations()
	if len(points) != 1 || points[0].Point != HookPointEvent {
		t.Fatalf("declarations = %+v, want the event point", points)
	}

	cases := []struct {
		name  string
		entry hookEntry
		want  time.Duration
	}{
		{"event default", hookEntry{point: HookPointEvent}, defaultEventHookTimeoutMs * time.Millisecond},
		{"request default", hookEntry{point: HookPointRequest}, defaultHookTimeoutMs * time.Millisecond},
		{"event declares its own", hookEntry{point: HookPointEvent, decl: HookDeclaration{TimeoutMs: 500}}, 500 * time.Millisecond},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := testCase.entry.hookTimeout(); got != testCase.want {
				t.Fatalf("timeout = %s, want %s", got, testCase.want)
			}
		})
	}
}

// A manifest key that is not a known point must not silently become a hook: the
// wire spelling of every point is one constant, and the event point is part of
// that set.
func TestHookPointNames(t *testing.T) {
	for _, want := range []string{"route", "request", "response", "event"} {
		var set HookSet
		if err := json.Unmarshal([]byte(`{"`+want+`":{"path":"/h","match_models":["m"]}}`), &set); err != nil {
			t.Fatalf("decode %s: %v", want, err)
		}
		points := set.declarations()
		if len(points) != 1 || points[0].Point != want {
			t.Fatalf("decoded %s as %+v", want, points)
		}
	}
}

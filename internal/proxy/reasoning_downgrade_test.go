package proxy

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDowngradeReasoningEffort(t *testing.T) {
	cases := []struct {
		name      string
		body      string
		max       string
		wantBody  string // substring expected in rewritten body
		wantNote  string
		wantApply bool
	}{
		{
			name:      "downgrade max to high",
			body:      `{"model":"m","reasoning_effort":"max","messages":[]}`,
			max:       "high",
			wantBody:  `"reasoning_effort":"high"`,
			wantNote:  "max→high",
			wantApply: true,
		},
		{
			name:      "downgrade xhigh to medium",
			body:      `{"reasoning_effort":"xhigh"}`,
			max:       "medium",
			wantBody:  `"reasoning_effort":"medium"`,
			wantNote:  "xhigh→medium",
			wantApply: true,
		},
		{
			name:      "already at max unchanged",
			body:      `{"reasoning_effort":"high"}`,
			max:       "high",
			wantApply: false,
		},
		{
			name:      "below max unchanged",
			body:      `{"reasoning_effort":"low"}`,
			max:       "max",
			wantApply: false,
		},
		{
			name:      "unknown value passes through",
			body:      `{"reasoning_effort":"turbo"}`,
			max:       "high",
			wantApply: false,
		},
		{
			name:      "missing field untouched",
			body:      `{"model":"m"}`,
			max:       "high",
			wantApply: false,
		},
		{
			name:      "case-insensitive",
			body:      `{"reasoning_effort":"MAX"}`,
			max:       "High",
			wantBody:  `"reasoning_effort":"high"`,
			wantNote:  "max→high",
			wantApply: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, note := downgradeReasoningEffort([]byte(tc.body), tc.max)
			if tc.wantApply {
				if out == nil {
					t.Fatalf("expected rewrite, got nil (note=%q)", note)
				}
				if !strings.Contains(string(out), tc.wantBody) {
					t.Fatalf("body = %s, want substring %s", out, tc.wantBody)
				}
				if note != tc.wantNote {
					t.Fatalf("note = %q, want %q", note, tc.wantNote)
				}
				// The rewritten body must remain valid JSON.
				var parsed map[string]any
				if err := json.Unmarshal(out, &parsed); err != nil {
					t.Fatalf("rewritten body invalid JSON: %v", err)
				}
			} else if out != nil {
				t.Fatalf("expected no rewrite, got %s (note=%q)", out, note)
			}
		})
	}
}

func TestDowngradeReasoningEffortPreservesOtherFields(t *testing.T) {
	body := `{"model":"alias","reasoning_effort":"max","messages":[{"role":"user","content":"hi"}],"stream":true,"temperature":0.7}`
	out, note := downgradeReasoningEffort([]byte(body), "xhigh")
	if out == nil || note != "max→xhigh" {
		t.Fatalf("unexpected: out=%s note=%q", out, note)
	}
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatal(err)
	}
	var model string
	if err := json.Unmarshal(parsed["model"], &model); err != nil || model != "alias" {
		t.Fatalf("model lost: %s", parsed["model"])
	}
	var stream bool
	if err := json.Unmarshal(parsed["stream"], &stream); err != nil || !stream {
		t.Fatalf("stream lost: %s", parsed["stream"])
	}
}

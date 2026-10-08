package callplan_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/callplan"
	"github.com/lan/meta-gateway/internal/domain"
)

type decodedBody struct {
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
	MaxTokens int    `json:"max_tokens"`
	Stream    bool   `json:"stream"`
	Model     string `json:"model"`
}

func decode(t *testing.T, body []byte) decodedBody {
	t.Helper()
	var parsed decodedBody
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("decode body: %v (%s)", err, body)
	}
	return parsed
}

func plan(t *testing.T, policy, purpose string, spec callplan.Spec) callplan.Plan {
	t.Helper()
	result, err := callplan.Request(policy, purpose, spec, "hi")
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	return result
}

// The minimal form is what the prober always sent: one user message, one token.
func TestMinimalFormKeepsTheProbeShape(t *testing.T) {
	got := plan(t, domain.CallPolicyAllowProbe, domain.PurposeProbe, callplan.Spec{
		Model: "m", MaxTokens: 1, ChannelID: 7,
	})
	body := decode(t, got.Body)
	if len(body.Messages) != 1 || body.Messages[0].Role != "user" {
		t.Fatalf("minimal form messages = %+v, want one user message", body.Messages)
	}
	if body.Messages[0].Content != "hi" {
		t.Errorf("minimal prompt = %q, want the caller's default", body.Messages[0].Content)
	}
	if body.MaxTokens != 1 {
		t.Errorf("minimal max_tokens = %d, want 1", body.MaxTokens)
	}
	if got.Form != domain.CallFormMinimal {
		t.Errorf("form = %q, want minimal", got.Form)
	}
}

// A site that bans probing gets a real request: a system message, a real
// question, and a budget that is no longer the probe signature.
func TestRealFormReplacesTheProbeSignature(t *testing.T) {
	got := plan(t, domain.CallPolicyRealCallsOnly, domain.PurposeProbe, callplan.Spec{
		Model: "m", MaxTokens: 1, ChannelID: 7, At: time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC),
	})
	body := decode(t, got.Body)
	if len(body.Messages) != 2 || body.Messages[0].Role != "system" || body.Messages[1].Role != "user" {
		t.Fatalf("real form messages = %+v, want system + user", body.Messages)
	}
	if body.Messages[1].Content == "hi" {
		t.Error(`real form still asks "hi": the probe signature was not replaced`)
	}
	if body.MaxTokens < callplan.RealMaxTokensFloor {
		t.Errorf("real max_tokens = %d, want >= %d", body.MaxTokens, callplan.RealMaxTokensFloor)
	}
	if got.Form != domain.CallFormReal {
		t.Errorf("form = %q, want real", got.Form)
	}
	if strings.TrimSpace(got.Prompt) == "" {
		t.Error("plan carries no prompt to log")
	}
}

// The floor matters most in the case that caused the feature: an operator
// configured one token because it was cheap, and the site bans that shape.
func TestRealFormRaisesATinyConfiguredBudget(t *testing.T) {
	for _, configured := range []int{0, 1, 63} {
		got := plan(t, domain.CallPolicyRealCallsOnly, domain.PurposeKeepalive, callplan.Spec{
			Model: "m", MaxTokens: configured,
		})
		if body := decode(t, got.Body); body.MaxTokens != callplan.DefaultMaxTokens {
			t.Errorf("configured %d: max_tokens = %d, want the %d default", configured, body.MaxTokens, callplan.DefaultMaxTokens)
		}
	}
	// A budget above the floor is the operator's call and is left alone.
	got := plan(t, domain.CallPolicyRealCallsOnly, domain.PurposeKeepalive, callplan.Spec{Model: "m", MaxTokens: 256})
	if body := decode(t, got.Body); body.MaxTokens != 256 {
		t.Errorf("max_tokens = %d, want the configured 256", body.MaxTokens)
	}
}

// The prompt pool varies by channel and day, and is stable within a day: a retry
// must not look like a burst of different clients.
func TestRealPromptVariesByChannelAndDayAndIsStable(t *testing.T) {
	day := time.Date(2026, 10, 8, 6, 0, 0, 0, time.UTC)
	same := callplan.RealPrompt(callplan.Spec{ChannelID: 3, At: day})
	if again := callplan.RealPrompt(callplan.Spec{ChannelID: 3, At: day.Add(3 * time.Hour)}); again != same {
		t.Errorf("prompt changed within a day: %q then %q", same, again)
	}
	next := callplan.RealPrompt(callplan.Spec{ChannelID: 3, At: day.Add(24 * time.Hour)})
	other := callplan.RealPrompt(callplan.Spec{ChannelID: 4, At: day})
	if same == next && same == other {
		t.Errorf("prompt never varies (channel and day both produced %q)", same)
	}
}

// An operator's own prompt wins in either form: it is their channel, and a
// keepalive they wrote is the traffic they want to send.
func TestConfiguredPromptWinsInBothForms(t *testing.T) {
	for _, policy := range []string{domain.CallPolicyAllowProbe, domain.CallPolicyRealCallsOnly} {
		got := plan(t, policy, domain.PurposeKeepalive, callplan.Spec{Model: "m", Prompt: "keep me alive"})
		if body := decode(t, got.Body); body.Messages[len(body.Messages)-1].Content != "keep me alive" {
			t.Errorf("%s: prompt = %q, want the operator's", policy, body.Messages[len(body.Messages)-1].Content)
		}
	}
}

// The registry the compiler cannot check: every purpose must have a form, so a
// new automated call path cannot be added without deciding what it looks like.
func TestEveryPurposeHasAForm(t *testing.T) {
	forms := map[string]bool{domain.CallFormMinimal: true, domain.CallFormReal: true}
	for _, purpose := range domain.AllCallPurposes {
		for _, policy := range []string{domain.CallPolicyAllowProbe, domain.CallPolicyRealCallsOnly, "", "nonsense"} {
			form := domain.CallFormFor(policy, purpose)
			if !forms[form] {
				t.Errorf("purpose %q under policy %q has no known form (%q)", purpose, policy, form)
			}
		}
	}
}

// A policy only ever reshapes an automated call; an operator's own test already
// carries a real prompt and a real budget.
func TestDiagnosticIsAlwaysReal(t *testing.T) {
	if got := domain.CallFormFor(domain.CallPolicyAllowProbe, domain.PurposeDiagnostic); got != domain.CallFormReal {
		t.Errorf("diagnostic form = %q, want real", got)
	}
}

// Package callplan builds the body of an automated call — a model probe, a
// keepalive ping — in the shape the target site's policy asks for.
//
// It exists as its own package because two call paths (internal/probe and
// internal/keepalive) must produce the identical request shape for the identical
// policy. Two builders would drift, and the drift would only be visible at the
// site that bans probing: one path would keep sending the one-token signature
// while its peer had already stopped.
package callplan

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
)

const (
	// DefaultMaxTokens is the budget of a real-form call when the caller has no
	// opinion. Big enough for a two-sentence answer, small enough to be cheap.
	DefaultMaxTokens = 192
	// RealMaxTokensFloor is the smallest budget a real-form call may use. A
	// caller who configured 1 token (the probe default) still gets a real
	// request, because a one-token budget *is* the probe signature — that is the
	// entire reason the real form exists.
	RealMaxTokensFloor = 64
	// SystemPrompt accompanies a real-form call so the request looks like the
	// traffic a real client sends, which is what a site inspecting shape sees.
	SystemPrompt = "You are a helpful assistant."
)

// promptPool is what a real-form call asks. Short, ordinary questions with no
// answer that matters: the result is only ever judged by its status code. The
// pool exists so a keepalive on a 30-day window does not send the byte-identical
// request thirty times in a row, which is its own signature.
var promptPool = []string{
	"用一句话说明什么是 API。",
	"帮我把“今天天气不错”翻译成英文。",
	"写一句 20 字以内的产品介绍。",
	"解释一下缓存的作用，一句话即可。",
	"把下面这句话改得更简洁：我们将在稍后进行处理。",
	"举一个日常生活中用到排队的例子。",
	"用一句话说明版本控制的好处。",
	"给出三个常见的编程命名规范。",
}

// Spec is what a caller knows about the call it wants to make.
type Spec struct {
	// Model is the upstream model name.
	Model string
	// Prompt is the operator's configured prompt. Empty falls back to the
	// caller's default (minimal form) or the pool (real form).
	Prompt string
	// MaxTokens is the operator's configured budget, or 0 for the default.
	MaxTokens int
	// ChannelID seeds the per-day variation, so two channels do not send the
	// identical request on the same day. 0 means "no variation".
	ChannelID int64
	// At is the moment the call is planned; only its date matters. Zero means
	// now, so tests can pin it.
	At time.Time
}

// Plan is a body ready to send, plus the facts worth logging about it.
type Plan struct {
	Body []byte
	// Prompt is the prompt actually sent — the operator's, or the pool entry
	// that was chosen for this channel and day.
	Prompt string
	// MaxTokens is the budget actually sent, after the real-form floor.
	MaxTokens int
	// Form is the shape that produced this body.
	Form string
}

// Request plans an automated call under a policy.
//
// defaultPrompt is what the caller normally sends on a minimal probe (the probe
// package's "hi"); a real-form call ignores it and asks a real question instead,
// because "hi" with a large token budget is still a probe to anything reading
// the request.
func Request(policy, purpose string, spec Spec, defaultPrompt string) (Plan, error) {
	form := domain.CallFormFor(policy, purpose)
	plan := Plan{Form: form, MaxTokens: spec.MaxTokens}

	var messages []map[string]string
	if form == domain.CallFormReal {
		plan.Prompt = RealPrompt(spec)
		plan.MaxTokens = RealMaxTokens(spec.MaxTokens)
		messages = []map[string]string{
			{"role": "system", "content": SystemPrompt},
			{"role": "user", "content": plan.Prompt},
		}
	} else {
		plan.Prompt = strings.TrimSpace(spec.Prompt)
		if plan.Prompt == "" {
			plan.Prompt = defaultPrompt
		}
		if plan.MaxTokens <= 0 {
			plan.MaxTokens = 1
		}
		messages = []map[string]string{{"role": "user", "content": plan.Prompt}}
	}

	body, err := json.Marshal(map[string]any{
		"model":      spec.Model,
		"messages":   messages,
		"stream":     false,
		"max_tokens": plan.MaxTokens,
	})
	if err != nil {
		return Plan{}, fmt.Errorf("build %s request: %w", form, err)
	}
	plan.Body = body
	return plan, nil
}

// RealMaxTokens applies the real-form floor and default to a configured budget.
func RealMaxTokens(configured int) int {
	if configured < RealMaxTokensFloor {
		return DefaultMaxTokens
	}
	return configured
}

// RealPrompt returns the prompt a real-form call asks: the operator's when they
// wrote one, otherwise a pool entry chosen by channel and day.
//
// The choice is deterministic (no rand): the same channel sends the same
// question for a whole day — so a keepalive that retries does not look like a
// burst of different clients — and a different one tomorrow.
func RealPrompt(spec Spec) string {
	if prompt := strings.TrimSpace(spec.Prompt); prompt != "" {
		return prompt
	}
	at := spec.At
	if at.IsZero() {
		at = time.Now()
	}
	day := at.UTC().Unix() / 86400
	index := (day + spec.ChannelID) % int64(len(promptPool))
	if index < 0 {
		index += int64(len(promptPool))
	}
	return promptPool[index]
}

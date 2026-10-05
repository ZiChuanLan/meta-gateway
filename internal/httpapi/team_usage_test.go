package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
)

// A member's overview counts that member's own traffic and nothing else.
//
// This boundary is the entire risk of giving members an overview: the
// /me/usage/* handlers and the console's /admin/usage/* share one set of
// aggregates (store.UsageScope), so a scope passed wrongly would show one
// account another's traffic. The test therefore asserts both halves — that a
// member sees their own usage, and that the other member's rows are absent —
// for every one of the three endpoints.
func TestMemberUsageOverviewIsScopedToTheAccount(t *testing.T) {
	e := newTeamTestEnv(t)
	e.enable()
	alice := e.member("alice", "member")
	bob := e.member("bob", "member")
	aliceKey, _ := alice.key("alice-key")
	bobKey, _ := bob.key("bob-key")

	seed := func(keyID, userID int64, model string, prompt, completion int, cost float64) {
		t.Helper()
		if _, err := e.db.Usage.Insert(&domain.UsageRecord{
			RequestID:        fmt.Sprintf("req-%d-%s", keyID, model),
			DownstreamKeyID:  keyID,
			UserID:           userID,
			Model:            model,
			Path:             "chat/completions",
			PromptTokens:     prompt,
			CompletionTokens: completion,
			TotalTokens:      prompt + completion,
			Status:           http.StatusOK,
			Cost:             cost,
		}); err != nil {
			t.Fatalf("seed usage: %v", err)
		}
	}
	seed(aliceKey, alice.userID, "alice-model", 100, 50, 1.5)
	seed(bobKey, bob.userID, "bob-model", 900, 100, 9)

	var aliceSummary domain.UsageSummary
	json.Unmarshal(alice.request("GET", "/me/usage/summary", nil, http.StatusOK), &aliceSummary)
	if aliceSummary.RequestCount != 1 || aliceSummary.PromptTokens != 100 ||
		aliceSummary.CompletionTokens != 50 || aliceSummary.TotalTokens != 150 {
		t.Fatalf("alice summary = %+v, want exactly her own request", aliceSummary)
	}
	if aliceSummary.Cost != 1.5 {
		t.Fatalf("alice cost = %v, want 1.5 (bob's 9 must not leak in)", aliceSummary.Cost)
	}

	var bobSummary domain.UsageSummary
	json.Unmarshal(bob.request("GET", "/me/usage/summary", nil, http.StatusOK), &bobSummary)
	if bobSummary.RequestCount != 1 || bobSummary.PromptTokens != 900 {
		t.Fatalf("bob summary = %+v, want exactly his own request", bobSummary)
	}

	// The ranking is scoped too: a member's top model list must not name a model
	// only somebody else called.
	var aliceModels []struct {
		Model    string `json:"model"`
		Requests int    `json:"requests"`
	}
	json.Unmarshal(alice.request("GET", "/me/usage/top-models", nil, http.StatusOK), &aliceModels)
	if len(aliceModels) != 1 || aliceModels[0].Model != "alice-model" {
		t.Fatalf("alice top models = %+v, want only alice-model", aliceModels)
	}

	// The chart is scoped as well. The window is widened so the freshly seeded
	// rows are inside it regardless of bucket alignment.
	var aliceSeries struct {
		Requests []int   `json:"requests"`
		Tokens   []int64 `json:"tokens"`
	}
	json.Unmarshal(alice.request("GET", "/me/usage/series?window_minutes=1440", nil, http.StatusOK), &aliceSeries)
	requests, tokens := 0, int64(0)
	for _, n := range aliceSeries.Requests {
		requests += n
	}
	for _, n := range aliceSeries.Tokens {
		tokens += n
	}
	if requests != 1 || tokens != 150 {
		t.Fatalf("alice series = %d requests / %d tokens, want 1 / 150", requests, tokens)
	}

	// A member has no business reading the gateway's own totals: the admin
	// endpoints stay behind the admin gate.
	alice.request("GET", "/admin/usage/summary", nil, http.StatusForbidden)
}

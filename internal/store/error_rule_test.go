package store_test

import (
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/store"
)

// TestErrorRuleLifecycle covers CRUD plus the live-match semantics used by
// the proxy (status/keyword/model glob/channel scoping).
func TestErrorRuleLifecycle(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	rule := store.ErrorPassRule{
		Name:       "rate-limit",
		StatusCode: 429,
		Keyword:    "rate limit",
		ModelGlob:  "gpt-*",
		ChannelID:  0,
		Action:     store.ErrorRulePassthrough,
		Enabled:    true,
	}
	id, err := db.ErrorRule.Create(&rule)
	if err != nil {
		t.Fatal(err)
	}
	if id <= 0 {
		t.Fatal("no id")
	}

	// Match: status + keyword + model glob.
	got, err := db.ErrorRule.MatchErrorRule(429, "{\"error\":{\"message\":\"rate limit exceeded\"}}", "gpt-4o", 7)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Name != "rate-limit" {
		t.Fatalf("rule not matched: %+v", got)
	}

	// Non-matching keyword → nil.
	got, err = db.ErrorRule.MatchErrorRule(429, "insufficient quota", "gpt-4o", 7)
	if err != nil || got != nil {
		t.Fatalf("keyword mismatch matched: %+v err=%v", got, err)
	}

	// Non-matching status → nil.
	got, err = db.ErrorRule.MatchErrorRule(401, "rate limit", "gpt-4o", 7)
	if err != nil || got != nil {
		t.Fatalf("status mismatch matched: %+v err=%v", got, err)
	}

	// Non-matching model glob → nil.
	got, err = db.ErrorRule.MatchErrorRule(429, "rate limit", "claude-3", 7)
	if err != nil || got != nil {
		t.Fatalf("model glob mismatch matched: %+v err=%v", got, err)
	}

	// 2xx/5xx never match (rules are 4xx-only).
	got, err = db.ErrorRule.MatchErrorRule(503, "rate limit", "gpt-4o", 7)
	if err != nil || got != nil {
		t.Fatalf("5xx matched: %+v err=%v", got, err)
	}

	// Channel-scoped rule.
	rule2 := store.ErrorPassRule{
		Name: "ch-scoped", StatusCode: 400, Keyword: "bad request", Action: store.ErrorRuleIgnoreMonitor, Enabled: true,
	}
	id2, _ := db.ErrorRule.Create(&rule2)
	if err := db.ErrorRule.Update(&store.ErrorPassRule{ID: id2, Name: "ch-scoped", StatusCode: 400, Keyword: "bad request", ChannelID: 3, Action: store.ErrorRuleIgnoreMonitor, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	got, err = db.ErrorRule.MatchErrorRule(400, "bad request", "any", 3)
	if err != nil || got == nil || got.ChannelID != 3 {
		t.Fatalf("channel rule not matched: %+v err=%v", got, err)
	}
	got, err = db.ErrorRule.MatchErrorRule(400, "bad request", "any", 4)
	if err != nil || got != nil {
		t.Fatalf("channel rule matched wrong channel: %+v err=%v", got, err)
	}

	// Disabled rule stops matching (hot reload semantics).
	if err := db.ErrorRule.Update(&store.ErrorPassRule{ID: id, Name: "rate-limit", StatusCode: 429, Keyword: "rate limit", ModelGlob: "gpt-*", Action: store.ErrorRulePassthrough, Enabled: false}); err != nil {
		t.Fatal(err)
	}
	got, err = db.ErrorRule.MatchErrorRule(429, "rate limit", "gpt-4o", 7)
	if err != nil || got != nil {
		t.Fatalf("disabled rule matched: %+v err=%v", got, err)
	}

	// Delete.
	if err := db.ErrorRule.Delete(id); err != nil {
		t.Fatal(err)
	}
	if err := db.ErrorRule.Delete(id); err == nil {
		t.Fatal("double delete succeeded")
	}
	list, err := db.ErrorRule.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("list len = %d, want 1", len(list))
	}
	_ = time.Now
}

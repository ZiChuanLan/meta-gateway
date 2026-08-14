package routing

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
)

// TestSessionKeyChainOrder verifies the identity chain: prompt_cache_key >
// session_id > metadata.user_id (conversation) > conversation_id > content
// hash. Each level is tried only when the previous is absent.
func TestSessionKeyChainOrder(t *testing.T) {
	base := func() map[string]any {
		return map[string]any{
			"model":    "m",
			"messages": []map[string]any{{"role": "user", "content": "hi"}},
		}
	}
	// Level 1: prompt_cache_key wins over everything else.
	withCache := base()
	withCache["prompt_cache_key"] = "cache-1"
	withCache["metadata"] = map[string]any{"user_id": "conv-9"}
	body, _ := json.Marshal(withCache)
	if key := SessionKeyFromBody(body); key != "c:cache-1" {
		t.Fatalf("prompt_cache_key level = %q", key)
	}
	// Level 1b: session_id field.
	withSession := base()
	withSession["session_id"] = "sess-7"
	withSession["metadata"] = map[string]any{"user_id": "conv-9"}
	body, _ = json.Marshal(withSession)
	if key := SessionKeyFromBody(body); key != "x:sess-7" {
		t.Fatalf("session_id level = %q", key)
	}
	// Level 2: metadata.user_id (conversation carrier).
	withConv := base()
	withConv["metadata"] = map[string]any{"user_id": "conv-9"}
	body, _ = json.Marshal(withConv)
	if key := SessionKeyFromBody(body); key != "u:conv-9" {
		t.Fatalf("metadata.user_id level = %q", key)
	}
	// Level 2b: conversation_id.
	withConvID := base()
	withConvID["conversation_id"] = "conv-42"
	body, _ = json.Marshal(withConvID)
	if key := SessionKeyFromBody(body); key != "n:conv-42" {
		t.Fatalf("conversation_id level = %q", key)
	}
	// Level 3: content hash (prefix s:).
	body, _ = json.Marshal(base())
	if key := SessionKeyFromBody(body); len(key) != 66 || key[:2] != "s:" {
		t.Fatalf("content hash level = %q", key)
	}
}

// TestSessionKeyStableAcrossTurns keeps the existing stability guarantee.
func TestSessionKeyStableAcrossTurns(t *testing.T) {
	turn1 := []byte(`{"messages":[{"role":"user","content":"hello"}]}`)
	turn2 := []byte(`{"messages":[{"role":"user","content":"hello"},{"role":"assistant","content":"hi"},{"role":"user","content":"more"}]}`)
	if SessionKeyFromBody(turn1) != SessionKeyFromBody(turn2) {
		t.Fatal("digest must be stable across turns")
	}
}

// TestBindingOutranksPriorityTier verifies a sticky binding selects the bound
// channel even when another channel now sits in a higher priority tier.
func TestBindingOutranksPriorityTier(t *testing.T) {
	route := &domain.Route{ID: 1, ModelPattern: "m", Enabled: true}
	now := time.Now().UTC()
	candidates := []domain.RoutingCandidate{
		{
			Member:           domain.RouteMember{RouteID: 1, ChannelID: 10, Priority: 1, Weight: 100, Enabled: true},
			Channel:          domain.Channel{ID: 10, Name: "a", BaseURL: "http://a", Status: domain.StatusEnabled},
			CredentialUsable: true,
		},
		{
			Member:           domain.RouteMember{RouteID: 1, ChannelID: 20, Priority: 2, Weight: 100, Enabled: true},
			Channel:          domain.Channel{ID: 20, Name: "b", BaseURL: "http://b", Status: domain.StatusEnabled},
			CredentialUsable: true,
		},
	}
	repo := fakeRepo{route: route, candidates: candidates}
	// Bind the lower-priority channel 20 for "sess-1".
	stickyStore := NewStickyStore(time.Hour, systemClock{})
	stickyStore.Bind("sess-1", 20, now)
	selector := New(repo)
	selector.SetSticky(stickyStore)
	decision, err := selector.SelectSticky(context.Background(), "m", nil, "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	if decision.Selected.Channel.ID != 20 {
		t.Fatalf("bound channel 20 not selected (got %d); binding must outrank the priority tier", decision.Selected.Channel.ID)
	}
}

func ptrI64(v int64) *int64 { return &v }

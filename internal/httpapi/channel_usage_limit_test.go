package httpapi_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
)

// The channel's usage budget is what an operator sets in the connection editor:
// "this account came with $12.50 — stop sending when it is spent". It is
// validated like every other numeric field (a negative limit would park the
// channel on its first request), and the response carries the counters, because
// the drawer shows what has been used next to what is allowed.
func TestChannelUsageLimitsRoundTrip(t *testing.T) {
	base, _, _ := setupServer(t, "http://127.0.0.1:1")
	_, conn := postConnection(t, base, "admin-secret", map[string]any{
		"base_url": "https://api.example.com",
		"secret":   "sk-test",
	})
	path := fmt.Sprintf("/admin/channels/%d", conn.Channel.ID)

	for _, bad := range []map[string]any{
		{"usage_limit_cost": -1},
		{"usage_limit_tokens": -5},
	} {
		status, body := adminJSONBody(t, base, "admin-secret", http.MethodPut, path, bad)
		if status != http.StatusBadRequest {
			t.Fatalf("%v: status=%d body=%s", bad, status, body)
		}
	}

	status, body := adminJSONBody(t, base, "admin-secret", http.MethodPut, path, map[string]any{
		"usage_limit_cost":   12.5,
		"usage_limit_tokens": 1_000_000,
	})
	if status != http.StatusOK {
		t.Fatalf("set limits: status=%d body=%s", status, body)
	}
	var updated domain.Channel
	if err := json.Unmarshal(body, &updated); err != nil {
		t.Fatal(err)
	}
	if updated.UsageLimitCost != 12.5 || updated.UsageLimitTokens != 1_000_000 {
		t.Fatalf("limits = %v/%d, want 12.5/1000000", updated.UsageLimitCost, updated.UsageLimitTokens)
	}
	if updated.UsageLimitHit != "" || updated.UsageLimitHitAt != "" {
		t.Fatalf("a fresh limit must not read as tripped: %q/%q", updated.UsageLimitHit, updated.UsageLimitHitAt)
	}

	// The overview is the edit drawer's data source: if the budget did not
	// round-trip through it, saving the drawer would silently clear it.
	status, body = adminJSONBody(t, base, "admin-secret", http.MethodGet, "/admin/channels/overview", nil)
	if status != http.StatusOK {
		t.Fatalf("overview: status=%d body=%s", status, body)
	}
	var overviews []domain.ChannelOverview
	if err := json.Unmarshal(body, &overviews); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, overview := range overviews {
		if overview.Channel.ID != conn.Channel.ID {
			continue
		}
		found = true
		if overview.Channel.UsageLimitCost != 12.5 || overview.Channel.UsageLimitTokens != 1_000_000 {
			t.Fatalf("overview limits = %v/%d, want the saved 12.5/1000000",
				overview.Channel.UsageLimitCost, overview.Channel.UsageLimitTokens)
		}
	}
	if !found {
		t.Fatalf("channel %d missing from the overview", conn.Channel.ID)
	}

	// Clearing both hands the channel back to "unlimited" — and an unrelated
	// patch must leave the budget alone.
	status, body = adminJSONBody(t, base, "admin-secret", http.MethodPut, path, map[string]any{
		"weight": 7,
	})
	if status != http.StatusOK {
		t.Fatalf("unrelated patch: status=%d body=%s", status, body)
	}
	if err := json.Unmarshal(body, &updated); err != nil {
		t.Fatal(err)
	}
	if updated.UsageLimitCost != 12.5 || updated.Weight != 7 {
		t.Fatalf("after an unrelated patch: cost limit = %v weight = %d, want 12.5/7",
			updated.UsageLimitCost, updated.Weight)
	}

	status, body = adminJSONBody(t, base, "admin-secret", http.MethodPut, path, map[string]any{
		"usage_limit_cost":   0,
		"usage_limit_tokens": 0,
	})
	if status != http.StatusOK {
		t.Fatalf("clear limits: status=%d body=%s", status, body)
	}
	// Both fields are `omitempty`, so "no limit" arrives as their absence — a
	// fresh struct is what makes that readable (reusing one would keep the old
	// value and hide a cleared field behind a missing key).
	var cleared domain.Channel
	if err := json.Unmarshal(body, &cleared); err != nil {
		t.Fatal(err)
	}
	if cleared.UsageLimitCost != 0 || cleared.UsageLimitTokens != 0 {
		t.Fatalf("cleared limits = %v/%d, want 0/0", cleared.UsageLimitCost, cleared.UsageLimitTokens)
	}
}

// A parked channel has to stay editable: the way out of a usage limit is to
// raise it, and a PATCH that does not mention `status` preserves the stored
// `auto_disabled` — which the validator used to reject as an invalid status, so
// the fix could not be applied from the console or the API at all.
func TestParkedChannelStaysEditable(t *testing.T) {
	base, _, db := setupServer(t, "http://127.0.0.1:1")
	_, conn := postConnection(t, base, "admin-secret", map[string]any{
		"base_url": "https://api.example.com",
		"secret":   "sk-test",
	})
	path := fmt.Sprintf("/admin/channels/%d", conn.Channel.ID)

	// Park it the way the relay does when a budget comes due.
	if _, err := db.Exec(`UPDATE channels SET status = ?, usage_limit_hit = 'cost', usage_limit_hit_at = datetime('now') WHERE id = ?`,
		domain.StatusAutoDisabled, conn.Channel.ID); err != nil {
		t.Fatal(err)
	}

	status, body := adminJSONBody(t, base, "admin-secret", http.MethodPut, path, map[string]any{
		"usage_limit_cost": 50,
	})
	if status != http.StatusOK {
		t.Fatalf("raising the limit on a parked channel: status=%d body=%s", status, body)
	}
	var updated domain.Channel
	if err := json.Unmarshal(body, &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Status != domain.StatusEnabled {
		t.Fatalf("status = %q, want the parked channel released once the limit allows it", updated.Status)
	}
	if updated.UsageLimitHit != "" || updated.UsageLimitHitAt != "" {
		t.Fatalf("hit state = %q/%q, want it cleared", updated.UsageLimitHit, updated.UsageLimitHitAt)
	}
	if updated.UsageLimitCost != 50 {
		t.Fatalf("limit = %v, want the saved 50", updated.UsageLimitCost)
	}
}

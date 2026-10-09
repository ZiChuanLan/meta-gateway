package httpapi_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
)

// The channel-level call policy is what an operator sets in the connection
// editor's advanced section: "this one channel talks to a site that bans
// probing". Empty means inherit the site's — which is why clearing it has to
// work, and why an unknown value must be rejected instead of normalized (see
// domain.NormalizeCallPolicy: unknown input falls back to allow_probe, the wrong
// answer for a site that bans it).
func TestChannelCallPolicyOverrideAndInheritance(t *testing.T) {
	base, _, _ := setupServer(t, "http://127.0.0.1:1")
	_, conn := postConnection(t, base, "admin-secret", map[string]any{
		"base_url": "https://api.example.com",
		"secret":   "sk-test",
	})
	path := fmt.Sprintf("/admin/channels/%d", conn.Channel.ID)

	if conn.Channel.CallPolicy != "" {
		t.Fatalf("a new channel should inherit, got %q", conn.Channel.CallPolicy)
	}

	for _, bad := range []string{"probe", "REAL_CALLS_ONLY", "real_calls", "allow"} {
		status, body := adminJSONBody(t, base, "admin-secret", http.MethodPut, path, map[string]any{
			"call_policy": bad,
		})
		if status != http.StatusBadRequest {
			t.Fatalf("call_policy=%q: status=%d body=%s", bad, status, body)
		}
	}

	// Surrounding whitespace is trimmed rather than rejected — the value that
	// lands in the database is the exact one, so a pasted string with a stray
	// space is a convenience, not a different policy.
	status, body := adminJSONBody(t, base, "admin-secret", http.MethodPut, path, map[string]any{
		"call_policy": "  real_calls_only ",
	})
	if status != http.StatusOK {
		t.Fatalf("padded policy: status=%d body=%s", status, body)
	}

	status, body = adminJSONBody(t, base, "admin-secret", http.MethodPut, path, map[string]any{
		"call_policy": "real_calls_only",
	})
	if status != http.StatusOK {
		t.Fatalf("set policy: status=%d body=%s", status, body)
	}
	var updated struct {
		CallPolicy string `json:"call_policy"`
	}
	if err := json.Unmarshal(body, &updated); err != nil {
		t.Fatal(err)
	}
	if updated.CallPolicy != "real_calls_only" {
		t.Fatalf("call_policy=%q, want real_calls_only", updated.CallPolicy)
	}

	// Clearing it hands the decision back to the site rather than pinning
	// allow_probe on the channel. The response omits the field when it is empty
	// (json:",omitempty"), so the assertion is on the raw body: "inherit" is
	// the absence of a policy, not a third value.
	status, body = adminJSONBody(t, base, "admin-secret", http.MethodPut, path, map[string]any{
		"call_policy": "",
	})
	if status != http.StatusOK {
		t.Fatalf("clear policy: status=%d body=%s", status, body)
	}
	if strings.Contains(string(body), "\"call_policy\"") {
		t.Fatalf("cleared policy should be absent from the payload, got %s", body)
	}

	// And a fresh read agrees: the channel has no policy of its own again.
	status, body = adminJSONBody(t, base, "admin-secret", http.MethodGet, path, nil)
	if status != http.StatusOK {
		t.Fatalf("read back: status=%d body=%s", status, body)
	}
	var fresh domain.Channel
	if err := json.Unmarshal(body, &fresh); err != nil {
		t.Fatal(err)
	}
	if fresh.CallPolicy != "" {
		t.Fatalf("after clearing, channel policy = %q, want empty (inherit)", fresh.CallPolicy)
	}

	// An unrelated patch must not disturb it, and an unrelated field must
	// survive the policy change (the merge direction both ways).
	status, body = adminJSONBody(t, base, "admin-secret", http.MethodPut, path, map[string]any{
		"call_policy": "allow_probe",
		"weight":      11,
	})
	if status != http.StatusOK {
		t.Fatalf("set both: status=%d body=%s", status, body)
	}
	var both struct {
		CallPolicy string `json:"call_policy"`
		Weight     int    `json:"weight"`
	}
	if err := json.Unmarshal(body, &both); err != nil {
		t.Fatal(err)
	}
	if both.CallPolicy != "allow_probe" || both.Weight != 11 {
		t.Fatalf("got policy=%q weight=%d", both.CallPolicy, both.Weight)
	}
}

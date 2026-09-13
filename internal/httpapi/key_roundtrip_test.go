package httpapi_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// The regression tests here pin field-level round-trips through the admin
// API. A column can be write-only for a whole feature cycle unless a test
// asserts it survives create → list → partial update.

func keyByID(t *testing.T, base string, id float64) map[string]any {
	t.Helper()
	// The list endpoint returns a bare JSON array, so parse the raw body
	// (adminCall only decodes objects).
	status, _, raw := adminCall(t, base, http.MethodGet, "/admin/downstream-keys", nil)
	if status != http.StatusOK {
		t.Fatalf("list keys status = %d", status)
	}
	var items []map[string]any
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		t.Fatalf("decode key list: %v", err)
	}
	for _, entry := range items {
		if entryID, _ := entry["id"].(float64); entryID == id {
			return entry
		}
	}
	t.Fatalf("key %v not found in list", id)
	return nil
}

func TestCreateKeyResponseEchoesExpiryAndIPs(t *testing.T) {
	srv, _, _ := revealTestServer(t)

	status, created, _ := adminCall(t, srv.URL, http.MethodPost, "/admin/downstream-keys", map[string]any{
		"name":        "echo",
		"expires_at":  "2027-01-01T00:00:00Z",
		"allowed_ips": "10.0.0.1",
	})
	if status != http.StatusCreated {
		t.Fatalf("create status = %d", status)
	}
	if created["expires_at"] != "2027-01-01T00:00:00Z" {
		t.Fatalf("expires_at = %v, want echo", created["expires_at"])
	}
	if created["allowed_ips"] != "10.0.0.1" {
		t.Fatalf("allowed_ips = %v, want echo", created["allowed_ips"])
	}
}

// routeGroupOf reads route_group_name from a key JSON entry; the field is
// omitempty on the wire, so an absent key means "".
func routeGroupOf(entry map[string]any) string {
	if v, ok := entry["route_group_name"]; ok {
		return v.(string)
	}
	return ""
}

func TestDownstreamKeyRouteGroupRoundTrip(t *testing.T) {
	srv, _, _ := revealTestServer(t)
	base := srv.URL

	status, created, _ := adminCall(t, base, http.MethodPost, "/admin/downstream-keys", map[string]any{
		"name":             "grouped",
		"route_group_name": "B",
	})
	if status != http.StatusCreated {
		t.Fatalf("create status = %d", status)
	}
	if got, _ := created["route_group_name"].(string); got != "B" {
		t.Fatalf("create response route_group_name = %v, want B", created["route_group_name"])
	}
	id, _ := created["id"].(float64)

	// The list endpoint must read the stored value.
	if entry := keyByID(t, base, id); routeGroupOf(entry) != "B" {
		t.Fatalf("listed route_group_name = %v, want B", entry["route_group_name"])
	}

	// A partial update that omits the group must not zero it.
	status, _, _ = adminCall(t, base, http.MethodPut, fmt.Sprintf("/admin/downstream-keys/%d", int64(id)), map[string]any{
		"name": "renamed",
	})
	if status != http.StatusOK {
		t.Fatalf("partial update status = %d", status)
	}
	if entry := keyByID(t, base, id); routeGroupOf(entry) != "B" {
		t.Fatalf("route_group_name after partial update = %v, want B (must survive)", entry["route_group_name"])
	}

	// Explicit updates still apply, and clearing persists.
	status, _, _ = adminCall(t, base, http.MethodPut, fmt.Sprintf("/admin/downstream-keys/%d", int64(id)), map[string]any{
		"route_group_name": "C",
	})
	if status != http.StatusOK {
		t.Fatalf("group update status = %d", status)
	}
	if entry := keyByID(t, base, id); routeGroupOf(entry) != "C" {
		t.Fatalf("route_group_name after explicit update = %v, want C", entry["route_group_name"])
	}
	status, _, _ = adminCall(t, base, http.MethodPut, fmt.Sprintf("/admin/downstream-keys/%d", int64(id)), map[string]any{
		"route_group_name": "",
	})
	if status != http.StatusOK {
		t.Fatalf("clear status = %d", status)
	}
	if entry := keyByID(t, base, id); routeGroupOf(entry) != "" {
		t.Fatalf("route_group_name after clear = %v, want empty (default routing)", entry["route_group_name"])
	}
}

func TestCredentialRejectsInvalidStatus(t *testing.T) {
	srv, _, _ := revealTestServer(t)
	base := srv.URL

	status, site, _ := adminCall(t, base, http.MethodPost, "/admin/sites", map[string]any{
		"name":     "status-check",
		"base_url": "https://api.example.com",
		"platform": "openai",
	})
	if status != http.StatusCreated {
		t.Fatalf("site create status = %d", status)
	}
	siteID, _ := site["id"].(float64)

	status, _, _ = adminCall(t, base, http.MethodPost, fmt.Sprintf("/admin/sites/%d/credentials", int64(siteID)), map[string]any{
		"kind":   "api_key",
		"secret": "sk-1",
		"status": "bogus",
	})
	if status != http.StatusBadRequest {
		t.Fatalf("bogus credential status = %d, want 400", status)
	}
}

func TestUpdateChannelOmittedFieldsKeepPriorityWeight(t *testing.T) {
	srv, _, _ := revealTestServer(t)
	base := srv.URL

	status, conn := postConnection(t, base, "admin-secret", map[string]any{
		"name":      "patched",
		"base_url":  "https://api.example.com",
		"secret":    "sk-live",
		"type_hint": "openai-compatible",
	})
	if status != http.StatusCreated {
		t.Fatalf("connection status = %d", status)
	}
	channelID := conn.Channel.ID

	// Establish a non-zero baseline through an explicit update.
	status, _, _ = adminCall(t, base, http.MethodPut, fmt.Sprintf("/admin/channels/%d", channelID), map[string]any{
		"priority": 7,
		"weight":   33,
	})
	if status != http.StatusOK {
		t.Fatalf("baseline update status = %d", status)
	}

	// Patch only the name: priority/weight must survive. They used to be
	// overwritten unconditionally, so omitting them zeroed both.
	status, _, _ = adminCall(t, base, http.MethodPut, fmt.Sprintf("/admin/channels/%d", channelID), map[string]any{
		"name": "renamed-only",
	})
	if status != http.StatusOK {
		t.Fatalf("name-only update status = %d", status)
	}

	status, _, raw := adminCall(t, base, http.MethodGet, "/admin/channels", nil)
	if status != http.StatusOK {
		t.Fatalf("channel list status = %d", status)
	}
	var channels []struct {
		ID       int64  `json:"id"`
		Name     string `json:"name"`
		Priority int    `json:"priority"`
		Weight   int    `json:"weight"`
	}
	if err := json.Unmarshal([]byte(raw), &channels); err != nil {
		t.Fatalf("decode channels: %v", err)
	}
	for _, ch := range channels {
		if ch.ID == channelID {
			if ch.Name != "renamed-only" || ch.Priority != 7 || ch.Weight != 33 {
				t.Fatalf("channel after partial patch = %+v, want name=renamed-only priority=7 weight=33", ch)
			}
			return
		}
	}
	t.Fatalf("channel %d not found", channelID)
}

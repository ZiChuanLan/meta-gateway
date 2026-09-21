package httpapi_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

// The site credential list must expose the key's pool tier and its own
// discovered model names. The console feeds the allowlist picker from that
// per-key list: an explicit models_csv overrides the per-key discovered
// filter, so candidates taken from the channel-wide union would invite
// selections the key cannot actually serve.
func TestCredentialListReportsPriorityAndModels(t *testing.T) {
	srv, db, _ := revealTestServer(t)
	base := srv.URL

	status, site, _ := adminCall(t, base, "POST", "/admin/sites", map[string]any{
		"name": "tiered", "base_url": "https://up.example", "platform": "new-api",
		"status": "enabled",
	})
	if status != http.StatusCreated {
		t.Fatalf("create site status %d", status)
	}
	siteID := int64(site["id"].(float64))

	// The preferred tier travels in the create body.
	status, created, _ := adminCall(t, base, "POST", fmt.Sprintf("/admin/sites/%d/credentials", siteID), map[string]any{
		"kind": "api_key", "secret": "sk-first", "status": "enabled",
		"priority": domain.CredentialPriorityPreferred,
	})
	if status != http.StatusCreated {
		t.Fatalf("create credential status %d", status)
	}
	if got := created["priority"]; got != float64(domain.CredentialPriorityPreferred) {
		t.Fatalf("created priority = %v, want %d", got, domain.CredentialPriorityPreferred)
	}
	firstID := int64(created["id"].(float64))

	// Omitting the field means the balanced tier, not the backup tier.
	status, created, _ = adminCall(t, base, "POST", fmt.Sprintf("/admin/sites/%d/credentials", siteID), map[string]any{
		"kind": "api_key", "secret": "sk-second", "status": "enabled",
	})
	if status != http.StatusCreated {
		t.Fatalf("create second credential status %d", status)
	}
	if got := created["priority"]; got != float64(domain.CredentialPriorityBalanced) {
		t.Fatalf("default priority = %v, want %d", got, domain.CredentialPriorityBalanced)
	}
	secondID := int64(created["id"].(float64))

	// Out-of-range values must be rejected instead of silently outranking the
	// whole pool forever.
	status, _, _ = adminCall(t, base, "PUT", fmt.Sprintf("/admin/credentials/%d", firstID), map[string]any{
		"priority": 1000,
	})
	if status != http.StatusBadRequest {
		t.Fatalf("priority 1000 status %d, want 400", status)
	}

	// Record per-key visibility exactly like a successful discovery pass.
	channelID, err := db.Channel.Create(&domain.Channel{
		SiteID: &siteID, Name: "tiered channel", BaseURL: "https://up.example",
		Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DiscoveredModel.Reconcile(t.Context(), store.ReconcileInput{
		ChannelID: channelID,
		Models:    []string{"codex-1", "gemini-3", "shared-2"},
		CredentialModels: map[int64][]string{
			firstID: {"shared-2", "codex-1"},
		},
		CheckedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	status, _, raw := adminCall(t, base, "GET", fmt.Sprintf("/admin/sites/%d/credentials", siteID), nil)
	if status != http.StatusOK {
		t.Fatalf("list credentials status %d", status)
	}
	byID := map[int64]map[string]any{}
	for _, cred := range mustUnmarshalCredentialList(raw) {
		byID[int64(cred["id"].(float64))] = cred
	}
	if got := byID[firstID]["priority"]; got != float64(domain.CredentialPriorityPreferred) {
		t.Fatalf("listed priority = %v, want %d", got, domain.CredentialPriorityPreferred)
	}
	// Sorted names, not the map's iteration order.
	models, err := json.Marshal(byID[firstID]["models"])
	if err != nil {
		t.Fatal(err)
	}
	if string(models) != `["codex-1","shared-2"]` {
		t.Fatalf("listed models = %s, want the key's own sorted snapshot", models)
	}
	if _, present := byID[secondID]["models"]; present {
		t.Fatalf("key without a snapshot must not carry models: %v", byID[secondID])
	}
	if got := byID[secondID]["model_count"]; got != float64(-1) {
		t.Fatalf("model_count = %v, want -1 for a key with no snapshot", got)
	}

	// Zero is a real tier (balanced), so the update has to distinguish
	// "omitted" from "0" — otherwise demoting a preferred key is impossible.
	status, updated, _ := adminCall(t, base, "PUT", fmt.Sprintf("/admin/credentials/%d", firstID), map[string]any{
		"priority": domain.CredentialPriorityBalanced,
	})
	if status != http.StatusOK {
		t.Fatalf("set priority 0 status %d", status)
	}
	if got := updated["priority"]; got != float64(domain.CredentialPriorityBalanced) {
		t.Fatalf("update priority = %v, want 0", got)
	}
	status, _, raw = adminCall(t, base, "GET", fmt.Sprintf("/admin/sites/%d/credentials", siteID), nil)
	if status != http.StatusOK {
		t.Fatalf("list credentials status %d", status)
	}
	for _, cred := range mustUnmarshalCredentialList(raw) {
		if int64(cred["id"].(float64)) == firstID && cred["priority"] != float64(domain.CredentialPriorityBalanced) {
			t.Fatalf("priority 0 did not persist: %v", cred)
		}
	}

	// A body without the field keeps the stored tier.
	status, updated, _ = adminCall(t, base, "PUT", fmt.Sprintf("/admin/credentials/%d", firstID), map[string]any{
		"priority": domain.CredentialPriorityBackup,
	})
	if status != http.StatusOK || updated["priority"] != float64(domain.CredentialPriorityBackup) {
		t.Fatalf("set backup tier: status=%d body=%v", status, updated)
	}
	status, updated, _ = adminCall(t, base, "PUT", fmt.Sprintf("/admin/credentials/%d", firstID), map[string]any{
		"status": "disabled",
	})
	if status != http.StatusOK {
		t.Fatalf("status-only update: %d", status)
	}
	if updated["priority"] != float64(domain.CredentialPriorityBackup) {
		t.Fatalf("status-only update reset the tier: %v", updated)
	}
}

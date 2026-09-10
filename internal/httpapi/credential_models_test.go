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

// listCredentials must report how many models each key lists in the latest
// discovery snapshot (-1 before any snapshot), never the secret.
func TestListCredentialsReportsPerKeyModelCount(t *testing.T) {
	srv, db, _ := revealTestServer(t)
	base := srv.URL

	// Two keys on one site, mirroring a group-scoped upstream pool.
	status, site, _ := adminCall(t, base, "POST", "/admin/sites", map[string]any{
		"name": "multi-group", "base_url": "https://up.example", "platform": "new-api",
		"status": "enabled",
	})
	if status != http.StatusCreated {
		t.Fatalf("create site status %d", status)
	}
	siteID := int64(site["id"].(float64))
	var firstID, secondID int64
	for _, secret := range []string{"sk-first", "sk-second"} {
		status, created, _ := adminCall(t, base, "POST", fmt.Sprintf("/admin/sites/%d/credentials", siteID), map[string]any{
			"kind": "api_key", "secret": secret, "status": "enabled",
		})
		if status != http.StatusCreated {
			t.Fatalf("create credential status %d", status)
		}
		id := int64(created["id"].(float64))
		if firstID == 0 {
			firstID = id
		} else {
			secondID = id
		}
	}

	// Before any successful discovery snapshot: model_count = -1.
	status, _, raw := adminCall(t, base, "GET", fmt.Sprintf("/admin/sites/%d/credentials", siteID), nil)
	if status != http.StatusOK {
		t.Fatalf("list credentials status %d", status)
	}
	creds := mustUnmarshalCredentialList(raw)
	if len(creds) != 2 {
		t.Fatalf("credential count = %d, want 2 (%s)", len(creds), raw)
	}
	for _, c := range creds {
		if c["model_count"] != float64(-1) {
			t.Fatalf("model_count = %v, want -1 before snapshot", c["model_count"])
		}
		if c["has_secret"] != true {
			t.Fatalf("has_secret should be true: %v", c)
		}
		if _, exposed := c["secret_enc"]; exposed {
			t.Fatalf("secret_enc leaked in list response")
		}
	}

	// Record per-key visibility through the store, exactly like a successful
	// two-key discovery pass, then re-read the API.
	channelID, err := db.Channel.Create(&domain.Channel{
		SiteID: &siteID, Name: "multi-group channel", BaseURL: "https://up.example",
		Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DiscoveredModel.Reconcile(t.Context(), store.ReconcileInput{
		ChannelID: channelID,
		Models:    []string{"codex-1", "gemini-3", "shared-2"},
		CredentialModels: map[int64][]string{
			firstID:  {"codex-1", "shared-2"},
			secondID: {"gemini-3"},
		},
		CheckedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	status, _, raw = adminCall(t, base, "GET", fmt.Sprintf("/admin/sites/%d/credentials", siteID), nil)
	if status != http.StatusOK {
		t.Fatalf("list credentials status %d", status)
	}
	creds = mustUnmarshalCredentialList(raw)
	byID := map[int64]map[string]any{}
	for _, c := range creds {
		byID[int64(c["id"].(float64))] = c
	}
	if got := byID[firstID]["model_count"]; got != float64(2) {
		t.Fatalf("first key model_count = %v, want 2", got)
	}
	if got := byID[secondID]["model_count"]; got != float64(1) {
		t.Fatalf("second key model_count = %v, want 1", got)
	}
	if _, exposed := byID[firstID]["secret_enc"]; exposed {
		t.Fatalf("secret_enc leaked after snapshot")
	}
}

func mustUnmarshalCredentialList(raw string) []map[string]any {
	var out []map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		panic(fmt.Sprintf("bad credentials list %q: %v", raw, err))
	}
	return out
}

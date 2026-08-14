package httpapi_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestDuplicateChannel(t *testing.T) {
	upstream := mockOpenAIUpstream(t)
	defer upstream.Close()
	serverURL, _, channelID := setupRelay(t, upstream.URL, "openai-compatible")
	getChannel := func(id float64) map[string]any {
		req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/admin/channels/%d", serverURL, int64(id)), nil)
		req.Header.Set("Authorization", "Bearer admin-test")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		json.NewDecoder(resp.Body).Decode(&out)
		return out
	}

	// The clone must copy every field with only the name suffixed.
	req, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/admin/channels/%d/duplicate", serverURL, channelID), strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer admin-test")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("duplicate status = %d", resp.StatusCode)
	}
	var clone struct {
		ID           float64 `json:"id"`
		Name         string  `json:"name"`
		BaseURL      string  `json:"base_url"`
		TypeHint     string  `json:"type_hint"`
		Status       string  `json:"status"`
		SiteID       float64 `json:"site_id"`
		CredentialID float64 `json:"credential_id"`
	}
	json.NewDecoder(resp.Body).Decode(&clone)
	resp.Body.Close()

	source := getChannel(float64(channelID))
	if clone.ID == 0 || clone.ID == float64(channelID) {
		t.Fatalf("clone id = %v", clone.ID)
	}
	if !strings.HasSuffix(clone.Name, " (copy)") {
		t.Fatalf("clone name = %q", clone.Name)
	}
	if clone.BaseURL != source["base_url"] {
		t.Fatalf("base_url not copied: %q vs %v", clone.BaseURL, source["base_url"])
	}
	if clone.TypeHint != source["type_hint"] {
		t.Fatalf("type_hint not copied")
	}
	if clone.Status != "enabled" {
		t.Fatalf("clone status = %q, want enabled", clone.Status)
	}
	if clone.SiteID != source["site_id"] {
		t.Fatalf("site_id not copied")
	}
	if clone.CredentialID != source["credential_id"] {
		t.Fatalf("credential_id not copied")
	}

	// The clone is editable through the normal update path.
	put(t, fmt.Sprintf("%s/admin/channels/%d", serverURL, int64(clone.ID)), map[string]any{"name": "clone-renamed"})
	updated := getChannel(clone.ID)
	if updated["name"] != "clone-renamed" {
		t.Fatalf("clone edit failed: %v", updated["name"])
	}

	// Unknown id → 404.
	req, _ = http.NewRequest(http.MethodPost, serverURL+"/admin/channels/999999/duplicate", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer admin-test")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown duplicate status = %d", resp.StatusCode)
	}
}

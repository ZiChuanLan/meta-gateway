package httpapi_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

// A downstream key can check its own quota via the OpenAI-compatible
// /v1/dashboard/billing/credit_summary surface.
func TestCreditSummaryReportsOwnQuota(t *testing.T) {
	// credit_summary is served locally; the upstream URL is never contacted.
	serverURL, _, _ := setupRelay(t, "http://127.0.0.1:9", "openai-compatible")

	var key struct {
		ID    int64  `json:"id"`
		Token string `json:"token"`
	}
	json.Unmarshal(post(t, serverURL+"/admin/downstream-keys", map[string]any{
		"name":               "quota-key",
		"scopes":             "relay",
		"quota_total_tokens": 1000000,
	}), &key)
	if key.Token == "" {
		t.Fatal("no token returned")
	}

	req, _ := http.NewRequest(http.MethodGet, serverURL+"/v1/dashboard/billing/credit_summary", nil)
	req.Header.Set("Authorization", "Bearer "+key.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var out struct {
		Object         string `json:"object"`
		TotalGranted   int64  `json:"total_granted"`
		TotalUsed      int64  `json:"total_used"`
		TotalAvailable int64  `json:"total_available"`
		ExpiresAt      int64  `json:"expires_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Object != "credit_summary" {
		t.Fatalf("object = %q", out.Object)
	}
	if out.TotalGranted != 1000000 || out.TotalUsed != 0 || out.TotalAvailable != 1000000 {
		t.Fatalf("summary = %+v", out)
	}
}

func TestCreditSummaryUnauthorized(t *testing.T) {
	serverURL, _, _ := setupRelay(t, "http://127.0.0.1:9", "openai-compatible")

	req, _ := http.NewRequest(http.MethodGet, serverURL+"/v1/dashboard/billing/credit_summary", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

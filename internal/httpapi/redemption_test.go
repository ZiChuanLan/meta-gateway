package httpapi_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRedemptionEndToEnd: admin mints a code → a downstream key redeems it →
// quota grows → the same code cannot be redeemed twice.
func TestRedemptionEndToEnd(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"data":[]}`)
	}))
	defer upstream.Close()

	serverURL, keyToken, _ := setupRelay(t, upstream.URL, "openai")

	// Mint one code worth 250k tokens.
	resp := post(t, serverURL+"/admin/redemption-codes", map[string]any{
		"count": 1, "quota_tokens": 250_000,
	})
	var minted struct {
		Items []struct {
			ID          int64  `json:"id"`
			Code        string `json:"code"`
			QuotaTokens int64  `json:"quota_tokens"`
		} `json:"items"`
	}
	if err := json.Unmarshal(resp, &minted); err != nil || len(minted.Items) != 1 {
		t.Fatalf("mint response = %s", resp)
	}
	code := minted.Items[0].Code

	// Baseline quota via credit_summary.
	before := getCreditSummary(t, serverURL, keyToken)

	// Redeem with the downstream key.
	redeemReq, _ := http.NewRequest(http.MethodPost, serverURL+"/v1/redemption/redeem", strings.NewReader(
		fmt.Sprintf(`{"code":"%s"}`, code),
	))
	redeemReq.Header.Set("Authorization", "Bearer "+keyToken)
	redeemResp, err := http.DefaultClient.Do(redeemReq)
	if err != nil {
		t.Fatal(err)
	}
	defer redeemResp.Body.Close()
	redeemBody, _ := io.ReadAll(redeemResp.Body)
	if redeemResp.StatusCode != http.StatusOK {
		t.Fatalf("redeem status = %d body=%s", redeemResp.StatusCode, redeemBody)
	}
	var redeemOut struct {
		QuotaTokens int64 `json:"quota_tokens"`
	}
	if err := json.Unmarshal(redeemBody, &redeemOut); err != nil || redeemOut.QuotaTokens != 250_000 {
		t.Fatalf("redeem body = %s", redeemBody)
	}

	// Quota grew by exactly the voucher amount.
	after := getCreditSummary(t, serverURL, keyToken)
	if after.Available != before.Available+250_000 {
		t.Fatalf("quota before=%d after=%d, want +250000", before.Available, after.Available)
	}

	// The same code must fail on the second redeem.
	redeemReq2, _ := http.NewRequest(http.MethodPost, serverURL+"/v1/redemption/redeem", strings.NewReader(
		fmt.Sprintf(`{"code":"%s"}`, code),
	))
	redeemReq2.Header.Set("Authorization", "Bearer "+keyToken)
	redeemResp2, err := http.DefaultClient.Do(redeemReq2)
	if err != nil {
		t.Fatal(err)
	}
	defer redeemResp2.Body.Close()
	if redeemResp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("second redeem status = %d, want 400", redeemResp2.StatusCode)
	}

	// Admin list shows it redeemed.
	adminList := getJSON(t, serverURL+"/admin/redemption-codes")
	items := adminList["items"].([]any)
	first := items[0].(map[string]any)
	if first["redeemed_by_key_id"].(float64) == 0 {
		t.Fatal("code should be marked redeemed")
	}
}

func getCreditSummary(t *testing.T, serverURL, keyToken string) struct{ Available int64 } {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, serverURL+"/v1/dashboard/billing/credit_summary", nil)
	req.Header.Set("Authorization", "Bearer "+keyToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var out struct {
		TotalAvailable float64 `json:"total_available"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("credit summary = %s", body)
	}
	return struct{ Available int64 }{Available: int64(out.TotalAvailable)}
}

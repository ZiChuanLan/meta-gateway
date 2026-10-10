package httpapi_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestAdminRatioAcceptsEncodedModelName: a model name carries reserved
// characters (cn:auto), so the console percent-encodes it in the path. The
// handler has to decode it — storing the escaped form would create a second,
// unreachable model entry.
func TestAdminRatioAcceptsEncodedModelName(t *testing.T) {
	server := newLimitTestServer(t, limitTestConfig())

	assertStatus(t, http.MethodPut, server.URL+"/admin/ratios/cn%3Aauto", "admin-test", []byte(`{"ratio":2.5}`), http.StatusOK)
	list := assertStatus(t, http.MethodGet, server.URL+"/admin/ratios", "admin-test", nil, http.StatusOK)

	var rows []struct {
		Model string  `json:"model"`
		Ratio float64 `json:"ratio"`
	}
	if err := json.Unmarshal(list, &rows); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range rows {
		if row.Model == "cn:auto" && row.Ratio == 2.5 {
			found = true
		}
	}
	if !found {
		t.Fatalf("the ratio was not stored under the decoded name: %s", list)
	}
	if strings.Contains(string(list), "%3A") {
		t.Fatalf("the escaped name was stored verbatim: %s", list)
	}
}

package httpapi_test

import (
	"encoding/json"
	"net/url"
	"testing"
)

// The q parameter must reach the store and produce a valid MATCH expression.
// A syntax error (or a missing FTS table) would surface as a 500 on the logs
// page, so even an empty result set is worth asserting on.
func TestProxyLogsFullTextQueryParam(t *testing.T) {
	upstream := mockOpenAIUpstream(t)
	defer upstream.Close()
	serverURL, _, _ := setupRelay(t, upstream.URL, "openai-compatible")

	for _, q := range []string{"gemini", "gemini flash", `"`, "*", "AND OR", "req-1"} {
		body := get(t, serverURL+"/admin/proxy-logs?limit=10&q="+url.QueryEscape(q))
		var list struct {
			Items []map[string]any `json:"items"`
		}
		if err := json.Unmarshal(body, &list); err != nil {
			t.Fatalf("q=%q: %v (%s)", q, err, body)
		}
	}
}

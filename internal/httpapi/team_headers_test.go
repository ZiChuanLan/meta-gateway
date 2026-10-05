package httpapi

import (
	"net/http"
	"testing"
)

func TestClientHeadersExcludeGatewaySessionMaterial(t *testing.T) {
	headers := http.Header{
		"Authorization":       {"Bearer private"},
		"Cookie":              {"mg_team_session=private"},
		"X-Meta-Csrf":         {"private"},
		"X-Api-Key":           {"private"},
		"X-Goog-Api-Key":      {"private"},
		"Proxy-Authorization": {"private"},
		"X-Application":       {"keep"},
		"Anthropic-Version":   {"2023-06-01"},
	}
	got := clientHeaders(headers)
	if len(got) != 2 || got["X-Application"] != "keep" || got["Anthropic-Version"] != "2023-06-01" {
		t.Fatalf("unexpected forwarded metadata: keys=%v", func() []string {
			keys := []string{}
			for key := range got {
				keys = append(keys, key)
			}
			return keys
		}())
	}
}

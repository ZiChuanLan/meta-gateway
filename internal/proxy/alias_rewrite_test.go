package proxy

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
)

// The body rewrite and the health/billing key must name the same model. They
// used to be two parsers, and only one of them trimmed: a member mapping stored
// as {"real":" gpt-4o "} sent the padded name upstream (a 404 on most
// providers) while the proxy log, the model-not-found blacklist and the price
// fallback all recorded "gpt-4o".
func TestRewriteModelNameUsesTheSharedExtractor(t *testing.T) {
	for _, test := range []struct {
		name    string
		mapping string
		want    string
	}{
		{"plain", `{"real":"upstream-4o"}`, "upstream-4o"},
		{"padded member mapping", `{"real":"  upstream-4o  "}`, "upstream-4o"},
		{"padded mapping document", `  {"real":"upstream-4o"}  `, "upstream-4o"},
		{"no real", `{}`, "gpt-4o"},
		{"empty real", `{"real":"   "}`, "gpt-4o"},
		{"malformed", `{"real":`, "gpt-4o"},
		{"wrong type", `{"real":7}`, "gpt-4o"},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := []byte(`{"model":"gpt-4o","messages":[]}`)
			got := rewriteModelName(body, "gpt-4o", test.mapping)
			var payload map[string]json.RawMessage
			if err := json.Unmarshal(got, &payload); err != nil {
				t.Fatalf("rewritten body is not JSON: %s", got)
			}
			var model string
			if err := json.Unmarshal(payload["model"], &model); err != nil {
				t.Fatalf("model field unreadable: %s", got)
			}
			if model != test.want {
				t.Fatalf("model=%q, want %q", model, test.want)
			}
			// The extractor is the contract: whatever it returns is what the
			// log, the blacklist and the price fallback will key on.
			if real := domain.MemberRealModel(test.mapping); real != "" && real != test.want {
				t.Fatalf("extractor=%q, rewrite used %q", real, test.want)
			}
		})
	}
}

func TestRewriteModelNameLeavesUnrelatedBodiesAlone(t *testing.T) {
	mapping := `{"real":"upstream-4o"}`
	for _, test := range []struct {
		name           string
		body           string
		requestedModel string
		usesMapping    bool
	}{
		{name: "other model", body: `{"model":"other"}`, requestedModel: "gpt-4o", usesMapping: true},
		{name: "no model field", body: `{"messages":[]}`, requestedModel: "gpt-4o", usesMapping: true},
		{name: "not json", body: `gpt-4o`, requestedModel: "gpt-4o", usesMapping: true},
		{name: "empty mapping", body: `{"model":"gpt-4o"}`, requestedModel: "gpt-4o"},
		{name: "empty body", body: ``, requestedModel: "gpt-4o", usesMapping: true},
		{name: "no requested model", body: `{"model":"gpt-4o"}`, usesMapping: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			used := ""
			if test.usesMapping {
				used = mapping
			}
			got := string(rewriteModelName([]byte(test.body), test.requestedModel, used))
			if got != test.body {
				t.Fatalf("body rewritten: %q", got)
			}
		})
	}
}

// Multipart uploads take the same value from the same extractor; the padded
// mapping must not reach the upstream form field either.
func TestRewriteModelNameTrimsInMultipart(t *testing.T) {
	requested, real := "gpt-image-2", "gpt-image-2-upstream"
	body := "--b\r\nContent-Disposition: form-data; name=\"model\"\r\n\r\n" +
		requested + "\r\n" +
		"--b\r\nContent-Disposition: form-data; name=\"prompt\"\r\n\r\ncat\r\n" +
		"--b--\r\n"
	rewritten := string(rewriteModelName([]byte(body), requested, `{"real":"   `+real+`   "}`, "multipart/form-data; boundary=b"))
	if !strings.Contains(rewritten, real) {
		t.Fatalf("multipart model not rewritten: %q", rewritten)
	}
	if strings.Contains(rewritten, "   "+real) {
		t.Fatalf("multipart kept the padding: %q", rewritten)
	}
	if !strings.Contains(rewritten, "cat") {
		t.Fatalf("unrelated form field lost: %q", rewritten)
	}
}

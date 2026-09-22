package adapters_test

import (
	"testing"

	"github.com/lan/meta-gateway/internal/adapters"
)

// SplitEndpointBaseURL is what lets an operator paste a complete upstream
// endpoint as the base URL, new-api style. The dangerous half is the negative
// case: every cn/intl provider preset that ends in an API-version segment
// (`/api/paas/v4`, `/v1beta`, `/openai/v1`) must be left alone, or the provider's
// whole API root would be relocated.
func TestSplitEndpointBaseURL(t *testing.T) {
	cases := []struct {
		name         string
		in           string
		wantBase     string
		wantOverride string
	}{
		{
			name:         "complete endpoint is split",
			in:           "https://api.typesafe.ai/v1/systemone",
			wantBase:     "https://api.typesafe.ai",
			wantOverride: "/v1/systemone",
		},
		{
			// Ambiguous on purpose, and resolved toward the mount prefix. A path
			// with neither a version segment nor a known endpoint tail cannot be
			// told apart from a mount point, and guessing "endpoint" silently broke
			// the mount-prefix case (E2E channel at `/ok` lost `/v1/chat/completions`).
			// The endpoint preview shows the operator the /v1 join, so a genuine
			// endpoint of this shape is a visible, one-field fix.
			name:     "path without a version or known tail stays a mount prefix",
			in:       "https://host.example/api/invoke",
			wantBase: "https://host.example/api/invoke",
		},
		{
			name:     "mount prefix /ok is untouched",
			in:       "http://mock-upstream:8080/ok",
			wantBase: "http://mock-upstream:8080/ok",
		},
		{
			name:     "mount prefix /prefix is untouched",
			in:       "https://api.example.com/prefix",
			wantBase: "https://api.example.com/prefix",
		},
		{
			name:         "perplexity's documented endpoint is split (no /v1 base exists)",
			in:           "https://api.perplexity.ai/chat/completions",
			wantBase:     "https://api.perplexity.ai",
			wantOverride: "/chat/completions",
		},
		{
			name:         "a mount prefix before a known tail is kept whole",
			in:           "https://host.example/gateway/v1/chat/completions",
			wantBase:     "https://host.example",
			wantOverride: "/gateway/v1/chat/completions",
		},
		{
			name:     "zhipu version root is untouched",
			in:       "https://open.bigmodel.cn/api/paas/v4",
			wantBase: "https://open.bigmodel.cn/api/paas/v4",
		},
		{
			name:     "trailing /v1 is untouched",
			in:       "https://api.deepseek.com/v1",
			wantBase: "https://api.deepseek.com/v1",
		},
		{
			name:     "gemini v1beta is untouched",
			in:       "https://generativelanguage.googleapis.com/v1beta",
			wantBase: "https://generativelanguage.googleapis.com/v1beta",
		},
		{
			name:     "groq /openai/v1 is untouched",
			in:       "https://api.groq.com/openai/v1",
			wantBase: "https://api.groq.com/openai/v1",
		},
		{
			name:     "bare host is untouched",
			in:       "https://api.example.com",
			wantBase: "https://api.example.com",
		},
		{
			name:     "trailing slash is normalized away",
			in:       "https://api.example.com/",
			wantBase: "https://api.example.com",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base, override, err := adapters.SplitEndpointBaseURL(tc.in)
			if err != nil {
				t.Fatalf("SplitEndpointBaseURL(%q): %v", tc.in, err)
			}
			if base != tc.wantBase || override != tc.wantOverride {
				t.Fatalf("SplitEndpointBaseURL(%q) = (%q, %q), want (%q, %q)", tc.in, base, override, tc.wantBase, tc.wantOverride)
			}
		})
	}

	if _, _, err := adapters.SplitEndpointBaseURL("not a url"); err == nil {
		t.Fatal("a malformed base URL must be reported, not silently kept")
	}
}

func TestEndpointOverrideURL(t *testing.T) {
	const base = "https://api.typesafe.ai"

	got, err := adapters.EndpointOverrideURL(base, "v1/systemone", "")
	if err != nil || got != "https://api.typesafe.ai/v1/systemone" {
		t.Fatalf("path override = %q err=%v", got, err)
	}
	got, err = adapters.EndpointOverrideURL(base, "", "https://api.typesafe.ai/v1/other")
	if err != nil || got != "https://api.typesafe.ai/v1/other" {
		t.Fatalf("url override = %q err=%v", got, err)
	}
	// The URL form wins over the path form.
	got, err = adapters.EndpointOverrideURL(base, "v1/ignored", "https://api.typesafe.ai/v1/other")
	if err != nil || got != "https://api.typesafe.ai/v1/other" {
		t.Fatalf("url must win over path: %q err=%v", got, err)
	}
	// Query strings never survive: they routinely carry credentials.
	got, err = adapters.EndpointOverrideURL(base, "", "https://api.typesafe.ai/v1/other?api_key=secret#frag")
	if err != nil || got != "https://api.typesafe.ai/v1/other" {
		t.Fatalf("query/fragment must be stripped: %q err=%v", got, err)
	}
	if got, err := adapters.EndpointOverrideURL(base, "", ""); err != nil || got != base {
		t.Fatalf("no override = %q err=%v", got, err)
	}

	// A different host would let a caller send the channel's API key to its own
	// server, so it is a hard error rather than a redirect.
	if _, err := adapters.EndpointOverrideURL(base, "", "https://attacker.example/v1/systemone"); err == nil {
		t.Fatal("cross-host override must be rejected")
	}
	if _, err := adapters.EndpointOverrideURL(base, "..%2Fadmin", ""); err == nil {
		t.Fatal("encoded traversal must be rejected")
	}
	if _, err := adapters.EndpointOverrideURL(base, "a b", ""); err == nil {
		t.Fatal("whitespace in a path must be rejected")
	}
}

func TestSafeURLStripsCredentials(t *testing.T) {
	if got := adapters.SafeURL("https://host.example/v1/x?api_key=secret#f"); got != "https://host.example/v1/x" {
		t.Fatalf("SafeURL = %q", got)
	}
	if got := adapters.SafeURL("https://user:pw@host.example/v1/x"); got != "https://host.example/v1/x" {
		t.Fatalf("SafeURL kept userinfo: %q", got)
	}
	if got := adapters.SafeURL("not a url"); got != "" {
		t.Fatalf("SafeURL(%q) = %q, want empty", "not a url", got)
	}
}


package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientFamilyClassification(t *testing.T) {
	cases := map[string]string{
		"Claude-Code/1.0":                        "claude-code",
		"claude-code/1.0 (cli)":                  "claude-code",
		"ClaudeDesktop/1.0.4":                    "claude-desktop",
		"CherryStudio/1.0.3":                     "cherry-studio",
		"Cherry Studio/0.9.22":                   "cherry-studio",
		"LobeChat/1.0.0":                         "lobe",
		"Chatbox/1.0.0":                          "chatbox",
		"NextChat/2.14":                          "nextchat",
		"OpenCat/1.0":                            "opencat",
		"Copilot/1.0":                            "copilot",
		"Cursor/0.40":                            "cursor",
		"Windsurf/1.0":                           "windsurf",
		"Anthropic/2.0":                          "anthropic",
		"OpenAI/Python 1.30.0":                   "openai-python",
		"OpenAI-API/1.0 ChatGPT/1.0":             "openai",
		"curl/8.5.0":                             "cli",
		"python-requests/2.31":                   "python",
		"node-fetch/3.0":                         "node",
		"PostmanRuntime/7.36":                    "postman",
		"Insomnia/2023.5":                        "insomnia",
		"Mozilla/5.0 (Windows NT 10.0; Win64)":   "browser",
		"":                                       "unknown",
	}
	for ua, want := range cases {
		if got := ClientFamily(ua); got != want {
			t.Fatalf("ClientFamily(%q) = %q, want %q", ua, got, want)
		}
	}
}

func TestClientFamilyOf(t *testing.T) {
	newReq := func(ua string) *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		if ua != "" {
			req.Header.Set("User-Agent", ua)
		}
		return req
	}
	// UA classification unchanged.
	if got := ClientFamilyOf(newReq("Claude-Code/1.0")); got != "claude-code" {
		t.Fatalf("ua claude-code → %q", got)
	}
	if got := ClientFamilyOf(newReq("")); got != "unknown" {
		t.Fatalf("empty ua → %q", got)
	}
	// X-Meta-Client header overrides UA.
	req := newReq("curl/8.0")
	req.Header.Set("X-Meta-Client", "cursor")
	if got := ClientFamilyOf(req); got != "cursor" {
		t.Fatalf("declared cursor → %q", got)
	}
	// Unknown declared family is ignored, falls back to UA.
	req = newReq("curl/8.0")
	req.Header.Set("X-Meta-Client", "hacker-tool")
	if got := ClientFamilyOf(req); got != "cli" {
		t.Fatalf("invalid declared header must fall back: %q", got)
	}
	// Anthropic protocol signal: anthropic-version + /v1/messages.
	req = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req.Header.Set("anthropic-version", "2023-06-01")
	if got := ClientFamilyOf(req); got != "anthropic" {
		t.Fatalf("anthropic protocol → %q", got)
	}
	// anthropic-version on a chat path is not enough.
	req = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("anthropic-version", "2023-06-01")
	if got := ClientFamilyOf(req); got != "unknown" {
		t.Fatalf("anthropic-version alone → %q", got)
	}
}

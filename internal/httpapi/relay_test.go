package httpapi

import (
	"bytes"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestClientFamilyClassification(t *testing.T) {
	cases := map[string]string{
		"pi/1.2.3 (win32; bun/1.1.0; x64)":       "pi",
		"pi (win32 10.0.26300; x64)":             "pi",
		"pi-coding-agent":                        "pi",
		"Claude-Code/1.0":                        "claude-code",
		"claude-code/1.0 (cli)":                  "claude-code",
		"claude-cli/2.1.207 (external, sdk-cli)": "claude-code",
		"ClaudeDesktop/1.0.4":                    "claude-desktop",
		"CherryStudio/1.0.3":                     "cherry-studio",
		"Cherry Studio/0.9.22":                   "cherry-studio",
		"LobeChat/1.0.0":                         "lobe",
		"Chatbox/1.0.0":                          "chatbox",
		"NextChat/2.14":                          "nextchat",
		"OpenCat/1.0":                            "opencat",
		"Kilo-Code/1.0":                          "kilo-code",
		"Copilot/1.0":                            "copilot",
		"Cursor/0.40":                            "cursor",
		"Windsurf/1.0":                           "windsurf",
		// The coding CLIs, by the User-Agent each one actually sends.
		"codex_exec/0.155.1 (Windows 10.0.26300; x86_64) pebrel/2.2.0_2fde458":           "codex",
		"codex_cli_rs/0.155.1 (Mac OS 15.0; arm64) iTerm.app/3.5.14":                     "codex",
		"GeminiCLI/0.62.0/gemini-3-pro (win32; x64; interactive)":                        "gemini-cli",
		"opencode/1.18.34 ai-sdk/provider-utils/4.0.23 runtime/bun/1.3.14":               "opencode",
		"deepseek-harness/0.2.0-rc.2 (+https://github.com/deepseek-ai/deepseek-harness)": "dsh",
		// The AI-Agent product — the cross-tool convention for a client to say
		// what it is. It wins over the signatures, including a generic one:
		// `node/...` on its own would be filed as node.
		"node/22.11.0 AI-Agent/kiro":           "kiro",
		"amp/1.0 AI-Agent/amp runtime/bun/1.3": "amp",
		"foo/1.2.3 AI-Agent/codex":             "codex",
		// A name outside the known set is not trusted as a bucket key of its own.
		"foo/1.2.3 AI-Agent/made-up-cli": "unknown",
		// The editors/extensions that lead with `<tool>/<version>`, from their
		// own sources (see the doc comment on ClientFamily for the pointers).
		"Cline/3.39.2":           "cline",
		"cline-vscode-extension": "cline",
		"RooCode/3.22.6":         "roo-code",
		"roo-code/3.22.6 (win32 10.0.26300; x64) node/22.11.0": "roo-code",
		"goose/1.31.0 (+https://github.com/aaif-goose/goose)":  "goose",
		"LibreChat/1.0":          "librechat",
		"open-webui/openference": "open-webui",
		// Self-hosted front ends with no UA of their own: Open WebUI sends
		// aiohttp's default, and everything on httpx (LiteLLM, Dify,
		// LangChain) sends `python-httpx/…`. Both are Python clients, the same
		// granularity the list already had for `python-requests`.
		"Python/3.11 aiohttp/3.9.1":            "python",
		"python-httpx/0.27.0":                  "python",
		"Anthropic/2.0":                        "anthropic",
		"OpenAI/Python 1.30.0":                 "openai-python",
		"OpenAI-API/1.0 ChatGPT/1.0":           "openai",
		"curl/8.5.0":                           "cli",
		"python-requests/2.31":                 "python",
		"node-fetch/3.0":                       "node",
		"PostmanRuntime/7.36":                  "postman",
		"Insomnia/2023.5":                      "insomnia",
		"Mozilla/5.0 (Windows NT 10.0; Win64)": "browser",
		"":                                     "unknown",
		// Unidentified is not the same as a browser: claiming "browser" for
		// every unrecognized agent both asserted something false and threw away
		// the one usable fact. The raw User-Agent is stored on the row next to
		// this bucket, so these rows stay answerable.
		"api/v1/status":      "unknown",
		"Zalando/1.0":        "unknown",
		"Go-http-client/1.1": "unknown",
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
	// The protocol fallback only runs for a UA the family list cannot place —
	// it used to be dead code, because every UA (any non-empty one) came back
	// as "browser" before it could be reached.
	req = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("User-Agent", "my-private-fork/0.1")
	if got := ClientFamilyOf(req); got != "anthropic" {
		t.Fatalf("unknown ua + anthropic protocol → %q", got)
	}
	// X-Meta-Client stays the first word: it is the channel this gateway
	// documents for operators, and it accepts names from the same set as the
	// AI-Agent product does.
	req = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("X-Meta-Client", "goose")
	req.Header.Set("User-Agent", "foo/1.0 AI-Agent/codex")
	if got := ClientFamilyOf(req); got != "goose" {
		t.Fatalf("X-Meta-Client did not win over AI-Agent: %q", got)
	}
	// The console labels its own workbench traffic, so the operator can tell a
	// test run of theirs from an external browser holding the same key.
	req = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("X-Meta-Client", "console")
	if got := ClientFamilyOf(req); got != "console" {
		t.Fatalf("console header → %q", got)
	}
}

func TestClientUAStringIsSanitizedAndCapped(t *testing.T) {
	newReq := func(ua string) *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		if ua != "" {
			req.Header.Set("User-Agent", ua)
		}
		return req
	}
	if got := clientUAString(newReq("")); got != "" {
		t.Fatalf("empty ua → %q", got)
	}
	if got := clientUAString(newReq("  pi/1.0  ")); got != "pi/1.0" {
		t.Fatalf("trim → %q", got)
	}
	// A header value is client-supplied: it must not carry control characters
	// into the log table, the console's CSV export or the JSON API.
	if got := clientUAString(newReq("pi/1.0\r\nX-Injected: 1")); strings.ContainsAny(got, "\r\n") || got != "pi/1.0X-Injected: 1" {
		t.Fatalf("control characters survived: %q", got)
	}
	long := "pi/1.0 (" + strings.Repeat("x", maxClientUALen*2) + ")"
	if got := clientUAString(newReq(long)); len([]rune(got)) != maxClientUALen {
		t.Fatalf("cap → %d runes, want %d", len([]rune(got)), maxClientUALen)
	}
	// Capping by rune means a multi-byte character is never cut in half.
	wide := strings.Repeat("⇥", maxClientUALen+10)
	if got := clientUAString(newReq(wide)); len([]rune(got)) != maxClientUALen || !utf8.ValidString(got) {
		t.Fatalf("wide cap → %q", got)
	}
}

func makeMultipartBody(t *testing.T, fields map[string]string, files map[string]string) ([]byte, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for name, value := range fields {
		if err := writer.WriteField(name, value); err != nil {
			t.Fatal(err)
		}
	}
	for name, value := range files {
		part, err := writer.CreateFormFile(name, name+".txt")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(part, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return body.Bytes(), writer.FormDataContentType()
}

func TestExtractMultipartModelParsesOnlyScalarModelField(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	file, err := writer.CreateFormFile("file", "image.png")
	if err != nil {
		t.Fatal(err)
	}
	// This byte sequence fooled the old raw-body scanner when the upload
	// preceded the real form field.
	if _, err := io.WriteString(file, "image-bytes\r\nname=\"model\"\r\n\r\nallowed-model"); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("model", "  blocked-model  "); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	model, err := extractMultipartModel(body.Bytes(), writer.FormDataContentType())
	if err != nil || model != "blocked-model" {
		t.Fatalf("model=%q err=%v", model, err)
	}
}

func TestExtractMultipartModelRejectsMissingDuplicateAndOversized(t *testing.T) {
	// Build the cases separately so each content type keeps the matching
	// boundary generated by its writer.
	body, ct := makeMultipartBody(t, map[string]string{"prompt": "x"}, nil)
	if _, err := extractMultipartModel(body, ct); !errors.Is(err, errMultipartModelMissing) {
		t.Fatalf("missing model err=%v", err)
	}

	var duplicate bytes.Buffer
	w := multipart.NewWriter(&duplicate)
	_ = w.WriteField("model", "one")
	_ = w.WriteField("model", "two")
	_ = w.Close()
	if _, err := extractMultipartModel(duplicate.Bytes(), w.FormDataContentType()); !errors.Is(err, errMultipartModelDuplicate) {
		t.Fatalf("duplicate model err=%v", err)
	}

	large := strings.Repeat("x", maxMultipartModelBytes+1)
	body, ct = makeMultipartBody(t, map[string]string{"model": large}, nil)
	if _, err := extractMultipartModel(body, ct); !errors.Is(err, errMultipartModelTooLarge) {
		t.Fatalf("oversized model err=%v", err)
	}
}

func TestExtractMultipartModelRejectsMalformedContentType(t *testing.T) {
	if _, err := extractMultipartModel([]byte("anything"), "multipart/form-data"); !errors.Is(err, errMultipartInvalid) {
		t.Fatalf("malformed content type err=%v", err)
	}
}

func TestExtractMultipartModelPreservesBodyBytes(t *testing.T) {
	body, contentType := makeMultipartBody(t, map[string]string{"model": "gpt-image-1"}, nil)
	want := append([]byte(nil), body...)
	if _, err := extractMultipartModel(body, contentType); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, want) {
		t.Fatal("multipart parsing mutated forwarded body")
	}
}

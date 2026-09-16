package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// corsProbe wraps a recording handler so a test can assert both what the
// middleware granted and whether the request reached the inner handler.
func corsProbe(t *testing.T, allowed []string, method, path string, headers map[string]string) (*httptest.ResponseRecorder, bool) {
	t.Helper()
	var reached bool
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})
	request := httptest.NewRequest(method, path, nil)
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()
	corsMiddleware("/v1", allowed)(next).ServeHTTP(recorder, request)
	return recorder, reached
}

func corsPreflightHeaders(origin string) map[string]string {
	return map[string]string{
		"Origin":                         origin,
		"Access-Control-Request-Method":  "POST",
		"Access-Control-Request-Headers": "authorization, content-type",
	}
}

// The zero-config default: an empty allowlist opens /v1 to any origin.
func TestCORSPreflightOpensAnyOriginWhenUnconfigured(t *testing.T) {
	for _, allowed := range [][]string{nil, {}, {"*"}} {
		recorder, reached := corsProbe(t, allowed, http.MethodOptions, "/v1/chat/completions", corsPreflightHeaders("https://anything.example"))

		if reached {
			t.Fatalf("allowlist %v: preflight must not reach the inner handler", allowed)
		}
		if recorder.Code != http.StatusNoContent {
			t.Fatalf("allowlist %v: status = %d, want 204", allowed, recorder.Code)
		}
		if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "*" {
			t.Fatalf("allowlist %v: Allow-Origin = %q, want *", allowed, got)
		}
		if got := recorder.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(got, "POST") {
			t.Fatalf("allowlist %v: Allow-Methods = %q, want it to include POST", allowed, got)
		}
		// Preflight must not claim credentials while the origin is a wildcard.
		if got := recorder.Header().Get("Access-Control-Allow-Credentials"); got != "" {
			t.Fatalf("allowlist %v: Allow-Credentials = %q, want empty with a wildcard origin", allowed, got)
		}
		if got := recorder.Header().Get("Access-Control-Max-Age"); got != corsMaxAge {
			t.Fatalf("allowlist %v: Max-Age = %q, want %q", allowed, got, corsMaxAge)
		}
	}
}

// The browser has to be told which response headers it may read; none of the
// three the gateway sets are CORS-safelisted.
func TestCORSExposesBackoffAndRequestIDHeaders(t *testing.T) {
	recorder, reached := corsProbe(t, nil, http.MethodPost, "/v1/chat/completions",
		map[string]string{"Origin": "https://app.example"})

	if !reached {
		t.Fatal("a non-preflight request must reach the inner handler")
	}
	exposed := recorder.Header().Get("Access-Control-Expose-Headers")
	for _, name := range []string{"retry-after", "x-request-id", "x-meta-image-shim"} {
		if !strings.Contains(exposed, name) {
			t.Fatalf("Expose-Headers = %q, want it to include %q", exposed, name)
		}
	}
	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("Allow-Origin = %q, want *", got)
	}
}

// A configured allowlist switches to echo mode and must add Vary: Origin so a
// shared cache cannot hand one origin's grant to a different origin.
func TestCORSAllowlistEchoesOriginAndVaries(t *testing.T) {
	allowed := []string{"https://app.example.com"}
	recorder, reached := corsProbe(t, allowed, http.MethodOptions, "/v1/models",
		corsPreflightHeaders("https://app.example.com"))

	if reached {
		t.Fatal("preflight must not reach the inner handler")
	}
	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Fatalf("Allow-Origin = %q, want the echoed origin", got)
	}
	if got := recorder.Header().Values("Vary"); len(got) == 0 || !strings.Contains(strings.Join(got, ","), "Origin") {
		t.Fatalf("Vary = %v, want it to include Origin", got)
	}
}

func TestCORSAllowlistMatchesSubdomainPattern(t *testing.T) {
	recorder, _ := corsProbe(t, []string{"*.example.com"}, http.MethodOptions, "/v1/models",
		corsPreflightHeaders("https://chat.example.com"))

	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "https://chat.example.com" {
		t.Fatalf("Allow-Origin = %q, want the subdomain to be admitted", got)
	}
	// A pattern must not leak past its own suffix.
	if got, _ := corsProbe(t, []string{"*.example.com"}, http.MethodOptions, "/v1/models",
		corsPreflightHeaders("https://example.com.evil.test")); got.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("Allow-Origin = %q, want empty for a lookalike host", got.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestCORSAllowlistWithholdsGrantFromUnknownOrigin(t *testing.T) {
	// A preflight from an unlisted origin is answered without a grant.
	recorder, reached := corsProbe(t, []string{"https://app.example.com"}, http.MethodOptions, "/v1/models",
		corsPreflightHeaders("https://evil.test"))
	if reached {
		t.Fatal("preflight must not reach the inner handler")
	}
	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Allow-Origin = %q, want empty", got)
	}
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 so the browser reports a denial rather than a 405", recorder.Code)
	}

	// A real request from an unlisted origin still runs, just ungranted; the
	// browser is what blocks it.
	recorder, reached = corsProbe(t, []string{"https://app.example.com"}, http.MethodPost, "/v1/chat/completions",
		map[string]string{"Origin": "https://evil.test"})
	if !reached {
		t.Fatal("a non-preflight request must reach the inner handler")
	}
	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Allow-Origin = %q, want empty", got)
	}
}

// CORS only governs the downstream surface. The admin API stays same-origin.
func TestCORSLeavesOtherSurfacesUntouched(t *testing.T) {
	for _, path := range []string{"/admin/channels", "/console", "/healthz"} {
		recorder, reached := corsProbe(t, nil, http.MethodOptions, path, corsPreflightHeaders("https://app.example"))
		if !reached {
			t.Fatalf("%s: must not be short-circuited by CORS", path)
		}
		if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("%s: Allow-Origin = %q, want empty", path, got)
		}
	}
}

// A bare OPTIONS carries no Access-Control-Request-Method, so it is not a
// preflight and must keep flowing to the router to be answered as usual.
func TestCORSBareOptionsIsNotAPreflight(t *testing.T) {
	recorder, reached := corsProbe(t, nil, http.MethodOptions, "/v1/models",
		map[string]string{"Origin": "https://app.example"})

	if !reached {
		t.Fatal("a bare OPTIONS must not be treated as a preflight")
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want the inner handler's 200", recorder.Code)
	}
}

// Non-browser callers send no Origin and must be left alone entirely.
func TestCORSIgnoresRequestsWithoutOrigin(t *testing.T) {
	recorder, reached := corsProbe(t, nil, http.MethodPost, "/v1/chat/completions", nil)

	if !reached {
		t.Fatal("a request without Origin must reach the inner handler")
	}
	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Allow-Origin = %q, want empty", got)
	}
}

// Image edits are multipart POSTs with an Authorization header, so the browser
// always preflights them and the echoed Allow-Headers has to survive.
func TestCORSPreflightEchoesRequestedHeadersForImageEdits(t *testing.T) {
	recorder, _ := corsProbe(t, nil, http.MethodOptions, "/v1/images/edits", map[string]string{
		"Origin":                         "https://studio.example",
		"Access-Control-Request-Method":  "POST",
		"Access-Control-Request-Headers": "authorization, content-type, anthropic-version",
	})

	allowHeaders := recorder.Header().Get("Access-Control-Allow-Headers")
	for _, name := range []string{"authorization", "content-type", "anthropic-version"} {
		if !strings.Contains(allowHeaders, name) {
			t.Fatalf("Allow-Headers = %q, want it to include %q", allowHeaders, name)
		}
	}
}

// Without a stated request-header list the preflight still has to be answerable.
func TestCORSPreflightFallsBackWhenHeadersOmitted(t *testing.T) {
	recorder, _ := corsProbe(t, nil, http.MethodOptions, "/v1/models", map[string]string{
		"Origin":                        "https://app.example",
		"Access-Control-Request-Method": "GET",
	})

	if got := recorder.Header().Get("Access-Control-Allow-Headers"); got != corsFallbackAllowHeaders {
		t.Fatalf("Allow-Headers = %q, want the fallback list", got)
	}
}

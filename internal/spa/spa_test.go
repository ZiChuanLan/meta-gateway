package spa

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// The tests drive a MapFS rather than an embedded bundle so they pin the
// behaviour of the handler itself, independent of whatever the current
// frontend build happens to contain.
func testFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":            &fstest.MapFile{Data: []byte(`<div id="root"></div>`)},
		"assets/app-abc123.js":  &fstest.MapFile{Data: []byte("console.log(1)")},
		"assets/app-abc123.css": &fstest.MapFile{Data: []byte("body{}")},
	}
}

func serve(t *testing.T, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	Handler(testFS(), "/console/", "index.html").ServeHTTP(recorder, httptest.NewRequest(method, target, nil))
	return recorder
}

func TestHandlerServesShellWithFallback(t *testing.T) {
	// The prefix root, the prefix without its trailing slash, and a deep
	// client-side route must all resolve to the shell.
	for _, target := range []string{"/console/", "/console", "/console/usage", "/console/deep/link"} {
		recorder := serve(t, http.MethodGet, target)
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s: code=%d", target, recorder.Code)
		}
		if !strings.Contains(recorder.Body.String(), `<div id="root"></div>`) {
			t.Fatalf("%s: body=%q", target, recorder.Body.String())
		}
		if got := recorder.Header().Get("Cache-Control"); got != "no-cache" {
			t.Fatalf("%s: cache-control=%q", target, got)
		}
	}
}

func TestHandlerServesAssetsAndHead(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		recorder := serve(t, method, "/console/assets/app-abc123.js")
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s: code=%d", method, recorder.Code)
		}
		if got := recorder.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
			t.Fatalf("%s: cache-control=%q", method, got)
		}
		if got := recorder.Header().Get("Content-Type"); !strings.Contains(got, "javascript") {
			t.Fatalf("%s: content-type=%q", method, got)
		}
		if method == http.MethodHead && recorder.Body.Len() != 0 {
			t.Fatalf("HEAD returned %d body bytes", recorder.Body.Len())
		}
	}
}

// A missing file with an extension stays a 404. Answering it with the shell
// would turn a broken asset link into a confusing MIME error in the browser.
func TestHandlerDoesNotFallbackForMissingAssets(t *testing.T) {
	for _, target := range []string{"/console/assets/missing.js", "/console/assets/missing.css"} {
		recorder := serve(t, http.MethodGet, target)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("%s: code=%d, want 404", target, recorder.Code)
		}
	}
}

func TestHandlerRejectsMutation(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		recorder := serve(t, method, "/console/")
		if recorder.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s: code=%d, want 405", method, recorder.Code)
		}
		if got := recorder.Header().Get("Allow"); got != "GET, HEAD" {
			t.Fatalf("%s: allow=%q", method, got)
		}
	}
}

// Two properties of the traversal guard, both pinned because a future refactor
// of the path handling could quietly drop either:
//
//   - a path that stays inside the bundle is normalized and served;
//   - a path that climbs above the bundle is re-rooted by path.Clean("/"+name),
//     so Stat misses and nothing outside the bundle becomes addressable.
//
// The second property is observable only in what is *not* returned. With an
// extension the miss is a hard 404; without one the SPA fallback answers with
// the shell — which is itself a file from inside the bundle, never the escaped
// target. Asserting both keeps the distinction explicit.
func TestHandlerAnchorsPathsInsideTheBundle(t *testing.T) {
	if got := serve(t, http.MethodGet, "/console/assets/../assets/app-abc123.js"); got.Code != http.StatusOK {
		t.Fatalf("in-bundle traversal: code=%d, want 200", got.Code)
	}

	escaped := serve(t, http.MethodGet, "/console/../../etc/passwd.txt")
	if escaped.Code != http.StatusNotFound {
		t.Fatalf("escaping traversal: code=%d, want 404", escaped.Code)
	}

	shell := serve(t, http.MethodGet, "/console/../../etc/passwd")
	if shell.Code != http.StatusOK {
		t.Fatalf("escaping traversal without extension: code=%d, want the bundle shell", shell.Code)
	}
	if !strings.Contains(shell.Body.String(), `<div id="root"></div>`) {
		t.Fatalf("escaping traversal served %q, want the bundle shell", shell.Body.String())
	}
}

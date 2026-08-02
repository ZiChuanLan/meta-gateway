package webui

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerServesShellAndFallback(t *testing.T) {
	handler := Handler()
	for _, target := range []string{"/console/", "/console/routing"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `<div id="root"></div>`) {
			t.Fatalf("target %s: code=%d body=%q", target, recorder.Code, recorder.Body.String())
		}
		if got := recorder.Header().Get("Cache-Control"); got != "no-cache" {
			t.Fatalf("target %s: cache-control=%q", target, got)
		}
	}
}

func TestHandlerRejectsMutation(t *testing.T) {
	recorder := httptest.NewRecorder()
	Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/console/", nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("code=%d, want %d", recorder.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandlerServesAssetsAndHead(t *testing.T) {
	matches, err := fs.Glob(assets, "dist/assets/*.css")
	if err != nil || len(matches) == 0 {
		t.Fatalf("find embedded stylesheet: matches=%v err=%v", matches, err)
	}
	target := "/console/" + strings.TrimPrefix(matches[0], "dist/")
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		recorder := httptest.NewRecorder()
		Handler().ServeHTTP(recorder, httptest.NewRequest(method, target, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s %s: code=%d", method, target, recorder.Code)
		}
		if got := recorder.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
			t.Fatalf("%s %s: cache-control=%q", method, target, got)
		}
		if method == http.MethodHead && recorder.Body.Len() != 0 {
			t.Fatalf("HEAD %s returned %d body bytes", target, recorder.Body.Len())
		}
	}
}

func TestHandlerDoesNotFallbackForMissingAssets(t *testing.T) {
	recorder := httptest.NewRecorder()
	Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/console/assets/missing.js", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("code=%d, want %d", recorder.Code, http.StatusNotFound)
	}
}

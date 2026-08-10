package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func cpaStatus(t *testing.T, handler http.Handler, baseURL string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/admin/cpa/status?base_url="+urlQueryEscape(baseURL), nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	body := map[string]any{}
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	return rr.Code, body
}

func cpaHandler() http.Handler {
	r := chi.NewRouter()
	admin := chi.NewRouter()
	NewCPAHandler().Register(admin)
	r.Mount("/admin", admin)
	return r
}

func urlQueryEscape(s string) string {
	return strings.ReplaceAll(s, ":", "%3A")
}

func TestCPAStatusRunning(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
			return
		}
		http.NotFound(w, r)
	}))
	defer upstream.Close()

	h := cpaHandler()
	code, body := cpaStatus(t, h, upstream.URL)
	if code != http.StatusOK {
		t.Fatalf("status code = %d, want 200", code)
	}
	if body["running"] != true {
		t.Fatalf("running = %v, want true (body=%v)", body["running"], body)
	}
	if body["base_url"] != upstream.URL {
		t.Fatalf("base_url = %v, want %s", body["base_url"], upstream.URL)
	}
}

func TestCPAStatusNotRunning(t *testing.T) {
	// Port with nothing listening: 127.0.0.1:1 (closed) is reliably refused.
	h := cpaHandler()
	code, body := cpaStatus(t, h, "http://127.0.0.1:1")
	if code != http.StatusOK {
		t.Fatalf("status code = %d, want 200", code)
	}
	if body["running"] != false {
		t.Fatalf("running = %v, want false", body["running"])
	}
	if body["error"] == "" || body["error"] == nil {
		t.Fatalf("expected an error detail for a refused connection, got %v", body["error"])
	}
}

func TestCPAStatusUnhealthyUpstream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer upstream.Close()

	h := cpaHandler()
	_, body := cpaStatus(t, h, upstream.URL)
	if body["running"] != false {
		t.Fatalf("running = %v, want false for 503", body["running"])
	}
	if !strings.Contains(body["error"].(string), "503") {
		t.Fatalf("error = %v, want mention of status 503", body["error"])
	}
}

func TestCPAStatusRejectsNonLoopback(t *testing.T) {
	h := cpaHandler()
	for _, bad := range []string{
		"http://evil.example.com",
		"http://192.168.1.10",
		"http://10.0.0.1:9090",
		"ftp://127.0.0.1",
	} {
		code, _ := cpaStatus(t, h, bad)
		if code != http.StatusBadRequest {
			t.Fatalf("base_url %q -> status %d, want 400", bad, code)
		}
	}
}

func TestCPAStatusDefaultBaseURL(t *testing.T) {
	// No query param: defaults to 127.0.0.1:9090 which is refused in tests.
	code, body := cpaStatus(t, cpaHandler(), "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if body["base_url"] != "http://127.0.0.1:9090" {
		t.Fatalf("default base_url = %v", body["base_url"])
	}
}

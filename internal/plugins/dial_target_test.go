package plugins

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestDialTargetNormalizesPluginURLs(t *testing.T) {
	cases := []struct{ url, want string }{
		{"http://127.0.0.1:44215", "127.0.0.1:44215"},
		{"http://127.0.0.1:44215/v1", "127.0.0.1:44215"},
		{"http://OCH-MG:9300/v1", "och-mg:9300"},
		{"https://wb.example.com", "wb.example.com:443"},
		{"http://sidecar.lan", "sidecar.lan:80"},
		{"http://[::1]:9300", "[::1]:9300"},
		{"", ""},
		{"not a url", ""},
	}
	for _, c := range cases {
		if got := dialTarget(c.url); got != c.want {
			t.Fatalf("dialTarget(%q)=%q want %q", c.url, got, c.want)
		}
	}
}

// The exception is per endpoint: registering one plugin must not make another
// loopback port reachable.
func TestDialAllowedMatchesOnlyRegisteredEndpoints(t *testing.T) {
	service := &Service{targets: map[string]struct{}{"127.0.0.1:44215": {}, "och-mg:9300": {}}}
	if !service.DialAllowed("127.0.0.1", "44215") {
		t.Fatal("managed plugin endpoint refused")
	}
	if !service.DialAllowed("OCH-MG", "9300") {
		t.Fatal("sidecar hostname should match case-insensitively")
	}
	for _, c := range [][2]string{{"127.0.0.1", "44216"}, {"och-mg", "9301"}, {"", "9300"}} {
		if service.DialAllowed(c[0], c[1]) {
			t.Fatalf("unregistered endpoint %v allowed", c)
		}
	}
	var nilService *Service
	if nilService.DialAllowed("127.0.0.1", "44215") {
		t.Fatal("nil service must deny")
	}
}

// Enablement is the gate: a disabled plugin stops being an approved dial target,
// so "获取模型" cannot reach a sidecar the operator switched off.
func TestTargetsFollowPluginEnablement(t *testing.T) {
	svc := newServiceFor(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// RegisterSidecar fetches the manifest, so the stub has to answer with one.
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"dial-target","version":"1.0.0","name":"Dial Target"}`))
	}))
	defer upstream.Close()
	rec, err := svc.RegisterSidecar(upstream.URL, "sidecar-key", nil)
	if err != nil {
		t.Fatalf("RegisterSidecar: %v", err)
	}
	parsed, _ := url.Parse(upstream.URL)
	if !svc.DialAllowed(parsed.Hostname(), parsed.Port()) {
		t.Fatal("registered plugin endpoint is not an approved target")
	}
	if _, err := svc.Disable(rec.ID); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	if svc.DialAllowed(parsed.Hostname(), parsed.Port()) {
		t.Fatal("disabled plugin endpoint stayed approved")
	}
	if _, err := svc.Enable(rec.ID); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if !svc.DialAllowed(parsed.Hostname(), parsed.Port()) {
		t.Fatal("re-enabled plugin endpoint is not approved again")
	}
}

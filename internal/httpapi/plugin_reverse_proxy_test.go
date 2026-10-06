package httpapi

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestPluginRewriteProtectsInjectedAuthFromConnectionHeader(t *testing.T) {
	var key, authorization, query, path string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, authorization, query, path = r.Header.Get("X-Plugin-Key"), r.Header.Get("Authorization"), r.URL.RawQuery, r.URL.Path
		w.WriteHeader(200)
	}))
	defer upstream.Close()
	target, _ := url.Parse(upstream.URL + "/actual")
	request := httptest.NewRequest(http.MethodGet, "http://gateway/old?t=admin-secret&keep=1", nil)
	request.Header.Set("Authorization", "Bearer admin-secret")
	request.Header.Set("Connection", "X-Plugin-Key")
	w := httptest.NewRecorder()
	newPluginReverseProxy(target, nil, sidecarAuthAdmin, "", "plugin-secret", "").ServeHTTP(w, request)
	if w.Code != 200 || key != "plugin-secret" || authorization != "" || query != "keep=1" || path != "/actual" {
		t.Fatalf("unsafe forwarding: status=%d key=%q auth=%q query=%q path=%q", w.Code, key, authorization, query, path)
	}
}

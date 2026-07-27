package adapters

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewAPIAccountProbeSelfAndListKeys(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer user-token" {
			t.Fatalf("auth=%q", r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/api/user/self":
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":42,"username":"alice","quota":100,"used_quota":3}}`))
		case "/api/token/":
			_, _ = w.Write([]byte(`{"success":true,"data":{"items":[{"id":7,"name":"main","key":"sk-live-1","status":1},{"id":8,"name":"masked","key":"sk-****abcd","status":1}]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	adapter := NewNewAPIAccountAdapter("new-api", server.Client(), true)
	self, err := adapter.ProbeSelf(context.Background(), AccountInput{
		BaseURL: server.URL + "/v1",
		Secret:  "user-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	if self.Username != "alice" || self.PlatformUserID != 42 || self.Quota == nil || *self.Quota != 100 {
		t.Fatalf("self=%+v", self)
	}
	keys, err := adapter.ListAPIKeys(context.Background(), AccountInput{
		BaseURL: server.URL,
		Secret:  "user-token",
	}, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 {
		t.Fatalf("keys=%d", len(keys))
	}
	if keys[0].Secret != "sk-live-1" {
		t.Fatalf("secret0=%q", keys[0].Secret)
	}
	if keys[1].Secret != "" {
		t.Fatalf("masked secret should be empty, got %q", keys[1].Secret)
	}
}

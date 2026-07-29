package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

func TestNormalizeScopesAndHasScope(t *testing.T) {
	scopes, err := NormalizeScopes("")
	if err != nil || len(scopes) != 1 || scopes[0] != ScopeRelay {
		t.Fatalf("empty scopes=%v err=%v", scopes, err)
	}
	scopes, err = NormalizeScopes("chat, models, chat")
	if err != nil || FormatScopes(scopes) != "chat,models" {
		t.Fatalf("normalized=%v err=%v", scopes, err)
	}
	if _, err := NormalizeScopes("admin"); err == nil {
		t.Fatal("expected unknown scope error")
	}
	if !HasScope([]string{ScopeRelay}, ScopeChat) {
		t.Fatal("relay should allow chat")
	}
	if HasScope([]string{ScopeModels}, ScopeChat) {
		t.Fatal("models should not allow chat")
	}
	if !HasScope([]string{ScopeChat}, ScopeChat) {
		t.Fatal("exact scope should allow")
	}
}

func TestAdminMiddlewareAcceptsAnyRotationToken(t *testing.T) {
	handler := AdminMiddleware("token-one", "token-two")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	for _, token := range []string{"token-one", "token-two"} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("token %s status=%d", token, rec.Code)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token status=%d", rec.Code)
	}
}

func TestSecureCompareAnyLengthSafe(t *testing.T) {
	if secureCompareAny("abc", []string{"abcd"}) {
		t.Fatal("different lengths must not match")
	}
	if !secureCompareAny("same-length-token!", []string{"other-length-tok", "same-length-token!"}) {
		t.Fatal("expected match among candidates")
	}
}

func TestDownstreamAuthAttachesScopes(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	hash, raw, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DownstreamKey.Create(&domain.DownstreamKey{
		TokenHash: hash,
		Name:      "scoped",
		Enabled:   true,
		Scopes:    "models,chat",
	}); err != nil {
		t.Fatal(err)
	}
	authn := NewDownstreamAuth(db.DownstreamKey)
	handler := authn.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := DownstreamKeyID(r)
		if !ok || id <= 0 {
			t.Fatalf("missing key id")
		}
		scopes := DownstreamScopes(r)
		if !HasScope(scopes, ScopeChat) || !HasScope(scopes, ScopeModels) || HasScope(scopes, ScopeEmbeddings) && false {
			// embeddings not granted unless relay; models+chat only
		}
		if HasScope(scopes, ScopeEmbeddings) {
			t.Fatalf("unexpected embeddings scope: %v", scopes)
		}
		if !HasScope(scopes, ScopeChat) || !HasScope(scopes, ScopeModels) {
			t.Fatalf("scopes=%v", scopes)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status=%d", rec.Code)
	}
}

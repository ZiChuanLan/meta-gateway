package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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

func TestBearerRejectsAmbiguousAuthorizationHeaders(t *testing.T) {
	handler := AdminMiddleware("token-one")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Add("Authorization", "Bearer token-one")
	req.Header.Add("Authorization", "Bearer token-one")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("duplicate authorization headers status=%d, want 401", rec.Code)
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

func TestModelFilterAllows(t *testing.T) {
	// No restriction at all.
	if !ParseModelFilter("", "").Allows("gpt-4o") {
		t.Fatal("empty filter should allow everything")
	}
	// Allowlist only permits listed models.
	allowOnly := ParseModelFilter("gpt-4o, gpt-4o-mini", "")
	if !allowOnly.Allows("gpt-4o") || !allowOnly.Allows("gpt-4o-mini") {
		t.Fatal("allowlisted models should be permitted")
	}
	if allowOnly.Allows("claude-3") {
		t.Fatal("non-allowlisted model must be blocked")
	}
	// Denylist blocks even allowlisted models.
	withDeny := ParseModelFilter("gpt-4o, claude-3", "claude-3")
	if withDeny.Allows("claude-3") {
		t.Fatal("denylisted model must be blocked even when allowlisted")
	}
	if !withDeny.Allows("gpt-4o") {
		t.Fatal("allowlisted and non-denylisted model should pass")
	}
	// Whitespace and duplicates are handled.
	noisy := ParseModelFilter("  gpt-4o , gpt-4o , claude-3 ", " x ")
	if len(noisy.Allowlist) != 2 || len(noisy.Denylist) != 1 {
		t.Fatalf("expected dedup, got allow=%v deny=%v", noisy.Allowlist, noisy.Denylist)
	}
}

func TestModelFilterEmpty(t *testing.T) {
	if !(*ModelFilter)(nil).Empty() {
		t.Fatal("nil filter should be empty")
	}
	if !ParseModelFilter("", "").Empty() {
		t.Fatal("empty lists should be empty")
	}
	if ParseModelFilter("gpt-4o", "").Empty() {
		t.Fatal("allowlist should not be empty")
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

func TestCheckKeyExpiry(t *testing.T) {
	if err := checkKeyExpiry(""); err != nil {
		t.Fatalf("empty expiry must pass, got %v", err)
	}
	if err := checkKeyExpiry(time.Now().Add(time.Hour).Format(time.RFC3339)); err != nil {
		t.Fatalf("future expiry must pass, got %v", err)
	}
	if err := checkKeyExpiry(time.Now().Add(-time.Hour).Format(time.RFC3339)); err == nil {
		t.Fatal("past expiry must fail")
	}
	if err := checkKeyExpiry("not-a-time"); err == nil {
		t.Fatal("garbage expiry must fail closed")
	}
}

func TestCheckKeyAllowedIPs(t *testing.T) {
	if err := checkKeyAllowedIPs("", "1.2.3.4"); err != nil {
		t.Fatalf("empty allowlist must pass, got %v", err)
	}
	if err := checkKeyAllowedIPs("1.2.3.4\n10.0.0.0/8", "1.2.3.4"); err != nil {
		t.Fatalf("exact IP must pass, got %v", err)
	}
	if err := checkKeyAllowedIPs("1.2.3.4\n10.0.0.0/8", "10.5.6.7"); err != nil {
		t.Fatalf("CIDR match must pass, got %v", err)
	}
	if err := checkKeyAllowedIPs("1.2.3.4", "8.8.8.8"); err == nil {
		t.Fatal("non-allowlisted IP must fail")
	}
	if err := checkKeyAllowedIPs("1.2.3.4", "not-an-ip"); err == nil {
		t.Fatal("invalid client IP must fail")
	}
}

func TestValidateDownstreamKeyConstraints(t *testing.T) {
	if err := ValidateKeyExpiry("2030-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateKeyExpiry("not-a-time"); err == nil {
		t.Fatal("invalid expiry accepted")
	}
	if err := ValidateKeyAllowedIPs("1.2.3.4, 10.0.0.0/8"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateKeyAllowedIPs("1.2.3.4,not-an-ip"); err == nil {
		t.Fatal("invalid allowed IP accepted")
	}
}

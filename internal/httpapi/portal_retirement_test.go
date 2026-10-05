package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lan/meta-gateway/internal/config"
	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/store"
)

func TestRetiredKeyPortalRoutesAreAbsent(t *testing.T) {
	db, err := store.OpenTest(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	enc, err := crypto.New("retirement-test-master-key-32-characters")
	if err != nil {
		t.Fatal(err)
	}
	router := NewTestRouter(t, &config.Config{
		AdminToken:   "retirement-admin",
		MetricsToken: "retirement-metrics",
	}, db, enc)

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/portal"},
		{http.MethodGet, "/portal/"},
		{http.MethodGet, "/portal/assets/app.js"},
		{http.MethodGet, "/portal/auth"},
		{http.MethodPost, "/portal/auth/password"},
		{http.MethodGet, "/portal/auth/github/start"},
		{http.MethodGet, "/portal/auth/linuxdo/callback"},
		{http.MethodGet, "/me"},
		{http.MethodGet, "/me/usage"},
		{http.MethodPost, "/me/credentials/password"},
		{http.MethodGet, "/admin/downstream-keys/1/portal"},
		{http.MethodPost, "/admin/downstream-keys/1/portal/password"},
		{http.MethodDelete, "/admin/downstream-keys/1/portal/github"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			// Authenticate admin paths so an auth rejection cannot mask a
			// surviving route.
			req.Header.Set("Authorization", "Bearer retirement-admin")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status=%d body=%s, want 404", rec.Code, rec.Body.String())
			}
		})
	}

	for _, path := range []string{"/healthz", "/console", "/admin/downstream-keys"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer retirement-admin")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("retained route %s: status=%d body=%s", path, rec.Code, rec.Body.String())
		}
	}
}

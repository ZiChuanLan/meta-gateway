// Package httpapi wires HTTP routes for admin and public endpoints.
package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/lan/meta-gateway/internal/auth"
	"github.com/lan/meta-gateway/internal/config"
	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/relay"
	"github.com/lan/meta-gateway/internal/store"
)

// New creates a fully wired chi.Router.
func New(cfg *config.Config, db *store.DB, enc *crypto.Encrypter) http.Handler {
	r := chi.NewRouter()

	// Global middleware
	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)
	r.Use(chimw.Logger)
	r.Use(chimw.Recoverer)

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// Admin routes
	adminGroup := chi.NewRouter()
	adminGroup.Use(auth.AdminMiddleware(cfg.AdminToken))
	adminHandler := NewAdminHandler(db, enc)
	adminHandler.Register(adminGroup)
	r.Mount("/admin", adminGroup)

	// Relay routes (v1)
	relayHandler := NewRelayHandler(db, relay.New(), enc)
	v1Group := chi.NewRouter()
	v1Group.Use(auth.NewDownstreamAuth(db.DownstreamKey).Middleware())
	relayHandler.Register(v1Group)
	r.Mount("/v1", v1Group)

	return r
}

// writeJSON marshals v and writes it with the given status code.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		json.NewEncoder(w).Encode(v)
	}
}

// writeError sends a JSON error response.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// Package httpapi wires HTTP routes for admin and public endpoints.
package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/lan/meta-gateway/internal/adapters"
	"github.com/lan/meta-gateway/internal/auth"
	"github.com/lan/meta-gateway/internal/checkin"
	"github.com/lan/meta-gateway/internal/config"
	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/discovery"
	"github.com/lan/meta-gateway/internal/exchange"
	"github.com/lan/meta-gateway/internal/proxy"
	"github.com/lan/meta-gateway/internal/relay"
	"github.com/lan/meta-gateway/internal/routing"
	"github.com/lan/meta-gateway/internal/store"
)

type Dependencies struct {
	Registry        *adapters.Registry
	CheckinService  *checkin.Service
	ExchangeService *exchange.Service
}

// New creates a fully wired chi.Router.
func New(cfg *config.Config, db *store.DB, enc *crypto.Encrypter) http.Handler {
	return NewWithDependencies(cfg, db, enc, Dependencies{})
}

// NewWithDependencies wires shared application services into the HTTP router.
func NewWithDependencies(cfg *config.Config, db *store.DB, enc *crypto.Encrypter, dependencies Dependencies) http.Handler {
	r := chi.NewRouter()
	registry := dependencies.Registry
	if registry == nil {
		registry = adapters.NewRegistry(nil)
	}
	checkinService := dependencies.CheckinService
	if checkinService == nil {
		checkinService = checkin.New(db, enc, registry)
	}
	discoveryService := discovery.New(db, enc, registry)
	exchangeService := dependencies.ExchangeService
	if exchangeService == nil {
		exchangeService = exchange.NewService(db, enc, discoveryService)
	}

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
	selector := routing.New(db.RouteMember)
	adminHandler := NewAdminHandler(db, enc, selector)
	adminHandler.Register(adminGroup)
	discoveryHandler := NewDiscoveryHandler(db, discoveryService)
	discoveryHandler.Register(adminGroup)
	checkinHandler := NewCheckinHandler(db, checkinService)
	checkinHandler.Register(adminGroup)
	exchangeHandler := NewExchangeHandler(exchangeService)
	exchangeHandler.Register(adminGroup)
	r.Mount("/admin", adminGroup)

	// Relay routes (v1)
	proxyService := proxy.New(selector, relay.New(), db, enc, cfg.RetryTimes, cfg.Cooldown)
	relayHandler := NewRelayHandler(db, proxyService)
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

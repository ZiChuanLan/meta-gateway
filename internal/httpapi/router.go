// Package httpapi wires HTTP routes for admin and public endpoints.
package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/lan/meta-gateway/internal/account"
	"github.com/lan/meta-gateway/internal/adapters"
	"github.com/lan/meta-gateway/internal/auth"
	"github.com/lan/meta-gateway/internal/backup"
	"github.com/lan/meta-gateway/internal/checkin"
	"github.com/lan/meta-gateway/internal/config"
	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/discovery"
	"github.com/lan/meta-gateway/internal/exchange"
	"github.com/lan/meta-gateway/internal/observability"
	"github.com/lan/meta-gateway/internal/outbound"
	"github.com/lan/meta-gateway/internal/plugins"
	"github.com/lan/meta-gateway/internal/proxy"
	"github.com/lan/meta-gateway/internal/ratelimit"
	"github.com/lan/meta-gateway/internal/relay"
	"github.com/lan/meta-gateway/internal/routing"
	"github.com/lan/meta-gateway/internal/runtimeconfig"
	"github.com/lan/meta-gateway/internal/store"
	"github.com/lan/meta-gateway/internal/webdavsync"
	"github.com/lan/meta-gateway/internal/webui"
)

type Dependencies struct {
	Registry          *adapters.Registry
	CheckinService    *checkin.Service
	CheckinScheduler  *checkin.Scheduler
	ExchangeService   *exchange.Service
	PluginService     *plugins.Service
	OutboundClient    *http.Client
	Logger            *slog.Logger
	Metrics           *observability.Registry
	State             *observability.State
	BackupService     *backup.Service
	WebDAVService     *webdavsync.Service
	RuntimeController *runtimeconfig.Controller
	SetAuditRetention func(days, rows int)
}

// New creates a fully wired chi.Router.
func New(cfg *config.Config, db *store.DB, enc *crypto.Encrypter) http.Handler {
	return NewWithDependencies(cfg, db, enc, Dependencies{})
}

// NewWithDependencies wires shared application services into the HTTP router.
func NewWithDependencies(cfg *config.Config, db *store.DB, enc *crypto.Encrypter, dependencies Dependencies) http.Handler {
	r := chi.NewRouter()
	logger := dependencies.Logger
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}
	metrics := dependencies.Metrics
	if metrics == nil {
		metrics = observability.NewRegistry()
	}
	state := dependencies.State
	if state == nil {
		state = observability.NewState()
	}
	clientIPs, err := newClientIPResolver(cfg.TrustedProxyCIDRs)
	if err != nil {
		panic("httpapi: invalid trusted proxy policy")
	}
	outboundClient := dependencies.OutboundClient
	if outboundClient == nil {
		policy, err := outbound.NewPolicy(outbound.Options{
			AllowHosts:  cfg.OutboundAllowHosts,
			AllowCIDRs:  cfg.OutboundAllowCIDRs,
			DialTimeout: cfg.OutboundConnectTimeout,
		})
		if err != nil {
			panic("httpapi: invalid outbound policy")
		}
		outboundClient = outbound.NewClient(policy, outbound.ClientOptions{
			ResponseHeaderTimeout: cfg.OutboundResponseHeaderTimeout,
			TLSHandshakeTimeout:   cfg.OutboundTLSHandshakeTimeout,
		})
	}
	registry := dependencies.Registry
	if registry == nil {
		registry = adapters.NewRegistry(outboundClient)
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
	r.Use(clientIPs.Middleware)
	r.Use(requestTelemetry(logger, metrics))
	r.Use(recoverMiddleware(logger))

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	r.Get("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !state.Ready() || !pingReady(db, cfg.ReadinessTimeout) {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	trustedScrapers := parsePrefixes(cfg.TrustedScraperCIDRs)
	r.Get("/metrics", func(w http.ResponseWriter, request *http.Request) {
		if !metricsAuthorized(cfg.MetricsToken, trustedScrapers, request) {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		if err := metrics.WritePrometheus(w, state.Ready()); err != nil {
			logger.ErrorContext(request.Context(), "metrics write failed", "category", "write")
		}
	})
	r.Get("/admin-ui", func(w http.ResponseWriter, request *http.Request) {
		http.Redirect(w, request, "/admin-ui/", http.StatusPermanentRedirect)
	})
	r.Handle("/admin-ui/*", webui.Handler())

	// Admin routes
	adminGroup := chi.NewRouter()
	adminGroup.Use(auditAdmin(logger, db.AuditEvent))
	adminGroup.Use(auth.AdminMiddleware(cfg.AdminToken))
	adminLimiter := ratelimit.New(cfg.AdminRatePerMinute, cfg.AdminRateBurst)
	relayLimiter := ratelimit.New(cfg.RelayRatePerMinute, cfg.RelayRateBurst)
	adminGroup.Use(rateLimitMiddleware(adminLimiter, func(*http.Request) int64 { return 0 }, "admin", metrics))
	adminGroup.Use(withAdminBodyLimit(cfg.MaxAdminBodyBytes))
	selector := routing.New(db.RouteMember)
	proxyService := proxy.New(selector, relay.NewWithClient(outboundClient), db, enc, cfg.RetryTimes, cfg.Cooldown)
	adminHandler := NewAdminHandler(db, enc, selector)
	adminHandler.Register(adminGroup)
	discoveryHandler := NewDiscoveryHandler(db, discoveryService)
	discoveryHandler.Register(adminGroup)
	accountService := account.New(db, enc, registry)
	if exchangeService != nil {
		exchangeService.SetKeySyncer(account.ExchangeKeySyncer{Service: accountService})
	}
	NewAccountHandler(accountService).Register(adminGroup)
	NewTryHandler(proxyService).Register(adminGroup)

	// Plugin catalog remains optional. Core product ops are always registered:
	// check-in, exchange, audit, backup — these are first-class Admin surfaces.
	pluginService := dependencies.PluginService
	if pluginService != nil {
		NewPluginHandler(pluginService).Register(adminGroup)
	}

	NewCheckinHandler(db, checkinService).Register(adminGroup)
	NewExchangeHandler(exchangeService).Register(adminGroup)
	webdavService := dependencies.WebDAVService
	if webdavService == nil {
		maxBytes := cfg.WebDAVMaxBytes
		if maxBytes <= 0 {
			maxBytes = 10 << 20
		}
		webdavService = webdavsync.NewServiceWithSettings(webdavsync.Config{
			Enabled:        cfg.WebDAVSyncEnabled,
			URL:            cfg.WebDAVURL,
			Username:       cfg.WebDAVUsername,
			Password:       cfg.WebDAVPassword,
			BackupPassword: cfg.WebDAVBackupPassword,
			CronExpr:       cfg.WebDAVCron,
			MaxBytes:       maxBytes,
		}, &webdavsync.Client{HTTP: outboundClient, MaxBytes: maxBytes}, exchangeService, db.WebDAVSettings, enc)
	}
	NewWebDAVHandler(webdavService).Register(adminGroup)
	auditHandler := NewAuditHandler(db, cfg.AuditRetentionDays, cfg.AuditRetentionRows)
	auditHandler.Register(adminGroup)
	backupService := dependencies.BackupService
	if backupService == nil {
		backupService = backup.New(db, cfg.BackupDir)
	}
	NewBackupHandler(backupService).Register(adminGroup)

	runtimeController := dependencies.RuntimeController
	if runtimeController == nil {
		runtimeController = runtimeconfig.New(cfg, db.RuntimeSettings, runtimeconfig.Appliers{
			Proxy:        proxyService,
			RelayLimiter: relayLimiter,
			AdminLimiter: adminLimiter,
			CheckinSched: dependencies.CheckinScheduler,
			SetAudit:     auditHandler.SetRetention,
			SetAuditLoop: dependencies.SetAuditRetention,
		})
		if err := runtimeController.Bootstrap(); err != nil {
			logger.Error("runtime settings bootstrap failed", "category", "configuration", "err", err.Error())
		}
	}
	NewRuntimeSettingsHandler(runtimeController).Register(adminGroup)
	r.Mount("/admin", adminGroup)

	// Relay routes (v1)
	relayHandler := NewRelayHandler(db, proxyService)
	v1Group := chi.NewRouter()
	v1Group.Use(auth.NewDownstreamAuth(db.DownstreamKey).Middleware())
	v1Group.Use(rateLimitMiddleware(relayLimiter, downstreamRateKey, "relay", metrics))
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

// Package httpapi wires HTTP routes for admin and public endpoints.
package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/lan/meta-gateway/internal/account"
	"github.com/lan/meta-gateway/internal/adapters"
	"github.com/lan/meta-gateway/internal/alert"
	"github.com/lan/meta-gateway/internal/auth"
	"github.com/lan/meta-gateway/internal/backup"
	"github.com/lan/meta-gateway/internal/checkin"
	"github.com/lan/meta-gateway/internal/config"
	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/discovery"
	"github.com/lan/meta-gateway/internal/exchange"
	"github.com/lan/meta-gateway/internal/healthsweep"
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
	"github.com/lan/meta-gateway/internal/webhook"
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
	r.Get("/console", func(w http.ResponseWriter, request *http.Request) {
		http.Redirect(w, request, "/console/", http.StatusPermanentRedirect)
	})
	r.Group(func(console chi.Router) {
		console.Use(securityHeaders)
		console.Handle("/console/*", webui.Handler())
	})
	// Legacy paths: keep old /admin-ui bookmarks working.
	r.Get("/admin-ui", func(w http.ResponseWriter, request *http.Request) {
		http.Redirect(w, request, "/console/", http.StatusPermanentRedirect)
	})
	r.Handle("/admin-ui/*", http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		request.URL.Path = "/console/" + strings.TrimPrefix(request.URL.Path, "/admin-ui/")
		http.Redirect(w, request, request.URL.String(), http.StatusPermanentRedirect)
	}))
	r.Get("/", handleLanding)

	// Admin routes
	adminGroup := chi.NewRouter()
	adminGroup.Use(auditAdmin(logger, db.AuditEvent))
	adminGroup.Use(auth.AdminMiddleware(cfg.AdminTokenList()...))
	adminLimiter := ratelimit.New(cfg.AdminRatePerMinute, cfg.AdminRateBurst)
	relayLimiter := ratelimit.New(cfg.RelayRatePerMinute, cfg.RelayRateBurst)
	adminGroup.Use(rateLimitMiddleware(adminLimiter, func(*http.Request) int64 { return 0 }, "admin", metrics))
	adminGroup.Use(withAdminBodyLimit(cfg.MaxAdminBodyBytes))
	selector := routing.New(db.RouteMember)
	var stickyStore *routing.StickyStore
	if cfg.StickyEnabled {
		stickyStore = routing.NewStickyStore(cfg.StickyTTL, nil)
		selector.SetSticky(stickyStore)
	}
proxyService := proxy.New(selector, relay.NewWithClient(outboundClient), db, enc, cfg.RetryTimes, cfg.Cooldown)
proxyService.SetAdapterRegistry(registry)
selector.SetCircuitAware(proxyService.CircuitWeight)
proxyService.SetChannelRetryTimes(cfg.ChannelRetryTimes)
proxyService.SetKeyPoolRotation(cfg.KeyPoolRotation)
proxyService.SetAutoDisableThreshold(cfg.ChannelAutoDisableThreshold)
	proxyService.SetKeyFailThreshold(cfg.KeyFailThreshold)
	proxyService.SetStableFirstPromote(cfg.StableFirstPromoteRequests)
	proxyService.SetSticky(stickyStore)
	proxyService.SetBreakerFailCount(cfg.ModelBreakerFailCount)
	// Operational webhook notifier: auto-disable/recovery events, throttled.
	webhookNotifier := webhook.New(cfg.WebhookURL, time.Duration(cfg.WebhookThrottleSeconds)*time.Second)
	proxyService.SetWebhookNotifier(webhookNotifier)
	discoveryService.SetWebhookNotifier(webhookNotifier)
	// Alert matrix (webhook/bark/serverchan/telegram/smtp) + daily summary.
	var alertCfg webhook.AlertConfig
	if cfg.AlertConfigJSON != "" {
		if err := json.Unmarshal([]byte(cfg.AlertConfigJSON), &alertCfg); err == nil {
			webhookNotifier.SetAlertConfig(alertCfg)
		}
	}
	dailySummary := alert.NewDailySummary(db, webhookNotifier, cfg.AlertDailySummaryInterval, alertCfg.DailySummaryEnabled)
	dailySummary.Start()
	RegisterStopper(dailySummary.Stop)
	selector.SetStableFirst(cfg.StableFirstEnabled, cfg.StableFirstDenominator)
	if cfg.RoutingLatencyAware {
		proxyService.SetLatencyAware(true)
		selector.SetLatencyAware(true, proxyService.ChannelLatency)
	}
	// Shared /v1/models cache: recomputed on expiry or admin route/channel writes.
	modelsCache := newModelsCache(5 * time.Second)
	adminHandler := NewAdminHandler(db, enc, selector, stickyStore, outboundClient, modelsCache)
	adminHandler.Register(adminGroup)
	discoveryHandler := NewDiscoveryHandler(db, discoveryService)
	discoveryHandler.Register(adminGroup)
	// Periodic channel health sweep (runtime-configurable): jittered probes
	// grade each enabled channel operational/degraded/error and alert on
	// transitions. The service always exists so the sweep can be toggled from
	// Admin without a restart; Enabled is driven by config (env bootstrap or
	// runtime override), and SetHealthSweep hot-applies policy changes.
	healthSweep := healthsweep.New(db, discoveryService, webhookNotifier, healthsweep.Config{
		Enabled:             cfg.HealthSweepEnabled,
		IntervalSeconds:     cfg.HealthSweepIntervalSeconds,
		JitterSeconds:       cfg.HealthSweepJitterSeconds,
		DegradedThresholdMs: cfg.HealthSweepDegradedMs,
		Concurrency:         cfg.HealthSweepConcurrency,
		TimeoutSeconds:      cfg.HealthSweepTimeoutSeconds,
	})
	healthSweep.Start()
	RegisterStopper(healthSweep.Stop)
	adminGroup.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		status := healthSweep.Status()
		if status == nil {
			status = []healthsweep.ChannelHealth{}
		}
		writeJSON(w, http.StatusOK, status)
	})
	accountService := account.New(db, enc, registry)
	accountService.SetNotifier(webhookNotifier)
	checkinService.SetNotifier(webhookNotifier)
	// Proactive sweep: refresh finance (balance-low) + probe tokens (expired)
	// on a timer so alerts fire without an operator opening the admin pages.
	alertSweep := alert.NewSweep(accountService, webhookNotifier, cfg.AlertSweepInterval)
	alertSweep.Start()
	RegisterStopper(alertSweep.Stop)
	// Daily balance-history snapshot: records each channel's balance once a day
	// (plus a first snapshot shortly after boot when the table is empty) so the
	// dashboard trend chart has data without waiting 24h. Retention-prunes
	// snapshots older than 90 days on the same cadence.
	const balanceRetentionDays = 90
	balanceStop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		run := func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			if n, err := accountService.RecordBalanceHistory(ctx); err != nil {
				logger.Warn("balance history snapshot failed", "error", err)
			} else if n > 0 {
				logger.Info("balance history snapshot", "channels", n)
			}
			if _, err := accountService.PruneBalanceHistory(ctx, balanceRetentionDays); err != nil {
				logger.Warn("balance history prune failed", "error", err)
			}
		}
		// First run shortly after boot if there is no data yet, then daily.
		time.Sleep(30 * time.Second)
		points, err := accountService.BalanceHistory(context.Background(), 2)
		if err == nil && len(points) == 0 {
			run()
		}
		for {
			select {
			case <-ticker.C:
				run()
			case <-balanceStop:
				return
			}
		}
	}()
	RegisterStopper(func() { close(balanceStop) })
	if exchangeService != nil {
		exchangeService.SetKeySyncer(account.ExchangeKeySyncer{Service: accountService})
	}
	NewAccountHandler(accountService).Register(adminGroup)
	NewTryHandler(proxyService).Register(adminGroup)

	// Plugin catalog + add-on gates. Optional modules (exchange, checkin) must be
	// enabled to expose their Admin surfaces. Core audit/backup stay always-on.
	pluginService := dependencies.PluginService
	if pluginService != nil {
		NewPluginHandler(pluginService).Register(adminGroup)
	}
	// Local CLIProxyAPI integration surface (OAuth subscription pool add-on).
	NewCPAHandler().Register(adminGroup)

	adminGroup.Group(func(module chi.Router) {
		if pluginService != nil {
			module.Use(requirePluginEnabled(pluginService, "checkin"))
		}
		NewCheckinHandler(db, checkinService).Register(module)
	})
	adminGroup.Group(func(module chi.Router) {
		if pluginService != nil {
			module.Use(requirePluginEnabled(pluginService, "exchange"))
		}
		NewExchangeHandler(exchangeService, cfg.ExchangeAllowSecretExport).Register(module)
	})
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
	adminGroup.Group(func(module chi.Router) {
		if pluginService != nil {
			module.Use(requirePluginEnabled(pluginService, "exchange"))
		}
		NewWebDAVHandler(webdavService).Register(module)
	})
	auditHandler := NewAuditHandler(db, cfg.AuditRetentionDays, cfg.AuditRetentionRows)
	auditHandler.Register(adminGroup)
	backupService := dependencies.BackupService
	if backupService == nil {
		backupService = backup.New(db, cfg.BackupDir)
	}
	// Core: audit + online backups are always available (not store-gated).
	NewBackupHandler(backupService).Register(adminGroup)

	runtimeController := dependencies.RuntimeController
	if runtimeController == nil {
		runtimeController = runtimeconfig.New(cfg, db.RuntimeSettings, runtimeconfig.Appliers{
			Proxy:        proxyService,
			Selector:     selector,
			RelayLimiter: relayLimiter,
			AdminLimiter: adminLimiter,
			CheckinSched: dependencies.CheckinScheduler,
			CheckinAllowed: func() bool {
				return pluginService == nil || pluginService.IsEnabled("checkin")
			},
			SetAudit:     auditHandler.SetRetention,
			SetAuditLoop: dependencies.SetAuditRetention,
			SetProgressiveCooldown: func(enabled bool, base time.Duration, levels [3]time.Duration, breakerCount int) {
				db.RouteMember.SetProgressiveCooldown(enabled, base, levels, breakerCount)
			},
			// Sticky hot-swap: rewire selector + proxy + admin handler so an
			// admin toggle takes effect without a restart.
			SetSticky: func(store *routing.StickyStore, ttl time.Duration) {
				selector.SetSticky(store)
				proxyService.SetSticky(store)
				adminHandler.SetSticky(store)
			},
			SetRecoveryProbe: func(enabled bool, interval time.Duration) {
				discoveryService.SetRecoveryConfig(enabled, interval)
			},
			SetStableFirst: func(enabled bool, denominator, promoteRequests int) {
				selector.SetStableFirst(enabled, denominator)
				proxyService.SetStableFirstPromote(promoteRequests)
			},
			SetConcurrencyAware: func(enabled bool, limit int) {
				proxyService.SetConcurrencyAware(enabled, limit)
			},
			SetWebhook: func(url string, throttle time.Duration) {
				webhookNotifier.SetConfig(url, throttle)
			},
			SetAlert: func(cfg webhook.AlertConfig, sweepInterval, dailySummaryInterval time.Duration) {
				webhookNotifier.SetAlertConfig(cfg)
				alertSweep.SetInterval(sweepInterval)
				if !cfg.DailySummaryEnabled {
					dailySummaryInterval = 0
				}
				dailySummary.SetInterval(dailySummaryInterval)
			},
			SetHealthSweep: func(cfg healthsweep.Config) {
				healthSweep.SetConfig(cfg)
			},
			SetChannelRetryTimes: func(times int) {
				proxyService.SetChannelRetryTimes(times)
			},
			SetKeyPoolRotation: func(enabled bool) {
				proxyService.SetKeyPoolRotation(enabled)
			},
		})
		if err := runtimeController.Bootstrap(); err != nil {
			logger.Error("runtime settings bootstrap failed", "category", "configuration", "err", err.Error())
		}
	}
	// Passive-recovery loop: probes auto-disabled channels on a schedule and
	// restores them when the upstream answers (config hot-reloadable).
	recoveryCtx, recoveryCancel := context.WithCancel(context.Background())
	RegisterStopper(recoveryCancel)
	go discoveryService.RunRecoveryLoop(recoveryCtx)
	// Re-apply check-in schedule from effective runtime settings when the add-on is toggled.
	if pluginService != nil && runtimeController != nil {
		ctrl := runtimeController
		pluginService.SetOnChange(func(id string, _enabled bool) {
			if id != "checkin" {
				return
			}
			if err := ctrl.ResyncCheckin(); err != nil {
				logger.Error("check-in resync after module toggle failed", "category", "scheduler", "err", err.Error())
			}
		})
	}
	NewRuntimeSettingsHandler(runtimeController).Register(adminGroup)
	r.Mount("/admin", adminGroup)

	// Relay routes (v1)
	relayHandler := NewRelayHandler(db, proxyService, ratelimit.New(cfg.RelayModelRatePerMinute, cfg.RelayModelRateBurst), newGroupRateLimiter(), modelsCache)
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

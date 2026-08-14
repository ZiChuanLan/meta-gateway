// Meta Gateway — a lightweight relay gateway for LLM API access.
//
// Production OpenAI-compatible relay: multi-channel routing, discovery, check-in, exchange, ops, Web Admin.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/lan/meta-gateway/internal/adapters"
	"github.com/lan/meta-gateway/internal/backup"
	"github.com/lan/meta-gateway/internal/checkin"
	"github.com/lan/meta-gateway/internal/config"
	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/discovery"
	"github.com/lan/meta-gateway/internal/exchange"
	"github.com/lan/meta-gateway/internal/httpapi"
	"github.com/lan/meta-gateway/internal/observability"
	"github.com/lan/meta-gateway/internal/outbound"
	"github.com/lan/meta-gateway/internal/plugins"
	"github.com/lan/meta-gateway/internal/store"
	"github.com/lan/meta-gateway/internal/webdavsync"
	_ "time/tzdata" // embed IANA tz database so CHECKIN_TZ works in minimal containers
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)
	if len(os.Args) > 1 && os.Args[1] == "restore" {
		if len(os.Args) != 4 || os.Args[2] != "--from" || os.Args[3] == "" {
			logger.Error("usage: meta-gateway restore --from <backup-name>")
			os.Exit(2)
		}
		restoreCfg := config.LoadRestore()
		if _, err := backup.Restore(restoreCfg.DataDir, restoreCfg.BackupDir, os.Args[3]); err != nil {
			logger.Error("restore failed", "category", "restore")
			os.Exit(1)
		}
		logger.Info("restore completed", "backup", os.Args[3])
		return
	}
	cfg, err := config.Load()
	if err != nil {
		logger.Error("configuration invalid", "category", "configuration")
		os.Exit(1)
	}

	// Validate required config.
	if len(cfg.AdminTokenList()) == 0 {
		logger.Error("configuration invalid", "category", "admin_token_required")
		os.Exit(1)
	}
	if cfg.MasterKey == "" {
		logger.Error("configuration invalid", "category", "master_key_required")
		os.Exit(1)
	}

	// Initialize encryption.
	enc, err := crypto.New(cfg.MasterKey)
	if err != nil {
		logger.Error("crypto initialization failed", "category", "configuration")
		os.Exit(1)
	}

	// Ensure data and plugins directories exist.
	if err := os.MkdirAll(cfg.DataDir, 0700); err != nil {
		logger.Error("data directory initialization failed", "category", "filesystem")
		os.Exit(1)
	}
	if err := os.MkdirAll(cfg.PluginsDir, 0700); err != nil {
		logger.Error("plugins directory initialization failed", "category", "filesystem")
		os.Exit(1)
	}

	// Open database.
	db, err := store.OpenWithMaxConns(cfg.DataDir, cfg.SQLiteMaxOpenConns)
	if err != nil {
		logger.Error("store initialization failed", "category", "database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	outboundPolicy, err := outbound.NewPolicy(outbound.Options{
		AllowHosts:  cfg.OutboundAllowHosts,
		AllowCIDRs:  cfg.OutboundAllowCIDRs,
		DialTimeout: cfg.OutboundConnectTimeout,
	})
	if err != nil {
		logger.Error("outbound policy invalid", "category", "configuration")
		os.Exit(1)
	}
	outboundClient := outbound.NewClient(outboundPolicy, outbound.ClientOptions{
		ResponseHeaderTimeout: cfg.OutboundResponseHeaderTimeout,
		TLSHandshakeTimeout:   cfg.OutboundTLSHandshakeTimeout,
		MaxIdleConns:          cfg.OutboundMaxIdleConns,
		MaxIdleConnsPerHost:   cfg.OutboundMaxIdleConnsPerHost,
	})
	registry := adapters.NewRegistry(outboundClient)
	checkinService := checkin.New(db, enc, registry)
	pluginService, err := plugins.NewServiceWithOptions(cfg.PluginsDir, db.Plugin, cfg.PluginCatalogURL, outboundClient)
	if err != nil {
		logger.Error("plugin host initialization failed", "category", "plugins")
		os.Exit(1)
	}
	pluginService.SetMarketURLs(cfg.PluginMarketURLs)
	if err := pluginService.EnsureOfficialModulesInstalled(); err != nil {
		logger.Error("plugin bootstrap failed", "category", "plugins")
		os.Exit(1)
	}
	metrics := observability.NewRegistry()
	state := observability.NewState()
	discoveryService := discovery.New(db, enc, registry)
	exchangeService := exchange.NewService(db, enc, discoveryService)
	webdavMaxBytes := cfg.WebDAVMaxBytes
	if webdavMaxBytes <= 0 {
		webdavMaxBytes = 10 << 20
	}
	webdavService := webdavsync.NewServiceWithSettings(webdavsync.Config{
		Enabled:        cfg.WebDAVSyncEnabled,
		URL:            cfg.WebDAVURL,
		Username:       cfg.WebDAVUsername,
		Password:       cfg.WebDAVPassword,
		BackupPassword: cfg.WebDAVBackupPassword,
		CronExpr:       cfg.WebDAVCron,
		MaxBytes:       webdavMaxBytes,
	}, &webdavsync.Client{HTTP: outboundClient, MaxBytes: webdavMaxBytes}, exchangeService, db.WebDAVSettings, enc)
	// Always construct the check-in scheduler so Admin runtime settings can hot-enable it.
	// Initial Start() still respects env + module gate; later toggles use SetSchedule.
	checkinLocation := cfg.CheckinLocation()
	if cfg.CheckinTZ == "" && checkinLocation == time.UTC {
		logger.Warn("check-in scheduler uses UTC because TZ is unset — set CHECKIN_TZ (e.g. Asia/Shanghai) to schedule in local time")
	}
	var scheduler *checkin.Scheduler
	scheduler, err = checkin.NewScheduler(checkinService, cfg.CheckinCron, slog.NewLogLogger(logger.Handler(), slog.LevelInfo), checkinLocation)
	if err != nil {
		logger.Error("check-in scheduler configuration failed", "category", "configuration")
		os.Exit(1)
	}
	if cfg.CheckinTZ != "" {
		logger.Info("check-in scheduler timezone", "tz", cfg.CheckinTZ)
	}
	// Check-in scheduler resync on module toggle is wired in httpapi via runtimeconfig.ResyncCheckin.

	var auditDays atomic.Int64
	var auditRows atomic.Int64
	auditDays.Store(int64(cfg.AuditRetentionDays))
	auditRows.Store(int64(cfg.AuditRetentionRows))

	handler := httpapi.NewWithDependencies(cfg, db, enc, httpapi.Dependencies{
		Registry:         registry,
		CheckinService:   checkinService,
		CheckinScheduler: scheduler,
		ExchangeService:  exchangeService,
		PluginService:    pluginService,
		OutboundClient:   outboundClient,
		Logger:           logger, Metrics: metrics, State: state,
		BackupService: backup.New(db, cfg.BackupDir),
		WebDAVService: webdavService,
		SetAuditRetention: func(days, rows int) {
			auditDays.Store(int64(days))
			auditRows.Store(int64(rows))
		},
	})

	// Scheduler arming is owned by runtimeconfig.Bootstrap (admin override or
	// env, honoring the checkin module gate), which runs inside
	// NewWithDependencies. Start() below only serves embedders who bypass the
	// runtime controller; it is a no-op once a schedule decision was applied,
	// so a stored admin "disabled" override is never clobbered at boot.
	if err := scheduler.Start(); err != nil {
		logger.Error("check-in scheduler start failed", "category", "scheduler")
		os.Exit(1)
	}
	switch {
	case scheduler.Started():
		logger.Info("check-in scheduler enabled")
	case cfg.CheckinEnabled && pluginService.IsEnabled("checkin"):
		logger.Info("check-in scheduler idle: disabled via Admin Settings (override wins over CHECKIN_ENABLED)")
	case cfg.CheckinEnabled:
		logger.Info("check-in scheduler idle: activate checkin module or enable via Settings")
	default:
		logger.Info("check-in scheduler constructed but not started (CHECKIN_ENABLED=false); Settings can enable without restart")
	}

	var webdavScheduler *webdavsync.Scheduler
	webdavStatus := webdavService.Status()
	if webdavStatus.Enabled {
		if !webdavStatus.Configured {
			logger.Info("webdav scheduler idle: URL/username/password incomplete")
		} else {
			cronExpr := webdavStatus.CronExpr
			if cronExpr == "" {
				cronExpr = cfg.WebDAVCron
			}
			var schedErr error
			webdavScheduler, schedErr = webdavsync.NewScheduler(webdavService, cronExpr, slog.NewLogLogger(logger.Handler(), slog.LevelInfo))
			if schedErr != nil {
				logger.Error("webdav scheduler configuration failed", "category", "configuration")
				os.Exit(1)
			}
			if err := webdavScheduler.Start(); err != nil {
				logger.Error("webdav scheduler start failed", "category", "scheduler")
				os.Exit(1)
			}
			webdavService.SetSchedulerArmed(true)
			logger.Info("webdav read-only sync scheduler enabled", "source", webdavStatus.Source)
		}
	}

	// Determine addr with host:port format.
	addr := cfg.HTTPAddr
	if addr == "" {
		addr = ":4100"
	}

	logger.Info("meta gateway starting", "addr", addr)

	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: cfg.ServerReadHeaderTimeout,
		ReadTimeout:       cfg.ServerReadTimeout,
		WriteTimeout:      0,
		IdleTimeout:       cfg.ServerIdleTimeout,
		MaxHeaderBytes:    cfg.MaxHeaderBytes,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	maintenanceCtx, cancelMaintenance := context.WithCancel(context.Background())
	defer cancelMaintenance()
	go runAuditCleanup(maintenanceCtx, logger, db, &auditDays, &auditRows)
	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		logger.Info("meta gateway shutting down")
	case err := <-serverErrors:
		if err != nil && err != http.ErrServerClosed {
			logger.Error("http server failed", "category", "server")
		}
		stop()
	}

	state.SetReady(false)
	cancelMaintenance()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ServerShutdownTimeout)
	defer cancel()
	// Halt router-owned background loops (alert sweep, daily summary, health
	// sweep, recovery) before the database closes, so none of them can touch a
	// closed DB during shutdown.
	httpapi.StopBackground(shutdownCtx)
	if scheduler != nil {
		if err := scheduler.Stop(shutdownCtx); err != nil {
			logger.Error("check-in scheduler shutdown failed", "category", "scheduler")
		}
	}
	if webdavScheduler != nil {
		if err := webdavScheduler.Stop(shutdownCtx); err != nil {
			logger.Error("webdav scheduler shutdown failed", "category", "scheduler")
		}
	}
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("http server shutdown failed", "category", "server")
	}
}

func runAuditCleanup(ctx context.Context, logger *slog.Logger, db *store.DB, days, rows *atomic.Int64) {
	cleanup := func() {
		if _, err := db.AuditEvent.Cleanup(time.Now(), int(days.Load()), int(rows.Load())); err != nil {
			logger.ErrorContext(ctx, "audit cleanup failed", "category", "persistence")
		}
	}
	cleanup()
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cleanup()
		}
	}
}

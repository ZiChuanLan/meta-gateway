// Meta Gateway — a lightweight relay gateway for LLM API access.
//
// P0–P2: Bootstrap, Admin CRUD, Single-channel relay with SSE passthrough.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lan/meta-gateway/internal/adapters"
	"github.com/lan/meta-gateway/internal/backup"
	"github.com/lan/meta-gateway/internal/checkin"
	"github.com/lan/meta-gateway/internal/config"
	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/httpapi"
	"github.com/lan/meta-gateway/internal/observability"
	"github.com/lan/meta-gateway/internal/outbound"
	"github.com/lan/meta-gateway/internal/store"
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
	if cfg.AdminToken == "" {
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

	// Ensure data directory exists.
	if err := os.MkdirAll(cfg.DataDir, 0700); err != nil {
		logger.Error("data directory initialization failed", "category", "filesystem")
		os.Exit(1)
	}

	// Open database.
	db, err := store.Open(cfg.DataDir)
	if err != nil {
		logger.Error("store initialization failed", "category", "database")
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
	})
	registry := adapters.NewRegistry(outboundClient)
	checkinService := checkin.New(db, enc, registry)
	metrics := observability.NewRegistry()
	state := observability.NewState()
	handler := httpapi.NewWithDependencies(cfg, db, enc, httpapi.Dependencies{
		Registry:       registry,
		CheckinService: checkinService,
		OutboundClient: outboundClient,
		Logger:         logger, Metrics: metrics, State: state,
		BackupService: backup.New(db, cfg.BackupDir),
	})

	var scheduler *checkin.Scheduler
	if cfg.CheckinEnabled {
		scheduler, err = checkin.NewScheduler(checkinService, cfg.CheckinCron, slog.NewLogLogger(logger.Handler(), slog.LevelInfo))
		if err != nil {
			logger.Error("check-in scheduler configuration failed", "category", "configuration")
			os.Exit(1)
		}
		if err := scheduler.Start(); err != nil {
			logger.Error("check-in scheduler start failed", "category", "scheduler")
			os.Exit(1)
		}
		logger.Info("check-in scheduler enabled")
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
	go runAuditCleanup(maintenanceCtx, logger, db, cfg.AuditRetentionDays, cfg.AuditRetentionRows)
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
	if scheduler != nil {
		if err := scheduler.Stop(shutdownCtx); err != nil {
			logger.Error("check-in scheduler shutdown failed", "category", "scheduler")
		}
	}
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("http server shutdown failed", "category", "server")
	}
}

func runAuditCleanup(ctx context.Context, logger *slog.Logger, db *store.DB, days, rows int) {
	cleanup := func() {
		if _, err := db.AuditEvent.Cleanup(time.Now(), days, rows); err != nil {
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

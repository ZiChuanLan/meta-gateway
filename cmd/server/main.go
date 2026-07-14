// Meta Gateway — a lightweight relay gateway for LLM API access.
//
// P0–P2: Bootstrap, Admin CRUD, Single-channel relay with SSE passthrough.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/lan/meta-gateway/internal/adapters"
	"github.com/lan/meta-gateway/internal/checkin"
	"github.com/lan/meta-gateway/internal/config"
	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/httpapi"
	"github.com/lan/meta-gateway/internal/store"
)

func main() {
	cfg := config.Load()

	// Validate required config.
	if cfg.AdminToken == "" {
		log.Fatal("ADMIN_TOKEN environment variable is required")
	}
	if cfg.MasterKey == "" {
		log.Fatal("MASTER_KEY environment variable is required")
	}

	// Initialize encryption.
	enc, err := crypto.New(cfg.MasterKey)
	if err != nil {
		log.Fatalf("crypto init: %v", err)
	}

	// Ensure data directory exists.
	if err := os.MkdirAll(cfg.DataDir, 0700); err != nil {
		log.Fatalf("create data dir: %v", err)
	}

	// Open database.
	db, err := store.Open(cfg.DataDir)
	if err != nil {
		log.Fatalf("store init: %v", err)
	}
	defer db.Close()

	registry := adapters.NewRegistry(nil)
	checkinService := checkin.New(db, enc, registry)
	handler := httpapi.NewWithDependencies(cfg, db, enc, httpapi.Dependencies{
		Registry:       registry,
		CheckinService: checkinService,
	})

	var scheduler *checkin.Scheduler
	if cfg.CheckinEnabled {
		scheduler, err = checkin.NewScheduler(checkinService, cfg.CheckinCron, log.Default())
		if err != nil {
			log.Fatalf("check-in scheduler config: %v", err)
		}
		if err := scheduler.Start(); err != nil {
			log.Fatalf("check-in scheduler start: %v", err)
		}
		log.Printf("Scheduled check-in enabled (cron: %s)", cfg.CheckinCron)
	}

	// Determine addr with host:port format.
	addr := cfg.HTTPAddr
	if addr == "" {
		addr = ":4100"
	}

	// Log the data path and addr.
	dataDirAbs, _ := filepath.Abs(cfg.DataDir)
	log.Printf("Meta Gateway starting on %s (data: %s)", addr, dataDirAbs)

	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		log.Printf("Meta Gateway shutting down")
	case err := <-serverErrors:
		if err != nil && err != http.ErrServerClosed {
			log.Printf("server error: %v", err)
		}
		stop()
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if scheduler != nil {
		if err := scheduler.Stop(shutdownCtx); err != nil {
			log.Printf("check-in scheduler shutdown: %v", err)
		}
	}
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("server shutdown: %v", err)
	}
}

// Meta Gateway — a lightweight relay gateway for LLM API access.
//
// P0–P2: Bootstrap, Admin CRUD, Single-channel relay with SSE passthrough.
package main

import (
	"log"
	"net/http"
	"os"
	"path/filepath"

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

	// Build router.
	handler := httpapi.New(cfg, db, enc)

	// Determine addr with host:port format.
	addr := cfg.HTTPAddr
	if addr == "" {
		addr = ":4100"
	}

	// Log the data path and addr.
	dataDirAbs, _ := filepath.Abs(cfg.DataDir)
	log.Printf("Meta Gateway starting on %s (data: %s)", addr, dataDirAbs)

	// Start server.
	srv := &http.Server{
		Addr:    addr,
		Handler: handler,
	}

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server error: %v", err)
	}
}

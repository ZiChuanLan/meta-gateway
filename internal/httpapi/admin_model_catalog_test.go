package httpapi_test

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/config"
	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/httpapi"
	"github.com/lan/meta-gateway/internal/store"
)

// catalogServer builds a console with the catalog sync wired to both public
// indexes. No request in these tests reaches the network: the status endpoint
// only reports wiring, and a preview with nothing to plan short-circuits.
func catalogServer(t *testing.T) string {
	t.Helper()
	dataDir := t.TempDir()
	db, err := store.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	enc, err := crypto.New("catalog-test-master-key-at-least-32-chars!")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		AdminToken: "admin-test", AdminTokens: []string{"admin-test"},
		MetricsToken:           "metrics-test",
		BackupDir:              filepath.Join(dataDir, "backups"),
		MaxAdminBodyBytes:      1 << 20,
		ModelCatalogSources:    []string{"litellm", "models.dev"},
		ModelCatalogInterval:   24 * time.Hour,
		ModelCatalogSyncPrices: true,
	}
	server := httptest.NewServer(httpapi.New(cfg, db, enc))
	t.Cleanup(server.Close)
	return server.URL
}

// The status endpoint is the console's only read-only view of the sync, and it
// must answer without touching the network.
func TestModelCatalogStatusEndpoint(t *testing.T) {
	serverURL := catalogServer(t)

	body := get(t, serverURL+"/admin/model-capabilities/catalog")
	var status struct {
		State         map[string]any `json:"state"`
		Sources       []string       `json:"sources"`
		Scheduled     bool           `json:"scheduled"`
		PricesEnabled bool           `json:"prices_enabled"`
	}
	if err := json.Unmarshal(body, &status); err != nil {
		t.Fatal(err)
	}
	// Fresh install: no sync has run, so the board is empty but the wiring is
	// already visible.
	if status.State != nil {
		t.Errorf("a fresh install should have no sync state, got %v", status.State)
	}
	if len(status.Sources) != 2 {
		t.Errorf("sources = %v, want both public indexes", status.Sources)
	}
	if !status.Scheduled {
		t.Error("scheduled should be true at a non-zero interval")
	}
	if !status.PricesEnabled {
		t.Error("prices_enabled should mirror the process setting")
	}
}

// An empty route table means there is nothing to sync, and the console should
// get a plain empty plan rather than a download attempt.
func TestModelCatalogPreviewWithNothingToPlan(t *testing.T) {
	serverURL := catalogServer(t)

	var preview struct {
		Items         []map[string]any `json:"items"`
		Requested     int              `json:"requested"`
		Sources       []string         `json:"sources"`
		PricesEnabled bool             `json:"prices_enabled"`
	}
	body := post(t, serverURL+"/admin/model-capabilities/catalog/preview", map[string]any{})
	if err := json.Unmarshal(body, &preview); err != nil {
		t.Fatal(err)
	}
	if len(preview.Items) != 0 || preview.Requested != 0 {
		t.Errorf("preview = %+v, want an empty plan", preview)
	}
	if len(preview.Sources) != 2 {
		t.Errorf("sources = %v", preview.Sources)
	}
	if !preview.PricesEnabled {
		t.Error("prices_enabled should be reported even for an empty plan")
	}
}

package plugins

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lan/meta-gateway/internal/store"
)

func openPluginTestDB(t *testing.T) *store.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestActivateEnableDisableUninstall(t *testing.T) {
	db := openPluginTestDB(t)
	dir := filepath.Join(t.TempDir(), "plugins")
	svc, err := NewService(dir, db.Plugin)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	if len(svc.Catalog()) == 0 {
		t.Fatal("expected official catalog")
	}

	rec, err := svc.Activate("exchange")
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if rec == nil || !rec.Enabled || rec.Status != StatusInstalled {
		t.Fatalf("activate record = %+v", rec)
	}
	if !svc.IsEnabled("exchange") {
		t.Fatal("expected exchange enabled")
	}
	if _, err := os.Stat(filepath.Join(dir, "exchange", "plugin.json")); err != nil {
		t.Fatalf("manifest missing: %v", err)
	}

	// Second activate should stay enabled (idempotent install path).
	if _, err := svc.Activate("exchange"); err != nil {
		t.Fatalf("Activate again: %v", err)
	}

	rec, err = svc.Disable("exchange")
	if err != nil {
		t.Fatalf("Disable: %v", err)
	}
	if rec.Enabled || svc.IsEnabled("exchange") {
		t.Fatal("expected exchange disabled")
	}

	if err := svc.Uninstall("exchange"); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "exchange")); !os.IsNotExist(err) {
		t.Fatalf("plugin dir still present: %v", err)
	}
	items, err := svc.ListInstalled()
	if err != nil {
		t.Fatalf("ListInstalled: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("want empty installed list, got %+v", items)
	}
}

func TestOrphanCannotEnableButCanUninstall(t *testing.T) {
	db := openPluginTestDB(t)
	dir := filepath.Join(t.TempDir(), "plugins")
	svc, err := NewService(dir, db.Plugin)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	orphan := &store.PluginRecord{
		ID:      "legacy-orphan",
		Version: "0.0.1",
		Status:  StatusInstalled,
		Enabled: false,
		Source:  "leftover",
	}
	if err := db.Plugin.Upsert(orphan); err != nil {
		t.Fatalf("seed orphan: %v", err)
	}

	if _, err := svc.Enable("legacy-orphan"); err != ErrNotFound {
		t.Fatalf("Enable orphan err = %v, want ErrNotFound", err)
	}
	if err := svc.Uninstall("legacy-orphan"); err != nil {
		t.Fatalf("Uninstall orphan: %v", err)
	}
	got, err := db.Plugin.Get("legacy-orphan")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != nil {
		t.Fatalf("orphan still present: %+v", got)
	}
}

func TestInstallRejectsUnknown(t *testing.T) {
	db := openPluginTestDB(t)
	svc, err := NewService(filepath.Join(t.TempDir(), "plugins"), db.Plugin)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if _, err := svc.Install("no-such-module"); err != ErrNotFound {
		t.Fatalf("Install unknown err = %v, want ErrNotFound", err)
	}
}

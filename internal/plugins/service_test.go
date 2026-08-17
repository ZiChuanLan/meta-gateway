package plugins

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func TestValidateSidecarPath(t *testing.T) {
	for _, value := range []string{"", "/", "healthz", "/app/", "assets/app.js", "/v0/management"} {
		if err := validateSidecarPath(value); err != nil {
			t.Errorf("valid path %q rejected: %v", value, err)
		}
	}
	for _, value := range []string{"..", "../admin", "/app/../admin", "/app/./x", "/app//x", `\admin`, "/app?token=x", "/app#x", "/app%2f.."} {
		if err := validateSidecarPath(value); err == nil {
			t.Errorf("unsafe path %q accepted", value)
		}
	}
	if err := validateAPIPrefix("/feature/../admin"); err == nil {
		t.Fatal("traversing API prefix accepted")
	}
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

func TestOfficialCatalogIsAddonsOnly(t *testing.T) {
	db := openPluginTestDB(t)
	svc, err := NewService(filepath.Join(t.TempDir(), "plugins"), db.Plugin)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range svc.Catalog() {
		if entry.ID == "operations" {
			t.Fatal("operations must not be a store-gated catalog module")
		}
		if entry.Kind != KindAddon {
			t.Fatalf("%s kind=%q want addon", entry.ID, entry.Kind)
		}
	}
	status, err := svc.Status()
	if err != nil {
		t.Fatal(err)
	}
	var sawCore, sawExchange bool
	for _, item := range status {
		if item.Kind == KindCore {
			sawCore = true
			if item.CanToggle {
				t.Fatalf("core %s should not toggle", item.ID)
			}
		}
		if item.ID == "exchange" {
			sawExchange = true
			if !item.CanToggle {
				t.Fatal("exchange should toggle")
			}
		}
	}
	if !sawCore || !sawExchange {
		t.Fatalf("status incomplete core=%v exchange=%v", sawCore, sawExchange)
	}
}

func TestEnsureOfficialBootstrapsAddons(t *testing.T) {
	db := openPluginTestDB(t)
	svc, err := NewService(filepath.Join(t.TempDir(), "plugins"), db.Plugin)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.EnsureOfficialModulesInstalled(); err != nil {
		t.Fatal(err)
	}
	if !svc.IsEnabled("exchange") || !svc.IsEnabled("checkin") {
		t.Fatal("expected addons enabled after bootstrap")
	}
	if svc.IsEnabled("operations") {
		t.Fatal("operations should not be an enabled plugin gate")
	}
}

func TestMultiOnChange(t *testing.T) {
	db := openPluginTestDB(t)
	svc, err := NewService(filepath.Join(t.TempDir(), "plugins"), db.Plugin)
	if err != nil {
		t.Fatal(err)
	}
	var calls []string
	svc.SetOnChange(func(id string, enabled bool) {
		calls = append(calls, id+":a")
	})
	svc.SetOnChange(func(id string, enabled bool) {
		calls = append(calls, id+":b")
	})
	if _, err := svc.Activate("checkin"); err != nil {
		t.Fatal(err)
	}
	if len(calls) < 2 {
		t.Fatalf("want multi listeners, got %v", calls)
	}
}

// fakeSidecar spins up a minimal sidecar plugin service serving /plugin.json,
// /healthz, and an echo endpoint that requires X-Plugin-Key.
func fakeSidecar(t *testing.T, id, name string, requireKey bool) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/plugin.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":%q,"version":"1.0.0","name":%q,"description":"test plugin","page_path":"/app","health_path":"healthz"}`, id, name)
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if requireKey && r.Header.Get("X-Plugin-Key") != "sekrit" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/app", func(w http.ResponseWriter, r *http.Request) {
		if requireKey && r.Header.Get("X-Plugin-Key") != "sekrit" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprintf(w, "plugin-page:%s", id)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestRegisterSidecarInstallsAndEnables(t *testing.T) {
	db := openPluginTestDB(t)
	svc, err := NewService(filepath.Join(t.TempDir(), "plugins"), db.Plugin)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	base := fakeSidecar(t, "test-plugin", "Test Plugin", false)

	rec, err := svc.RegisterSidecar(base, "", nil)
	if err != nil {
		t.Fatalf("RegisterSidecar: %v", err)
	}
	if rec == nil || !rec.Enabled || rec.Source != "sidecar" {
		t.Fatalf("record = %+v", rec)
	}
	spec, err := svc.SidecarFor("test-plugin")
	if err != nil || spec == nil {
		t.Fatalf("SidecarFor: %v %v", spec, err)
	}
	if spec.URL != base || spec.PagePath != "app" || spec.HealthPath != "healthz" {
		t.Fatalf("spec = %+v", spec)
	}
	// Appears in the store catalog as a normal add-on.
	found := false
	for _, e := range svc.Catalog() {
		if e.ID == "test-plugin" && e.Source == "sidecar" && e.Sidecar != nil {
			found = true
		}
	}
	if !found {
		t.Fatal("sidecar plugin missing from catalog")
	}
}

func TestRegisterSidecarRequiresHealthyService(t *testing.T) {
	db := openPluginTestDB(t)
	svc, err := NewService(filepath.Join(t.TempDir(), "plugins"), db.Plugin)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	// 401 on healthz because the key is missing.
	base := fakeSidecar(t, "keyed-plugin", "Keyed Plugin", true)
	if _, err := svc.RegisterSidecar(base, "", nil); err == nil {
		t.Fatal("expected health check failure")
	}
	// With the key the registration succeeds.
	rec, err := svc.RegisterSidecar(base, "sekrit", nil)
	if err != nil {
		t.Fatalf("RegisterSidecar with key: %v", err)
	}
	if rec == nil || !rec.Enabled {
		t.Fatalf("record = %+v", rec)
	}
}

func TestSidecarSurvivesRestart(t *testing.T) {
	db := openPluginTestDB(t)
	dir := filepath.Join(t.TempDir(), "plugins")
	svc, err := NewService(dir, db.Plugin)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	base := fakeSidecar(t, "restart-plugin", "Restart Plugin", false)
	if _, err := svc.RegisterSidecar(base, "", nil); err != nil {
		t.Fatalf("RegisterSidecar: %v", err)
	}
	// Simulate restart: fresh service over the same DB (no remote catalog).
	svc2, err := NewService(dir, db.Plugin)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if !svc2.IsEnabled("restart-plugin") {
		t.Fatal("expected enabled after restart")
	}
	spec, err := svc2.SidecarFor("restart-plugin")
	if err != nil || spec == nil {
		t.Fatalf("SidecarFor after restart: %v %v", spec, err)
	}
	if spec.URL != base {
		t.Fatalf("spec url = %q want %q", spec.URL, base)
	}
	found := false
	for _, e := range svc2.Catalog() {
		if e.ID == "restart-plugin" {
			found = true
		}
	}
	if !found {
		t.Fatal("sidecar missing from catalog after restart")
	}
}

func TestRegisterSidecarRejectsBadURL(t *testing.T) {
	db := openPluginTestDB(t)
	svc, err := NewService(filepath.Join(t.TempDir(), "plugins"), db.Plugin)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	for _, bad := range []string{"", "ftp://x", "http://", "not a url", "http://example.test/base/../admin", "http://example.test/base%2fchild"} {
		if _, err := svc.RegisterSidecar(bad, "", nil); err == nil {
			t.Fatalf("expected error for %q", bad)
		}
	}
}

func TestFetchSidecarManifestRejectsOversizedValidPrefix(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		prefix := `{"id":"oversized","version":"1.0.0","name":"Oversized"}`
		_, _ = w.Write([]byte(prefix + strings.Repeat(" ", maxPluginCatalogBytes-len(prefix)+1)))
	}))
	t.Cleanup(srv.Close)
	db := openPluginTestDB(t)
	svc, err := NewService(filepath.Join(t.TempDir(), "plugins"), db.Plugin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.fetchSidecarManifest(srv.URL); err == nil || !strings.Contains(err.Error(), "too_large") {
		t.Fatalf("oversized manifest err=%v", err)
	}
}

func TestRegisterSidecarManualManifestFallback(t *testing.T) {
	db := openPluginTestDB(t)
	svc, err := NewService(filepath.Join(t.TempDir(), "plugins"), db.Plugin)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	// Service WITHOUT /plugin.json (like CLIProxyAPI's built-in CPAMC page).
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	// Without manual fields → registration fails (no manifest).
	if _, err := svc.RegisterSidecar(srv.URL, "", nil); err == nil {
		t.Fatal("expected failure without manifest or manual fields")
	}

	// With manual identity → registers, page_path honored.
	manual := &SidecarManifest{
		ID: "cpa-console", Version: "1.0.0", Name: "CPA Console",
		PagePath: "/management.html", HealthPath: "healthz", APIPrefix: "/v0/management",
	}
	rec, err := svc.RegisterSidecar(srv.URL, "", manual)
	if err != nil {
		t.Fatalf("RegisterSidecar manual: %v", err)
	}
	if rec == nil || !rec.Enabled || rec.Source != "sidecar" {
		t.Fatalf("record = %+v", rec)
	}
	spec, err := svc.SidecarFor("cpa-console")
	if err != nil || spec == nil {
		t.Fatalf("SidecarFor: %v %v", spec, err)
	}
	if spec.PagePath != "management.html" {
		t.Fatalf("page path = %q, want management.html", spec.PagePath)
	}
	if spec.HealthPath != "healthz" {
		t.Fatalf("health path = %q, want healthz", spec.HealthPath)
	}
	forwarders := svc.PrefixForwarders()
	if len(forwarders) != 1 || forwarders[0].Prefix != "/v0/management" || forwarders[0].Spec == nil {
		t.Fatalf("prefix forwarders = %+v", forwarders)
	}

	// The forwarding snapshot is rebuilt from persisted manifests at startup;
	// serving a root API path must not depend on fetching the remote catalog.
	restarted, err := NewService(filepath.Join(t.TempDir(), "plugins-restarted"), db.Plugin)
	if err != nil {
		t.Fatal(err)
	}
	forwarders = restarted.PrefixForwarders()
	if len(forwarders) != 1 || forwarders[0].Prefix != "/v0/management" {
		t.Fatalf("restarted prefix forwarders = %+v", forwarders)
	}
}

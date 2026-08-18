package plugins

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestSidecarHelperProcess is the packaged sidecar executable inside test
// plugin archives. When the extracted copy of the test binary is launched by
// startManagedPlugin, META_GATEWAY_PLUGIN_ADDR is set and this test spins up
// a real HTTP server on that address, so the whole install -> health-check
// lifecycle is exercised against a genuine child process. As a plain test run
// the env var is absent and the function returns immediately.
func TestSidecarHelperProcess(t *testing.T) {
	addr := os.Getenv("META_GATEWAY_PLUGIN_ADDR")
	if addr == "" {
		return
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "helper listen: %v\n", err)
		os.Exit(1)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html><h1>helper plugin</h1></html>"))
	})
	_ = http.Serve(listener, mux)
}

// pluginExecutableName returns the entrypoint filename for the current
// platform (Windows requires the .exe suffix for direct CreateProcess).
func pluginExecutableName() string {
	if runtime.GOOS == "windows" {
		return "plugin.exe"
	}
	return "plugin"
}

// buildPluginArchive creates a zip package with plugin.json plus a copy of the
// running test binary as the executable. Extra entries let tests inject
// traversal/symlink payloads. Returns the archive bytes and its SHA-256.
func buildPluginArchive(t *testing.T, id, name, version string, extra ...string) ([]byte, string) {
	t.Helper()
	selfPath, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	selfBinary, err := os.ReadFile(selfPath)
	if err != nil {
		t.Fatalf("read test binary: %v", err)
	}

	manifest, err := json.Marshal(map[string]any{
		"id":          id,
		"name":        name,
		"version":     version,
		"description": "install lifecycle test plugin",
		"entrypoint":  pluginExecutableName(),
		"run_args":    []any{"-test.run=TestSidecarHelperProcess"},
	})
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	writeEntry := func(name string, body []byte) {
		t.Helper()
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	writeEntry("plugin.json", manifest)
	writeEntry(pluginExecutableName(), selfBinary)
	for _, path := range extra {
		writeEntry(path, []byte("extra"))
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	sum := sha256.Sum256(buf.Bytes())
	return buf.Bytes(), hex.EncodeToString(sum[:])
}

// marketWithArtifact serves a registry (one plugin with a direct install block
// pointing at /pkg.zip on the same origin) plus the artifact archive. The
// handler reads the registry body lazily so the artifact URL (known only after
// the server starts) can be filled in before any request. `sha` overrides the
// declared artifact checksum for corruption tests.
func marketWithArtifact(t *testing.T, plugin map[string]any, archive []byte, sha string) (*Service, *httptest.Server) {
	t.Helper()
	var registryBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/registry.json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(registryBody)
		case "/pkg.zip":
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	if sha == "" {
		sha = sha256Hex(archive)
	}
	plugin["install"] = map[string]any{
		"type": "direct",
		"artifacts": []map[string]any{
			{
				"goos":   runtime.GOOS,
				"goarch": runtime.GOARCH,
				"url":    srv.URL + "/pkg.zip",
				"sha256": sha,
				"size":   len(archive),
			},
		},
	}
	var err error
	registryBody, err = json.Marshal(map[string]any{
		"schema_version": 1,
		"plugins":        []map[string]any{plugin},
	})
	if err != nil {
		t.Fatalf("marshal registry: %v", err)
	}

	db := openPluginTestDB(t)
	svc, err := NewService(filepath.Join(t.TempDir(), "plugins"), db.Plugin)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	svc.SetMarketURLs([]string{srv.URL + "/registry.json"})
	return svc, srv
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func installMarketEntry(t *testing.T, svc *Service, id string) error {
	t.Helper()
	_, err := svc.InstallMarketFrom(context.Background(), id, "", "")
	return err
}

func TestMarketInstallDirectLifecycle(t *testing.T) {
	archive, _ := buildPluginArchive(t, "demo.helper", "Demo Helper", "1.2.0")
	svc, _ := marketWithArtifact(t, map[string]any{
		"id":      "demo.helper",
		"name":    "Demo Helper",
		"version": "1.2.0",
	}, archive, "")

	if err := installMarketEntry(t, svc, "demo.helper"); err != nil {
		t.Fatalf("InstallMarketFrom: %v", err)
	}

	rec, err := svc.store.Get("demo.helper")
	if err != nil || rec == nil {
		t.Fatalf("plugin record missing: rec=%+v err=%v", rec, err)
	}
	if !rec.Enabled || rec.Status != StatusInstalled {
		t.Fatalf("expected installed+enabled, got %+v", rec)
	}
	if rec.Version != "1.2.0" {
		t.Fatalf("version = %q, want 1.2.0", rec.Version)
	}
	if !strings.HasPrefix(rec.Source, "market:") {
		t.Fatalf("source = %q, want market: prefix", rec.Source)
	}

	// The managed sidecar must be up and answer its health endpoint.
	entry, err := svc.catalogEntry("demo.helper")
	if err != nil || entry.Sidecar == nil || entry.Sidecar.URL == "" {
		t.Fatalf("catalog entry missing sidecar: %+v err=%v", entry, err)
	}
	healthURL := strings.TrimRight(entry.Sidecar.URL, "/") + "/healthz"
	resp, err := http.Get(healthURL)
	if err != nil {
		t.Fatalf("health probe: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d", resp.StatusCode)
	}

	// Restart recovery must rebuild the catalog entry from the DB record.
	svc.mu.Lock()
	svc.remoteCatalog = nil
	svc.mu.Unlock()
	if _, err := svc.catalogEntry("demo.helper"); err != nil {
		t.Fatalf("catalog recovery after restart: %v", err)
	}

	// Uninstall must stop the process and remove the directory.
	if err := svc.Uninstall("demo.helper"); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if _, err := os.Stat(filepath.Join(svc.dir, "demo.helper")); !os.IsNotExist(err) {
		t.Fatalf("plugin dir still present after uninstall")
	}
}

func TestMarketInstallRejectsChecksumMismatch(t *testing.T) {
	archive, _ := buildPluginArchive(t, "demo.helper", "Demo Helper", "1.2.0")
	svc, _ := marketWithArtifact(t, map[string]any{
		"id":      "demo.helper",
		"name":    "Demo Helper",
		"version": "1.2.0",
	}, archive, strings.Repeat("0", 64))

	err := installMarketEntry(t, svc, "demo.helper")
	if err == nil || !strings.Contains(err.Error(), "plugin_artifact_checksum_mismatch") {
		t.Fatalf("expected checksum mismatch, got %v", err)
	}
	rec, _ := svc.store.Get("demo.helper")
	if rec != nil {
		t.Fatalf("no record should remain after failed install: %+v", rec)
	}
	if _, statErr := os.Stat(filepath.Join(svc.dir, "demo.helper")); !os.IsNotExist(statErr) {
		t.Fatalf("plugin dir left behind after failed install")
	}
}

func TestMarketInstallRejectsPathTraversal(t *testing.T) {
	archive, _ := buildPluginArchive(t, "demo.helper", "Demo Helper", "1.2.0", "../evil.txt")
	svc, _ := marketWithArtifact(t, map[string]any{
		"id":      "demo.helper",
		"name":    "Demo Helper",
		"version": "1.2.0",
	}, archive, "")

	err := installMarketEntry(t, svc, "demo.helper")
	if err == nil || !strings.Contains(err.Error(), "plugin_archive_path_escape") {
		t.Fatalf("expected path escape rejection, got %v", err)
	}
	rec, _ := svc.store.Get("demo.helper")
	if rec != nil {
		t.Fatalf("no record should remain after failed install: %+v", rec)
	}
	if _, statErr := os.Stat(filepath.Join(svc.dir, "demo.helper")); !os.IsNotExist(statErr) {
		t.Fatalf("plugin dir left behind after failed install")
	}
}

func TestMarketInstallRejectsMissingManifest(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("random.txt")
	_, _ = w.Write([]byte("not a plugin"))
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	svc, _ := marketWithArtifact(t, map[string]any{
		"id":      "demo.helper",
		"name":    "Demo Helper",
		"version": "1.2.0",
	}, buf.Bytes(), "")

	err := installMarketEntry(t, svc, "demo.helper")
	if err == nil || !strings.Contains(err.Error(), "plugin_manifest_missing") {
		t.Fatalf("expected missing manifest rejection, got %v", err)
	}
	rec, _ := svc.store.Get("demo.helper")
	if rec != nil {
		t.Fatalf("no record should remain after failed install: %+v", rec)
	}
}

func TestMarketInstallRejectsSymlink(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	manifestBody, _ := json.Marshal(map[string]any{
		"id": "demo.helper", "name": "Demo Helper", "version": "1.2.0",
		"entrypoint": pluginExecutableName(),
		"run_args":   []any{"-test.run=TestSidecarHelperProcess"},
	})
	w, _ := zw.Create("plugin.json")
	_, _ = w.Write(manifestBody)
	link := &zip.FileHeader{Name: "link.txt", Method: zip.Deflate}
	link.SetMode(os.ModeSymlink | 0o777)
	lw, _ := zw.CreateHeader(link)
	_, _ = lw.Write([]byte("target"))
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	svc, _ := marketWithArtifact(t, map[string]any{
		"id":      "demo.helper",
		"name":    "Demo Helper",
		"version": "1.2.0",
	}, buf.Bytes(), "")

	err := installMarketEntry(t, svc, "demo.helper")
	if err == nil || !strings.Contains(err.Error(), "plugin_archive_symlink_rejected") {
		t.Fatalf("expected symlink rejection, got %v", err)
	}
}

func TestMarketInstallRejectsEntrypointEscape(t *testing.T) {
	// The zip contains a valid plugin.json whose entrypoint points outside the
	// package directory (should never execute).
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	manifestBody, _ := json.Marshal(map[string]any{
		"id": "demo.helper", "name": "Demo Helper", "version": "1.2.0",
		"entrypoint": "../../../../bin/nope.exe",
	})
	w, _ := zw.Create("plugin.json")
	_, _ = w.Write(manifestBody)
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	svc, _ := marketWithArtifact(t, map[string]any{
		"id":      "demo.helper",
		"name":    "Demo Helper",
		"version": "1.2.0",
	}, buf.Bytes(), "")

	err := installMarketEntry(t, svc, "demo.helper")
	if err == nil || !strings.Contains(err.Error(), "plugin_manifest_entrypoint_invalid") {
		t.Fatalf("expected entrypoint escape rejection, got %v", err)
	}
}

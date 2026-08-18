package plugins

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	managedStartupTimeout = 15 * time.Second
	managedHealthTimeout  = 750 * time.Millisecond
)

type managedProcess struct {
	cmd     *exec.Cmd
	done    chan struct{}
	logFile *os.File
}

// StartManaged starts every enabled packaged sidecar recovered from SQLite.
// A broken optional plugin is reported to the caller but does not prevent the
// gateway itself from starting; the Store can still disable or uninstall it.
func (s *Service) StartManaged(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	records, err := s.store.List()
	if err != nil {
		return err
	}
	var failures []string
	for _, record := range records {
		if !record.Enabled || record.Status != StatusInstalled || record.MetaJSON == "" {
			continue
		}
		var manifest Manifest
		if err := json.Unmarshal([]byte(record.MetaJSON), &manifest); err != nil || manifest.Sidecar == nil || !manifest.Sidecar.Managed {
			continue
		}
		if err := s.startManagedPlugin(ctx, record.ID, manifest.Sidecar); err != nil {
			failures = append(failures, record.ID+": "+err.Error())
			continue
		}
		if err := s.persistManagedSpec(record.ID, manifest.Sidecar); err != nil {
			failures = append(failures, record.ID+": persist runtime: "+err.Error())
		}
	}
	if err := s.reloadEnabled(); err != nil {
		return err
	}
	if len(failures) > 0 {
		return fmt.Errorf("managed plugin startup failed: %s", strings.Join(failures, "; "))
	}
	return nil
}

// StopManaged terminates all processes owned by the gateway. It is called
// before the database closes so no plugin can continue using stale state.
func (s *Service) StopManaged(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s.processMu.Lock()
	ids := make([]string, 0, len(s.processes))
	for id := range s.processes {
		ids = append(ids, id)
	}
	s.processMu.Unlock()
	var failures []string
	for _, id := range ids {
		if err := s.stopManagedPlugin(ctx, id); err != nil {
			failures = append(failures, id+": "+err.Error())
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("managed plugin shutdown failed: %s", strings.Join(failures, "; "))
	}
	return nil
}

func (s *Service) startManagedPlugin(ctx context.Context, id string, spec *SidecarSpec) error {
	if spec == nil || !spec.Managed {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validatePluginID(id); err != nil {
		return err
	}
	pluginDir, err := s.safePluginDir(id)
	if err != nil {
		return err
	}
	entrypoint, err := resolvePluginPath(pluginDir, spec.Entrypoint)
	if err != nil {
		return fmt.Errorf("plugin_entrypoint_invalid: %w", err)
	}
	info, err := os.Stat(entrypoint)
	if err != nil {
		return fmt.Errorf("plugin_entrypoint_unavailable: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("plugin_entrypoint_not_file")
	}
	if err := makePluginExecutable(entrypoint); err != nil {
		return fmt.Errorf("plugin_entrypoint_not_executable: %w", err)
	}

	s.processMu.Lock()
	if current := s.processes[id]; current != nil {
		select {
		case <-current.done:
			delete(s.processes, id)
		default:
			s.processMu.Unlock()
			return nil
		}
	}
	s.processMu.Unlock()

	addr, err := reservePluginAddress()
	if err != nil {
		return fmt.Errorf("plugin_port_allocate: %w", err)
	}
	if strings.TrimSpace(spec.APIKey) == "" {
		key, err := newPluginKey()
		if err != nil {
			return fmt.Errorf("plugin_key_generate: %w", err)
		}
		spec.APIKey = key
	}
	spec.URL = "http://" + addr
	if strings.TrimSpace(spec.HealthPath) == "" {
		spec.HealthPath = "healthz"
	}
	args := substitutePluginArgs(spec.RunArgs, id, pluginDir, addr, spec.APIKey)
	cmd := exec.Command(entrypoint, args...)
	cmd.Dir = pluginDir
	cmd.Env = append(filterPluginEnv(os.Environ()),
		"META_GATEWAY_PLUGIN_ID="+id,
		"META_GATEWAY_PLUGIN_DIR="+pluginDir,
		"META_GATEWAY_PLUGIN_ADDR="+addr,
		"META_GATEWAY_PLUGIN_PORT="+portFromAddr(addr),
		"META_GATEWAY_PLUGIN_KEY="+spec.APIKey,
	)
	logPath := filepath.Join(pluginDir, "plugin.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("plugin_log_open: %w", err)
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return fmt.Errorf("plugin_start: %w", err)
	}
	process := &managedProcess{cmd: cmd, done: make(chan struct{}), logFile: logFile}
	s.processMu.Lock()
	s.processes[id] = process
	s.processMu.Unlock()
	go s.waitManagedProcess(id, process)

	startupCtx, cancel := context.WithTimeout(ctx, managedStartupTimeout)
	defer cancel()
	if err := s.waitManagedHealth(startupCtx, process, spec); err != nil {
		_ = s.stopManagedPlugin(context.Background(), id)
		return err
	}
	return nil
}

func (s *Service) waitManagedProcess(id string, process *managedProcess) {
	_ = process.cmd.Wait()
	_ = process.logFile.Close()
	close(process.done)
	s.processMu.Lock()
	if s.processes[id] == process {
		delete(s.processes, id)
	}
	s.processMu.Unlock()
}

func (s *Service) waitManagedHealth(ctx context.Context, process *managedProcess, spec *SidecarSpec) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		select {
		case <-process.done:
			if lastErr == nil {
				lastErr = errors.New("process exited before health check")
			}
			return fmt.Errorf("plugin_health_check_failed: %w", lastErr)
		default:
		}
		healthCtx, cancel := context.WithTimeout(ctx, managedHealthTimeout)
		err := s.healthCheckContext(healthCtx, spec)
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return fmt.Errorf("plugin_health_check_failed: %w", lastErr)
		case <-process.done:
			return fmt.Errorf("plugin_health_check_failed: process exited: %w", lastErr)
		case <-ticker.C:
		}
	}
}

func (s *Service) stopManagedPlugin(ctx context.Context, id string) error {
	s.processMu.Lock()
	process := s.processes[id]
	if process != nil {
		delete(s.processes, id)
	}
	s.processMu.Unlock()
	if process == nil {
		return nil
	}
	if process.cmd.Process != nil {
		if err := process.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return fmt.Errorf("kill process: %w", err)
		}
	}
	select {
	case <-process.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) persistManagedSpec(id string, spec *SidecarSpec) error {
	record, err := s.store.Get(id)
	if err != nil {
		return err
	}
	if record == nil || record.MetaJSON == "" {
		return ErrNotInstalled
	}
	var manifest Manifest
	if err := json.Unmarshal([]byte(record.MetaJSON), &manifest); err != nil {
		return fmt.Errorf("parse persisted manifest: %w", err)
	}
	manifest.Sidecar = spec
	body, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	pluginDir, err := s.safePluginDir(id)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(pluginDir, ".meta-gateway.json"), body, 0o600); err != nil {
		return err
	}
	if err := s.store.UpdateMeta(id, string(body)); err != nil {
		return err
	}
	s.mu.Lock()
	for i := range s.remoteCatalog {
		if s.remoteCatalog[i].ID == id && s.remoteCatalog[i].Sidecar != nil {
			copySpec := *spec
			s.remoteCatalog[i].Sidecar = &copySpec
		}
	}
	s.mu.Unlock()
	return nil
}

func (s *Service) healthCheckContext(ctx context.Context, spec *SidecarSpec) error {
	if spec == nil || strings.TrimSpace(spec.URL) == "" {
		return errors.New("no sidecar spec")
	}
	healthURL := strings.TrimRight(spec.URL, "/") + "/" + strings.TrimPrefix(spec.HealthPath, "/")
	req, err := httpNewRequestWithContext(ctx, healthURL)
	if err != nil {
		return err
	}
	if spec.APIKey != "" {
		req.Header.Set("X-Plugin-Key", spec.APIKey)
	}
	client := s.sidecarClient
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

// These small wrappers keep process.go's lifecycle code easy to test without
// exposing an alternate HTTP client abstraction throughout the package.
var httpNewRequestWithContext = func(ctx context.Context, rawURL string) (*http.Request, error) {
	return http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
}

func resolvePluginPath(baseDir, relative string) (string, error) {
	relative = strings.TrimSpace(relative)
	if relative == "" || filepath.IsAbs(relative) || strings.ContainsAny(relative, `\\?#%`) {
		return "", errors.New("entrypoint must be a relative path without encoded separators")
	}
	clean := filepath.Clean(relative)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("entrypoint escapes plugin directory")
	}
	base, err := filepath.Abs(baseDir)
	if err != nil {
		return "", err
	}
	target, err := filepath.Abs(filepath.Join(base, clean))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(base, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", errors.New("entrypoint escapes plugin directory")
	}
	return target, nil
}

func makePluginExecutable(path string) error {
	if os.PathSeparator == '\\' {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	mode := info.Mode().Perm()
	if mode&0o111 == 0 {
		mode |= 0o700
	}
	return os.Chmod(path, mode)
}

func reservePluginAddress() (string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		return "", err
	}
	return addr, nil
}

func portFromAddr(addr string) string {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return ""
	}
	return port
}

func substitutePluginArgs(args []string, id, dir, addr, key string) []string {
	values := map[string]string{
		"{id}":         id,
		"{plugin_dir}": dir,
		"{addr}":       addr,
		"{port}":       portFromAddr(addr),
		"{key}":        key,
	}
	out := make([]string, len(args))
	for i, arg := range args {
		for placeholder, value := range values {
			arg = strings.ReplaceAll(arg, placeholder, value)
		}
		out[i] = arg
	}
	return out
}

func newPluginKey() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// filterPluginEnv keeps only a minimal environment for managed sidecar
// processes. Plugins are third-party executables and must never inherit the
// gateway's secrets (ADMIN_TOKEN / MASTER_KEY / proxy credentials); anything
// not on the allowlist is dropped.
func filterPluginEnv(env []string) []string {
	const prefix = "META_GATEWAY_PLUGIN_"
	allowed := map[string]bool{
		"PATH": true, "HOME": true, "TMPDIR": true, "TEMP": true, "TMP": true,
		"TZ": true, "LANG": true, "LC_ALL": true,
		"HTTP_PROXY": true, "HTTPS_PROXY": true, "NO_PROXY": true,
		"http_proxy": true, "https_proxy": true, "no_proxy": true,
		"ALL_PROXY": true, "all_proxy": true,
	}
	out := make([]string, 0, len(env))
	for _, kv := range env {
		key := kv
		if index := strings.IndexByte(kv, '='); index >= 0 {
			key = kv[:index]
		}
		if allowed[key] || strings.HasPrefix(key, prefix) {
			out = append(out, kv)
		}
	}
	return out
}

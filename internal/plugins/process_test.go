package plugins

import (
	"net"
	"strings"
	"testing"
)

func TestFilterPluginEnv(t *testing.T) {
	// Plugin processes must not inherit gateway secrets.
	filtered := filterPluginEnv([]string{
		"PATH=/usr/bin", "ADMIN_TOKEN=super-secret", "MASTER_KEY=master-secret",
		"HTTP_PROXY=http://proxy", "META_GATEWAY_PLUGIN_ID=kept", "HOME=/root",
	})
	joined := strings.Join(filtered, "\n")
	if strings.Contains(joined, "ADMIN_TOKEN") || strings.Contains(joined, "MASTER_KEY") {
		t.Fatalf("secret env leaked: %q", joined)
	}
	for _, want := range []string{"PATH=/usr/bin", "HTTP_PROXY=http://proxy", "HOME=/root", "META_GATEWAY_PLUGIN_ID=kept"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing env %q in %q", want, joined)
		}
	}
}

// A managed plugin's channel points at {url}/v1, so the address has to survive
// a restart. A previous loopback address is reused while it is free; a URL that
// belongs to someone else is never adopted.
func TestReservePluginAddressReusesFreeLoopbackAddress(t *testing.T) {
	free, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	previous := "http://" + free.Addr().String()
	free.Close()

	got, err := reservePluginAddress(previous)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if got != strings.TrimPrefix(previous, "http://") {
		t.Fatalf("address not reused: got %q want %q", got, previous)
	}
}

func TestReservePluginAddressIgnoresForeignURL(t *testing.T) {
	for _, previous := range []string{"", "http://192.168.1.5:9000", "https://127.0.0.1:9000", "http://127.0.0.1:9000", "not a url"} {
		got, err := reservePluginAddress(previous)
		if err != nil {
			t.Fatalf("reserve(%q): %v", previous, err)
		}
		if previous == "http://127.0.0.1:9000" {
			// Still loopback: adoptable when free, so either answer is fine —
			// what must never happen is an address outside loopback.
			if host, _, err := net.SplitHostPort(got); err != nil || host != "127.0.0.1" {
				t.Fatalf("reserve(%q) returned non-loopback %q", previous, got)
			}
			continue
		}
		if strings.Contains(got, "192.168.1.5") || strings.HasPrefix(got, "https") || got == "127.0.0.1:9000" {
			t.Fatalf("reserve(%q) adopted a foreign address: %q", previous, got)
		}
		if host, _, err := net.SplitHostPort(got); err != nil || host != "127.0.0.1" {
			t.Fatalf("reserve(%q) returned %q, want loopback", previous, got)
		}
	}
}

// A JavaScript plugin ships its program and lets the host supply the runtime:
// the manifest names "node" instead of carrying one 84 MB executable per
// platform. Only a bare name qualifies — a separator means "a file in the
// package", which stays confined to the plugin directory.
func TestPluginRuntimeName(t *testing.T) {
	for _, name := range []string{"node", "python3", "node.exe"} {
		if !pluginRuntimeName(name) {
			t.Fatalf("%q should be treated as a host runtime", name)
		}
	}
	for _, name := range []string{"", " ", ".", "..", ".hidden", "./node", "bin/node", `bin\node`, "../node"} {
		if pluginRuntimeName(name) {
			t.Fatalf("%q must not be treated as a host runtime", name)
		}
	}
}

func TestPluginEntrypointResolvesHostRuntime(t *testing.T) {
	dir := t.TempDir()
	for _, declared := range []string{"node", "NO-SUCH-RUNTIME-FOR-PLUGINS"} {
		got, err := pluginEntrypoint(dir, declared)
		if declared == "node" {
			if err != nil {
				t.Skipf("node is not installed on this host: %v", err)
			}
			if got != "node" {
				t.Fatalf("entrypoint = %q, want the bare runtime name", got)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), "plugin_entrypoint_unavailable") {
			t.Fatalf("missing runtime should fail with plugin_entrypoint_unavailable, got %v", err)
		}
	}
}

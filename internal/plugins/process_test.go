package plugins

import (
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

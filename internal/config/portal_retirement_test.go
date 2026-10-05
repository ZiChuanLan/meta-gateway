package config

import "testing"

func TestLoadIgnoresRetiredPortalEnvironment(t *testing.T) {
	t.Setenv("METRICS_TOKEN", "retirement-metrics")
	t.Setenv("PORTAL_ENABLED", "true")
	t.Setenv("PORTAL_SESSION_TTL_HOURS", "obsolete-value")
	t.Setenv("PORTAL_BASE_URL", "obsolete-value")
	t.Setenv("PORTAL_GITHUB_CLIENT_ID", "old-client")
	t.Setenv("PORTAL_GITHUB_CLIENT_SECRET", "")
	t.Setenv("PORTAL_LINUXDO_MIN_TRUST_LEVEL", "obsolete-value")
	if _, err := Load(); err != nil {
		t.Fatalf("retired portal configuration should not prevent startup: %v", err)
	}
}

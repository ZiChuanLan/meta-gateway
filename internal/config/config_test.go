package config

import "testing"

func TestLoadCheckinDefaultsAndOverrides(t *testing.T) {
	t.Setenv("METRICS_TOKEN", "metrics-test-token")
	t.Setenv("CHECKIN_ENABLED", "")
	t.Setenv("CHECKIN_CRON", "")
	defaults, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if defaults.CheckinEnabled || defaults.CheckinCron != "0 8 * * *" {
		t.Fatalf("defaults=%+v", defaults)
	}

	t.Setenv("CHECKIN_ENABLED", "true")
	t.Setenv("CHECKIN_CRON", "15 7 * * 1-5")
	overrides, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !overrides.CheckinEnabled || overrides.CheckinCron != "15 7 * * 1-5" {
		t.Fatalf("overrides=%+v", overrides)
	}
}

func TestLoadRejectsInvalidSecurityConfiguration(t *testing.T) {
	t.Setenv("METRICS_TOKEN", "metrics-test-token")
	for _, test := range []struct{ key, value string }{
		{"CHECKIN_ENABLED", "maybe"},
		{"RETRY_TIMES", "-1"},
		{"OUTBOUND_ALLOW_HOSTS", "https://internal.example"},
		{"OUTBOUND_ALLOW_HOSTS", "127.0.0.1"},
		{"OUTBOUND_ALLOW_CIDRS", "not-a-cidr"},
	} {
		t.Run(test.key+"="+test.value, func(t *testing.T) {
			t.Setenv(test.key, test.value)
			if _, err := Load(); err == nil {
				t.Fatal("expected configuration error")
			}
		})
	}
}

func TestLoadOutboundAllowlist(t *testing.T) {
	t.Setenv("METRICS_TOKEN", "metrics-test-token")
	t.Setenv("OUTBOUND_ALLOW_HOSTS", "Internal.Example., internal.example")
	t.Setenv("OUTBOUND_ALLOW_CIDRS", "10.20.0.0/16, fd00::/8")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.OutboundAllowHosts) != 1 || cfg.OutboundAllowHosts[0] != "internal.example" {
		t.Fatalf("hosts=%v", cfg.OutboundAllowHosts)
	}
	if len(cfg.OutboundAllowCIDRs) != 2 {
		t.Fatalf("cidrs=%v", cfg.OutboundAllowCIDRs)
	}
}

func TestLoadRequiresProtectedMetrics(t *testing.T) {
	t.Setenv("METRICS_TOKEN", "")
	t.Setenv("TRUSTED_SCRAPER_CIDRS", "")
	if _, err := Load(); err == nil {
		t.Fatal("expected unprotected metrics configuration to fail")
	}
	t.Setenv("TRUSTED_SCRAPER_CIDRS", "10.0.0.0/8")
	if _, err := Load(); err != nil {
		t.Fatalf("trusted scraper should protect metrics: %v", err)
	}
}

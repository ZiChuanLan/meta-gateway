package config

import "testing"

func TestLoadCheckinDefaultsAndOverrides(t *testing.T) {
	t.Setenv("METRICS_TOKEN", "metrics-test-token")
	t.Setenv("CHECKIN_ENABLED", "")
	t.Setenv("CHECKIN_CRON", "")
	t.Setenv("CHECKIN_TZ", "")
	defaults, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if defaults.CheckinEnabled || defaults.CheckinCron != "0 8 * * *" || defaults.CheckinTZ != "" {
		t.Fatalf("defaults=%+v", defaults)
	}
	if loc := defaults.CheckinLocation(); loc == nil {
		t.Fatal("CheckinLocation must never be nil")
	}

	t.Setenv("CHECKIN_ENABLED", "true")
	t.Setenv("CHECKIN_CRON", "15 7 * * 1-5")
	t.Setenv("CHECKIN_TZ", "Asia/Shanghai")
	overrides, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !overrides.CheckinEnabled || overrides.CheckinCron != "15 7 * * 1-5" || overrides.CheckinTZ != "Asia/Shanghai" {
		t.Fatalf("overrides=%+v", overrides)
	}
	loc := overrides.CheckinLocation()
	if loc.String() != "Asia/Shanghai" {
		t.Fatalf("CheckinLocation=%v", loc)
	}
}

func TestLoadRejectsInvalidCheckinTimezone(t *testing.T) {
	t.Setenv("METRICS_TOKEN", "metrics-test-token")
	t.Setenv("CHECKIN_TZ", "Not/AZone")
	if _, err := Load(); err == nil {
		t.Fatal("expected invalid CHECKIN_TZ to fail")
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

func TestLoadWebDAVDefaultsAndOverrides(t *testing.T) {
	t.Setenv("METRICS_TOKEN", "metrics-test-token")
	t.Setenv("WEBDAV_SYNC_ENABLED", "")
	t.Setenv("WEBDAV_CRON", "")
	t.Setenv("WEBDAV_MAX_BYTES", "")
	defaults, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if defaults.WebDAVSyncEnabled || defaults.WebDAVCron != "0 */6 * * *" || defaults.WebDAVMaxBytes != 10<<20 {
		t.Fatalf("defaults=%+v", defaults)
	}

	t.Setenv("WEBDAV_SYNC_ENABLED", "true")
	t.Setenv("WEBDAV_URL", "https://dav.example.com/files/")
	t.Setenv("WEBDAV_USERNAME", "user")
	t.Setenv("WEBDAV_PASSWORD", "pass")
	t.Setenv("WEBDAV_BACKUP_PASSWORD", "enc")
	t.Setenv("WEBDAV_CRON", "30 1 * * *")
	t.Setenv("WEBDAV_MAX_BYTES", "2097152")
	overrides, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !overrides.WebDAVSyncEnabled || overrides.WebDAVURL != "https://dav.example.com/files/" ||
		overrides.WebDAVUsername != "user" || overrides.WebDAVPassword != "pass" ||
		overrides.WebDAVBackupPassword != "enc" || overrides.WebDAVCron != "30 1 * * *" ||
		overrides.WebDAVMaxBytes != 2097152 {
		t.Fatalf("overrides=%+v", overrides)
	}
}

func TestLoadConnectionPoolDefaultsAndRanges(t *testing.T) {
	t.Setenv("METRICS_TOKEN", "metrics-test-token")
	t.Setenv("SQLITE_MAX_OPEN_CONNS", "")
	t.Setenv("OUTBOUND_MAX_IDLE_CONNS", "")
	t.Setenv("OUTBOUND_MAX_IDLE_CONNS_PER_HOST", "")
	defaults, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if defaults.SQLiteMaxOpenConns != 4 {
		t.Fatalf("SQLiteMaxOpenConns default=%d, want 4", defaults.SQLiteMaxOpenConns)
	}
	if defaults.OutboundMaxIdleConns != 512 {
		t.Fatalf("OutboundMaxIdleConns default=%d, want 512", defaults.OutboundMaxIdleConns)
	}
	if defaults.OutboundMaxIdleConnsPerHost != 64 {
		t.Fatalf("OutboundMaxIdleConnsPerHost default=%d, want 64", defaults.OutboundMaxIdleConnsPerHost)
	}

	t.Setenv("SQLITE_MAX_OPEN_CONNS", "8")
	t.Setenv("OUTBOUND_MAX_IDLE_CONNS", "1024")
	t.Setenv("OUTBOUND_MAX_IDLE_CONNS_PER_HOST", "128")
	overrides, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if overrides.SQLiteMaxOpenConns != 8 || overrides.OutboundMaxIdleConns != 1024 || overrides.OutboundMaxIdleConnsPerHost != 128 {
		t.Fatalf("overrides=%+v", overrides)
	}

	t.Setenv("SQLITE_MAX_OPEN_CONNS", "0")
	if _, err := Load(); err == nil {
		t.Fatal("SQLITE_MAX_OPEN_CONNS=0 must be rejected")
	}
	t.Setenv("SQLITE_MAX_OPEN_CONNS", "17")
	if _, err := Load(); err == nil {
		t.Fatal("SQLITE_MAX_OPEN_CONNS=17 must be rejected")
	}
	t.Setenv("SQLITE_MAX_OPEN_CONNS", "")
	t.Setenv("OUTBOUND_MAX_IDLE_CONNS", "-1")
	if _, err := Load(); err == nil {
		t.Fatal("OUTBOUND_MAX_IDLE_CONNS=-1 must be rejected")
	}
}

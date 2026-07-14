package config

import "testing"

func TestLoadCheckinDefaultsAndOverrides(t *testing.T) {
	t.Setenv("CHECKIN_ENABLED", "")
	t.Setenv("CHECKIN_CRON", "")
	defaults := Load()
	if defaults.CheckinEnabled || defaults.CheckinCron != "0 8 * * *" {
		t.Fatalf("defaults=%+v", defaults)
	}

	t.Setenv("CHECKIN_ENABLED", "true")
	t.Setenv("CHECKIN_CRON", "15 7 * * 1-5")
	overrides := Load()
	if !overrides.CheckinEnabled || overrides.CheckinCron != "15 7 * * 1-5" {
		t.Fatalf("overrides=%+v", overrides)
	}
}

package store_test

import (
	"testing"

	"github.com/lan/meta-gateway/internal/store"
)

func TestAlertRuleLifecycle(t *testing.T) {
	db := openTestDB(t)
	rule := &store.AlertRule{
		Name:             "low availability",
		Metric:           "channel_availability",
		Operator:         "lt",
		Threshold:        0.8,
		WindowSeconds:    3600,
		SustainedSeconds: 300,
		CooldownSeconds:  900,
		Level:            "warning",
		Enabled:          true,
	}
	if err := db.AlertRule.Upsert(rule); err != nil {
		t.Fatal(err)
	}
	if rule.ID == 0 {
		t.Fatal("create did not assign id")
	}
	got, err := db.AlertRule.Get(rule.ID)
	if err != nil || got == nil {
		t.Fatalf("get = %v, %v", got, err)
	}
	if got.Name != "low availability" || got.Threshold != 0.8 {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}
	// Update.
	got.Enabled = false
	got.Threshold = 0.9
	if err := db.AlertRule.Upsert(got); err != nil {
		t.Fatal(err)
	}
	reloaded, _ := db.AlertRule.Get(rule.ID)
	if reloaded.Enabled || reloaded.Threshold != 0.9 {
		t.Fatalf("update mismatch: %+v", reloaded)
	}
	// Enabled list excludes disabled rules.
	enabled, _ := db.AlertRule.ListEnabled()
	if len(enabled) != 0 {
		t.Fatalf("ListEnabled = %d rules, want 0", len(enabled))
	}
	all, _ := db.AlertRule.List()
	if len(all) != 1 {
		t.Fatalf("List = %d rules, want 1", len(all))
	}
	// Delete.
	if err := db.AlertRule.Delete(rule.ID); err != nil {
		t.Fatal(err)
	}
	missing, _ := db.AlertRule.Get(rule.ID)
	if missing != nil {
		t.Fatal("delete left the rule behind")
	}
}

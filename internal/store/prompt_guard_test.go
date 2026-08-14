package store_test

import (
	"testing"

	"github.com/lan/meta-gateway/internal/store"
)

func TestPromptGuardLifecycle(t *testing.T) {
	db := openTestDB(t)
	rule := &store.PromptGuardRule{
		Name: "api keys", Pattern: `sk-[A-Za-z0-9]{16,}`, Action: "mask",
		Replacement: "[REDACTED]", Enabled: true,
	}
	if err := db.PromptGuard.Upsert(rule); err != nil {
		t.Fatal(err)
	}
	if rule.ID == 0 {
		t.Fatal("create did not assign id")
	}
	got, err := db.PromptGuard.Get(rule.ID)
	if err != nil || got == nil {
		t.Fatalf("get = %v, %v", got, err)
	}
	if got.Pattern != `sk-[A-Za-z0-9]{16,}` || got.Action != "mask" {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}
	// Update + disable.
	got.Action = "reject"
	got.Enabled = false
	if err := db.PromptGuard.Upsert(got); err != nil {
		t.Fatal(err)
	}
	reloaded, _ := db.PromptGuard.Get(rule.ID)
	if reloaded.Action != "reject" || reloaded.Enabled {
		t.Fatalf("update mismatch: %+v", reloaded)
	}
	enabled, _ := db.PromptGuard.ListEnabled()
	if len(enabled) != 0 {
		t.Fatalf("ListEnabled = %d, want 0", len(enabled))
	}
	all, _ := db.PromptGuard.List()
	if len(all) != 1 {
		t.Fatalf("List = %d, want 1", len(all))
	}
	if err := db.PromptGuard.Delete(rule.ID); err != nil {
		t.Fatal(err)
	}
	missing, _ := db.PromptGuard.Get(rule.ID)
	if missing != nil {
		t.Fatal("delete left the rule behind")
	}
}

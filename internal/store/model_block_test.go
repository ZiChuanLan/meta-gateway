package store_test

import (
	"testing"
)

func TestModelBlockInsertListUnblock(t *testing.T) {
	db := openTestDB(t)

	blocked, err := db.IsModelBlocked(1, "gpt-4o")
	if err != nil || blocked {
		t.Fatalf("fresh: blocked=%v err=%v", blocked, err)
	}

	if err := db.BlockModel(1, "gpt-4o", "upstream_status_404"); err != nil {
		t.Fatal(err)
	}
	// Duplicate insert is idempotent.
	if err := db.BlockModel(1, "gpt-4o", "upstream_status_404"); err != nil {
		t.Fatal(err)
	}
	blocked, err = db.IsModelBlocked(1, "gpt-4o")
	if err != nil || !blocked {
		t.Fatalf("after block: blocked=%v err=%v", blocked, err)
	}
	// Other channel / other model unaffected.
	other, _ := db.IsModelBlocked(2, "gpt-4o")
	if other {
		t.Fatal("channel 2 should not be blocked")
	}
	other, _ = db.IsModelBlocked(1, "claude-3")
	if other {
		t.Fatal("claude-3 should not be blocked")
	}

	blocks, err := db.ListModelBlocks()
	if err != nil || len(blocks) != 1 {
		t.Fatalf("list: %d blocks err=%v", len(blocks), err)
	}
	if blocks[0].ChannelID != 1 || blocks[0].Model != "gpt-4o" || blocks[0].Reason != "upstream_status_404" {
		t.Fatalf("block = %+v", blocks[0])
	}

	if err := db.UnblockModel(1, "gpt-4o"); err != nil {
		t.Fatal(err)
	}
	blocked, _ = db.IsModelBlocked(1, "gpt-4o")
	if blocked {
		t.Fatal("should be unblocked")
	}
	// Empty model / zero channel are no-ops.
	if err := db.BlockModel(0, "x", "r"); err != nil {
		t.Fatal(err)
	}
	if err := db.BlockModel(1, "", "r"); err != nil {
		t.Fatal(err)
	}
}

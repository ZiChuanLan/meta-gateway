package store_test

import (
	"testing"
	"time"
)

func TestBalanceHistoryInsertListPrune(t *testing.T) {
	db := openTestDB(t)

	now := time.Now().UTC()
	// Seed two channels across three days.
	days := []time.Time{
		now.AddDate(0, 0, -2),
		now.AddDate(0, 0, -1),
		now,
	}
	for i, day := range days {
		if err := db.InsertBalanceHistory(1, "ch-a", int64(1000+i*100), day); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.InsertBalanceHistory(2, "ch-b", 999, days[2]); err != nil {
		t.Fatal(err)
	}

	points, err := db.ListBalanceHistory(2) // last 2 days
	if err != nil {
		t.Fatal(err)
	}
	// Expect ch-a (-1, 0) and ch-b (0) → 3 rows; the -2 day row is outside.
	if len(points) != 3 {
		t.Fatalf("len = %d, want 3 (rows %+v)", len(points), points)
	}
	if points[0].ChannelName != "ch-a" || points[0].Balance != 1100 {
		t.Fatalf("first point = %+v", points[0])
	}
	if points[2].ChannelName != "ch-b" || points[2].Balance != 999 {
		t.Fatalf("last point = %+v", points[2])
	}

	pruned, err := db.PruneBalanceHistory(1) // keep 1 day
	if err != nil {
		t.Fatal(err)
	}
	if pruned != 2 { // ch-a -1 day and ch-a -2 day gone; ch-a 0 and ch-b 0 remain
		t.Fatalf("pruned = %d, want 2", pruned)
	}
	remaining, err := db.ListBalanceHistory(30)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 2 {
		t.Fatalf("remaining = %d, want 2", len(remaining))
	}
}

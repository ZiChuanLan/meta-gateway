package store_test

import (
	"encoding/json"
	"github.com/lan/meta-gateway/internal/store"
	"testing"
	"time"
)

func TestFinanceSnapshotHistoryIsBoundedAndLatestReadable(t *testing.T) {
	db, err := store.OpenTest(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for i := 0; i < 25; i++ {
		if err = db.SaveFinanceSnapshot(1, []byte(`{"balance":1}`), time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err = db.QueryRow("SELECT count(*) FROM finance_snapshots WHERE channel_id=1").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 20 {
		t.Fatalf("rows=%d", count)
	}
	rows, err := db.LatestFinanceSnapshots()
	if err != nil || len(rows) != 1 || !json.Valid(rows[0].Payload) {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
}

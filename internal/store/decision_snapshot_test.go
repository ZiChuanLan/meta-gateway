package store_test

import (
	"testing"
	"time"
)

func TestDecisionSnapshotInsertListPrune(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().UTC()

	if err := db.InsertDecisionSnapshot("req-1", "gpt-4o", 7, 3, 1, []byte(`{"model":"gpt-4o","candidates":[]}`), now); err != nil {
		t.Fatal(err)
	}
	// A second snapshot for the same request (a retry attempt) must keep the
	// latest as the query winner.
	if err := db.InsertDecisionSnapshot("req-1", "gpt-4o", 7, 5, 2, []byte(`{"model":"gpt-4o","candidates":[{"eligible":true}]}`), now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertDecisionSnapshot("req-2", "claude-3", 8, 9, 0, []byte(`{"model":"claude-3"}`), now); err != nil {
		t.Fatal(err)
	}

	snap, err := db.LatestDecisionSnapshot("req-1")
	if err != nil || snap == nil {
		t.Fatalf("latest: %v %v", snap, err)
	}
	if snap.SelectedChannelID != 5 {
		t.Fatalf("selected = %d, want latest attempt 5", snap.SelectedChannelID)
	}
	if snap.Attempt != 2 {
		t.Fatalf("attempt = %d, want 2", snap.Attempt)
	}
	if snap.Model != "gpt-4o" || snap.RouteID != 7 {
		t.Fatalf("snapshot = %+v", snap)
	}
	if string(snap.Payload) != `{"model":"gpt-4o","candidates":[{"eligible":true}]}` {
		t.Fatalf("payload = %s", snap.Payload)
	}

	// Per-attempt lookup returns the snapshot of exactly that attempt; legacy
	// rows (attempt 0) and unknown attempts return nil so callers can fall
	// back to the latest snapshot.
	first, err := db.DecisionSnapshotForAttempt("req-1", 1)
	if err != nil || first == nil {
		t.Fatalf("attempt 1: %v %v", first, err)
	}
	if first.SelectedChannelID != 3 || first.Attempt != 1 {
		t.Fatalf("attempt 1 snapshot = %+v", first)
	}
	if legacy, err := db.DecisionSnapshotForAttempt("req-2", 1); err != nil || legacy != nil {
		t.Fatalf("legacy attempt lookup: %v %v", legacy, err)
	}

	missing, err := db.LatestDecisionSnapshot("req-nope")
	if err != nil || missing != nil {
		t.Fatalf("missing request: %v %v", missing, err)
	}

	// Prune: nothing older than 7 days yet.
	n, err := db.PruneDecisionSnapshots(7)
	if err != nil || n != 0 {
		t.Fatalf("prune fresh: n=%d err=%v", n, err)
	}
	// Age one snapshot beyond retention and prune again.
	if err := db.InsertDecisionSnapshot("req-old", "m", 0, 0, 1, []byte(`{}`), now.AddDate(0, 0, -30)); err != nil {
		t.Fatal(err)
	}
	n, err = db.PruneDecisionSnapshots(7)
	if err != nil || n != 1 {
		t.Fatalf("prune old: n=%d err=%v", n, err)
	}
}

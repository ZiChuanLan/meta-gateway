package store_test

import (
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/store"
)

// TestHealthHistoryLifecycle covers append/recent/summary/prune.
func TestHealthHistoryLifecycle(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	now := time.Now().UTC()
	// 3 successes + 1 failure on channel 1, 1 success on channel 2.
	for i := 0; i < 3; i++ {
		if err := db.HealthHistory.Append(1, true, 120, "", now.Add(-time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.HealthHistory.Append(1, false, 0, "timeout", now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := db.HealthHistory.Append(2, true, 90, "", now); err != nil {
		t.Fatal(err)
	}

	recent, err := db.HealthHistory.Recent(1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 4 {
		t.Fatalf("recent len = %d, want 4", len(recent))
	}
	if recent[0].OK != false || recent[0].Verdict != "timeout" || recent[3].OK != true {
		t.Fatalf("recent order/content wrong: %+v", recent)
	}

	// Summary over 24h: channel 1 = 3/4, channel 2 = 1/1.
	summaries, err := db.HealthHistory.Summaries(now.Add(-24 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 2 {
		t.Fatalf("summaries len = %d, want 2", len(summaries))
	}
	var ch1, ch2 *store.ChannelHealthSummary
	for i := range summaries {
		if summaries[i].ChannelID == 1 {
			ch1 = &summaries[i]
		}
		if summaries[i].ChannelID == 2 {
			ch2 = &summaries[i]
		}
	}
	if ch1 == nil || ch1.Total != 4 || ch1.OK != 3 || ch1.Availability != 0.75 {
		t.Fatalf("ch1 summary wrong: %+v", ch1)
	}
	if ch2 == nil || ch2.Total != 1 || ch2.OK != 1 || ch2.Availability != 1 {
		t.Fatalf("ch2 summary wrong: %+v", ch2)
	}

	// Prune: delete everything older than 30 minutes → the failure at -60m is
	// removed; the 3 successes at -1/-2/-3m and ch2's "now" entry survive.
	n, err := db.HealthHistory.Prune(now.Add(-30 * time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("prune removed %d, want 1", n)
	}
	recent, _ = db.HealthHistory.Recent(1, 10)
	if len(recent) != 3 {
		t.Fatalf("after prune recent len = %d, want 3", len(recent))
	}
}

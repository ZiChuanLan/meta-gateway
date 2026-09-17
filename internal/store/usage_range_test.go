package store_test

import (
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

// backdate moves a freshly inserted row onto an explicit created_at. Both
// usage_records and proxy_logs default created_at to datetime('now'), so a
// range test has to rewrite it afterwards.
func backdate(t *testing.T, db *store.DB, table string, id int64, at time.Time) {
	t.Helper()
	if _, err := db.Exec(`UPDATE `+table+` SET created_at = ? WHERE id = ?`,
		at.UTC().Format("2006-01-02 15:04:05"), id); err != nil {
		t.Fatalf("backdate %s: %v", table, err)
	}
}

func seedUsage(t *testing.T, db *store.DB, requestID, model string, tokens int, status int, cost float64, at time.Time) int64 {
	t.Helper()
	id, err := db.Usage.Insert(&domain.UsageRecord{
		RequestID:   requestID,
		ChannelID:   1,
		Model:       model,
		Path:        "chat/completions",
		TotalTokens: tokens,
		Status:      status,
		Cost:        cost,
	})
	if err != nil {
		t.Fatalf("seed usage %s: %v", requestID, err)
	}
	backdate(t, db, "usage_records", id, at)
	return id
}

// A range must actually exclude rows outside it — an RFC3339 bound formatted
// naively would string-compare greater than every stored row and quietly
// return zeros (or everything).
func TestUsageSummaryRangeExcludesOutsideWindow(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().UTC().Truncate(time.Minute)
	seedUsage(t, db, "old", "gpt-a", 100, 200, 0.1, now.Add(-3*time.Hour))
	seedUsage(t, db, "inside", "gpt-a", 200, 200, 0.2, now.Add(-30*time.Minute))
	seedUsage(t, db, "new", "gpt-a", 400, 500, 0.4, now.Add(-time.Minute))

	since := now.Add(-time.Hour)
	until := now.Add(-10 * time.Minute)
	summary, err := db.Usage.SummaryRange(nil, &since, &until)
	if err != nil {
		t.Fatalf("summary range: %v", err)
	}
	if summary.RequestCount != 1 || summary.TotalTokens != 200 {
		t.Fatalf("range summary = %+v, want 1 request / 200 tokens", summary)
	}
	if diff := summary.Cost - 0.2; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("range cost = %v, want 0.2", summary.Cost)
	}

	all, err := db.Usage.SummaryRange(nil, nil, nil)
	if err != nil {
		t.Fatalf("summary all: %v", err)
	}
	if all.RequestCount != 3 || all.TotalTokens != 700 {
		t.Fatalf("unbounded summary = %+v, want 3 requests / 700 tokens", all)
	}

	// SummarySince keeps working as the open-ended special case.
	recent, err := db.Usage.SummarySince(nil, &since)
	if err != nil {
		t.Fatalf("summary since: %v", err)
	}
	if recent.RequestCount != 2 {
		t.Fatalf("since summary requests = %d, want 2", recent.RequestCount)
	}
}

// The series endpoint powers the overview chart, so buckets must be
// contiguous, epoch-aligned and complete even where no request landed.
func TestUsageSeriesBucketsAndAlignment(t *testing.T) {
	db := openTestDB(t)
	base := time.Now().UTC().Truncate(time.Hour).Add(-4 * time.Hour)
	seedUsage(t, db, "s1", "gpt-a", 10, 200, 0.01, base.Add(5*time.Minute))
	seedUsage(t, db, "s2", "gpt-a", 20, 500, 0.02, base.Add(10*time.Minute))
	seedUsage(t, db, "s3", "gpt-b", 30, 200, 0.03, base.Add(2*time.Hour+5*time.Minute))

	series, err := db.Usage.Series(base, base.Add(4*time.Hour), 4)
	if err != nil {
		t.Fatalf("series: %v", err)
	}
	if series.BucketSeconds != 3600 {
		t.Fatalf("bucket seconds = %d, want 3600", series.BucketSeconds)
	}
	if len(series.Requests) != len(series.Tokens) || len(series.Requests) != len(series.Cost) || len(series.Requests) != len(series.Failed) {
		t.Fatalf("series arrays are not parallel: %d/%d/%d/%d",
			len(series.Requests), len(series.Tokens), len(series.Cost), len(series.Failed))
	}
	if got := series.Since.Minute(); got != 0 {
		t.Fatalf("series start is not hour-aligned: %s", series.Since)
	}
	if len(series.Requests) < 5 {
		t.Fatalf("series length = %d, want >= 5 buckets for a 4h span", len(series.Requests))
	}
	if series.Requests[0] != 2 || series.Failed[0] != 1 {
		t.Fatalf("first bucket = %d requests / %d failed, want 2/1", series.Requests[0], series.Failed[0])
	}
	if series.Tokens[0] != 30 {
		t.Fatalf("first bucket tokens = %d, want 30", series.Tokens[0])
	}
	if series.Requests[2] != 1 {
		t.Fatalf("bucket at +2h = %d requests, want 1", series.Requests[2])
	}
	if series.Requests[1] != 0 {
		t.Fatalf("empty bucket should stay 0, got %d", series.Requests[1])
	}
	var total int
	for _, n := range series.Requests {
		total += n
	}
	if total != 3 {
		t.Fatalf("series total = %d, want 3", total)
	}
}

func TestUsageTopModelsRanksInsideWindow(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().UTC().Truncate(time.Minute)
	seedUsage(t, db, "m1", "gpt-heavy", 900, 200, 0, now.Add(-10*time.Minute))
	seedUsage(t, db, "m2", "gpt-heavy", 100, 500, 0, now.Add(-20*time.Minute))
	seedUsage(t, db, "m3", "gpt-light", 50, 200, 0, now.Add(-30*time.Minute))
	seedUsage(t, db, "m4", "gpt-ancient", 99999, 200, 0, now.Add(-48*time.Hour))

	since := now.Add(-time.Hour)
	rows, err := db.Usage.TopModels(&since, nil, 8)
	if err != nil {
		t.Fatalf("top models: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("top models = %d rows, want 2 (the 48h-old row must be excluded)", len(rows))
	}
	if rows[0].Model != "gpt-heavy" || rows[0].Requests != 2 || rows[0].Tokens != 1000 || rows[0].Failed != 1 {
		t.Fatalf("top row = %+v", rows[0])
	}
	if rows[1].Model != "gpt-light" {
		t.Fatalf("second row = %+v", rows[1])
	}
}

func TestProxyLogFilterTimeRange(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().UTC().Truncate(time.Minute)
	insert := func(requestID string, latency int, at time.Time) int64 {
		id, err := db.ProxyLog.Insert(&domain.ProxyLog{
			RequestID: requestID,
			ChannelID: 1,
			Model:     "gpt-a",
			Status:    200,
			LatencyMs: latency,
			Attempt:   1,
		})
		if err != nil {
			t.Fatalf("insert log: %v", err)
		}
		backdate(t, db, "proxy_logs", id, at)
		return id
	}
	insert("inside", 120, now.Add(-20*time.Minute))
	insert("outside", 9000, now.Add(-5*time.Hour))

	since := now.Add(-time.Hour)
	until := now
	rows, err := db.ProxyLog.ListFilter(store.ProxyLogFilter{Since: &since, Until: &until, Limit: 50})
	if err != nil {
		t.Fatalf("list filter: %v", err)
	}
	if len(rows) != 1 || rows[0].RequestID != "inside" {
		t.Fatalf("range filter returned %d rows (%+v)", len(rows), rows)
	}

	hist, err := db.ProxyLog.LatencyHistogram(1000, &since, &until)
	if err != nil {
		t.Fatalf("histogram: %v", err)
	}
	if hist.Matched != 1 || hist.Total != 1 {
		t.Fatalf("windowed histogram matched=%d total=%d, want 1/1", hist.Matched, hist.Total)
	}
	if hist.P95Ms != 120 {
		t.Fatalf("windowed p95 = %d, want 120", hist.P95Ms)
	}

	all, err := db.ProxyLog.LatencyHistogram(1000, nil, nil)
	if err != nil {
		t.Fatalf("unbounded histogram: %v", err)
	}
	if all.Matched != 2 || all.SlowCount != 1 {
		t.Fatalf("unbounded histogram matched=%d slow=%d, want 2/1", all.Matched, all.SlowCount)
	}
	if all.SampleSize != 1000 {
		t.Fatalf("sample size = %d, want 1000", all.SampleSize)
	}
}

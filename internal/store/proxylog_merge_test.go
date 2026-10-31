package store_test

import (
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

// recordAttempt walks a request through three channels; every hop inserts its
// own row and folds its predecessors into itself, which is exactly what the
// proxy's recordAttempt does.
func recordAttempt(t *testing.T, db *store.DB, row *domain.ProxyLog) {
	t.Helper()
	id, err := db.ProxyLog.Insert(row)
	if err != nil {
		t.Fatalf("insert attempt: %v", err)
	}
	row.ID = id
	if err := db.ProxyLog.MarkSuperseded(row.RequestID, id); err != nil {
		t.Fatalf("mark superseded: %v", err)
	}
}

// TestProxyLogMergeCollapsesFailoverChain is the contract the console depends
// on: one request that failed over twice shows up as ONE row, and that row
// carries the whole walk in order — which channel was tried, what it answered,
// and which hop the client's answer finally came from.
func TestProxyLogMergeCollapsesFailoverChain(t *testing.T) {
	db := openTestDB(t)
	requestID := "req-failover-001"
	recordAttempt(t, db, &domain.ProxyLog{
		RequestID: requestID, ChannelID: 11, Model: "gpt-4o", Status: 429,
		ErrorBrief: "upstream_429", ErrorDetail: `{"error":{"code":"rate_limited"}}`,
		LatencyMs: 120,
	})
	recordAttempt(t, db, &domain.ProxyLog{
		RequestID: requestID, ChannelID: 12, Model: "gpt-4o", UpstreamModel: "gpt-4o-2024",
		Status: 502, ErrorBrief: "upstream_5xx", ErrorDetail: "bad gateway from origin",
		LatencyMs: 300,
	})
	recordAttempt(t, db, &domain.ProxyLog{
		RequestID: requestID, ChannelID: 13, Model: "gpt-4o", Status: 200, LatencyMs: 900,
		TotalTokens: 42,
	})

	rows, err := db.ProxyLog.ListFilter(store.ProxyLogFilter{Limit: 50})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("list rows = %d, want 1: one request must read as one row", len(rows))
	}
	row := rows[0]
	if row.ChannelID != 13 || row.Status != 200 {
		t.Fatalf("row = channel %d status %d, want the final attempt (13/200)", row.ChannelID, row.Status)
	}
	if len(row.Attempts) != 3 {
		t.Fatalf("chain hops = %d, want 3", len(row.Attempts))
	}
	if row.AttemptsTruncated {
		t.Fatal("a three-hop chain must not be flagged truncated")
	}
	wantStatuses := []int{429, 502, 200}
	wantChannels := []int64{11, 12, 13}
	for i, hop := range row.Attempts {
		if hop.Status != wantStatuses[i] || hop.ChannelID != wantChannels[i] {
			t.Fatalf("hop %d = channel %d status %d, want channel %d status %d",
				i, hop.ChannelID, hop.Status, wantChannels[i], wantStatuses[i])
		}
		if hop.Final != (i == 2) {
			t.Fatalf("hop %d final = %v, want %v", i, hop.Final, i == 2)
		}
	}
	if row.Attempts[1].UpstreamModel != "gpt-4o-2024" {
		t.Fatalf("hop 1 upstream_model = %q, want the mapped name", row.Attempts[1].UpstreamModel)
	}
	if row.Attempts[0].ErrorDetail != `{"error":{"code":"rate_limited"}}` {
		t.Fatalf("hop 0 error_detail = %q, want the upstream's own text", row.Attempts[0].ErrorDetail)
	}

	// The dashboard numbers must agree with the table: one request, one outcome.
	total, failed := db.ProxyLog.FailRate(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	if total != 1 || failed != 0 {
		t.Fatalf("fail rate = %d/%d, want 1/0 — counting attempts would double-count a retried request", failed, total)
	}
	if hist, err := db.ProxyLog.LatencyHistogram(100, nil, nil); err != nil {
		t.Fatalf("histogram: %v", err)
	} else if hist.Total != 1 {
		t.Fatalf("histogram total = %d, want 1 (the client waited once)", hist.Total)
	}

	// A request with no failover still gets a one-hop chain, so the console
	// never needs a "no failover happened" special case.
	recordAttempt(t, db, &domain.ProxyLog{
		RequestID: "req-single-002", ChannelID: 21, Model: "m", Status: 200,
	})
	rows, err = db.ProxyLog.ListFilter(store.ProxyLogFilter{Limit: 50})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("list rows = %d, want 2", len(rows))
	}
	for _, row := range rows {
		if row.RequestID != "req-single-002" {
			continue
		}
		if len(row.Attempts) != 1 || !row.Attempts[0].Final {
			t.Fatalf("row %s: chain = %+v, want exactly one final hop", row.RequestID, row.Attempts)
		}
	}
}

// TestProxyLogSearchSpansTheChain is why the search no longer joins on the
// surviving row: the error an operator greps for is usually the one a hop
// returned BEFORE the request was retried somewhere else, and that hop has been
// merged away by then.
func TestProxyLogSearchSpansTheChain(t *testing.T) {
	db := openTestDB(t)
	requestID := "req-search-001"
	recordAttempt(t, db, &domain.ProxyLog{
		RequestID: requestID, ChannelID: 11, Model: "gpt-4o", Status: 502,
		ErrorDetail: "connection reset by peer while streaming",
	})
	recordAttempt(t, db, &domain.ProxyLog{
		RequestID: requestID, ChannelID: 12, Model: "gpt-4o", Status: 200,
	})

	for _, q := range []string{"reset by peer", "connection reset"} {
		rows, err := db.ProxyLog.ListFilter(store.ProxyLogFilter{Query: q, Limit: 50})
		if err != nil {
			t.Fatalf("search %q: %v", q, err)
		}
		if len(rows) != 1 || rows[0].RequestID != requestID {
			t.Fatalf("search %q: rows = %v, want the request found via its failed hop", q, rows)
		}
		if len(rows[0].Attempts) != 2 {
			t.Fatalf("search %q: chain = %d hops, want 2", q, len(rows[0].Attempts))
		}
	}
}

// TestProxyLogChainCapKeepsBothEnds pins the backstop: a pathological request
// cannot hand back an unbounded array. Where it did, a single log record grew
// into hundreds of megabytes in the wild. Both ends survive the cap because
// "where did it start" and "what was finally returned" are the two answers an
// operator needs.
func TestProxyLogChainCapKeepsBothEnds(t *testing.T) {
	db := openTestDB(t)
	const hops = 45
	requestID := "req-cap-001"
	for i := 0; i < hops; i++ {
		recordAttempt(t, db, &domain.ProxyLog{
			RequestID: requestID, ChannelID: int64(100 + i), Model: "m",
			Status: 502, LatencyMs: i + 1,
		})
	}
	rows, err := db.ProxyLog.ListFilter(store.ProxyLogFilter{Limit: 50})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("list rows = %d, want 1", len(rows))
	}
	row := rows[0]
	if !row.AttemptsTruncated {
		t.Fatal("a 45-hop chain must be flagged truncated")
	}
	if len(row.Attempts) != 20 {
		t.Fatalf("chain hops = %d, want the 20-hop cap", len(row.Attempts))
	}
	if row.Attempts[0].ChannelID != 100 {
		t.Fatalf("first kept hop = channel %d, want where the request started", row.Attempts[0].ChannelID)
	}
	if last := row.Attempts[len(row.Attempts)-1]; last.ChannelID != 100+hops-1 || !last.Final {
		t.Fatalf("last kept hop = channel %d final=%v, want the final answer", last.ChannelID, last.Final)
	}
}

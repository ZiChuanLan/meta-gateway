package httpapi

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
)

// billingRuleEvidence is a rule snapshot shaped like the relay writes it.
const billingRuleEvidence = `{"owner":"model:gpt-test","revision":7,"source":"manual","rule_state":"explicit","rates":{"input_per_1k":1.5,"output_per_1k":6,"cache_per_1k":0.3,"cache_write_per_1k":1.5,"per_request":0.02,"multiplier":1,"timezone":"UTC"},"ratio":1.25,"evaluated_at":"2026-01-02T03:04:05Z","usage_source":"upstream","ordinary_input_tokens":900,"cache_read_tokens":100,"cache_write_tokens":0}`

// billingEntries pulls the `billing` annotation off a decoded log row.
func billingEntries(t *testing.T, row map[string]any) []map[string]any {
	t.Helper()
	raw, ok := row["billing"]
	if !ok {
		t.Fatalf("row has no billing annotation: %#v", row)
	}
	list, ok := raw.([]any)
	if !ok {
		t.Fatalf("billing is not an array: %#v", raw)
	}
	out := make([]map[string]any, 0, len(list))
	for _, entry := range list {
		object, ok := entry.(map[string]any)
		if !ok {
			t.Fatalf("billing entry is not an object: %#v", entry)
		}
		out = append(out, object)
	}
	return out
}

// An admin log row must explain its own amount: the rule snapshot rides along
// with the cost, one entry per metered attempt. A request that failed over and
// billed twice shows both decisions; a row whose stored snapshot is unreadable
// gets null rather than a blob to print; a row with no usage at all gets
// nothing at all.
func TestAdminProxyLogsAnnotateBillingExplanation(t *testing.T) {
	e := newTeamTestEnv(t)
	const leak = "leak-this-snapshot-must-never-be-returned"
	usage := []domain.UsageRecord{
		{RequestID: "req-bill", ChannelID: 1, Model: "gpt-test", Path: "chat/completions", Status: 200, TotalTokens: 1000, Cost: 0.5, BillingStatus: "settled", BillingEvidence: billingRuleEvidence},
		{RequestID: "req-bill", ChannelID: 1, Model: "gpt-test", Path: "chat/completions", Status: 200, TotalTokens: 1000, Cost: 0.25, BillingStatus: "estimated", BillingEvidence: billingRuleEvidence},
		{RequestID: "req-legacy", ChannelID: 1, Model: "gpt-test", Path: "chat/completions", Status: 200, TotalTokens: 1000, Cost: 0.1},
		{RequestID: "req-bad", ChannelID: 1, Model: "gpt-test", Path: "chat/completions", Status: 200, TotalTokens: 1000, Cost: 0.1, BillingStatus: "settled", BillingEvidence: leak},
	}
	for i := range usage {
		if _, err := e.db.Usage.Insert(&usage[i]); err != nil {
			t.Fatal(err)
		}
	}
	attempt := map[string]int{}
	for _, requestID := range []string{"req-bill", "req-bill", "req-legacy", "req-bad", "req-nousage"} {
		attempt[requestID]++
		if _, err := e.db.ProxyLog.Insert(&domain.ProxyLog{RequestID: requestID, ChannelID: 1, Model: "gpt-test", Status: 200, Attempt: attempt[requestID]}); err != nil {
			t.Fatal(err)
		}
	}

	raw := e.admin("GET", "/admin/proxy-logs?limit=50", nil, 200)
	if bytes.Contains(raw, []byte(leak)) {
		t.Fatalf("unreadable evidence leaked into the response: %s", raw)
	}
	var logs []map[string]any
	if err := json.Unmarshal(raw, &logs); err != nil {
		t.Fatalf("decode logs: %v (%s)", err, raw)
	}
	rowsByRequest := map[string][]map[string]any{}
	for _, row := range logs {
		id, _ := row["request_id"].(string)
		rowsByRequest[id] = append(rowsByRequest[id], row)
	}

	// Both attempts of the failed-over request carry the request-level
	// explanation, in attempt order.
	billRows := rowsByRequest["req-bill"]
	if len(billRows) != 2 {
		t.Fatalf("req-bill rows = %d, want 2", len(billRows))
	}
	for _, row := range billRows {
		entries := billingEntries(t, row)
		if len(entries) != 2 {
			t.Fatalf("req-bill billing = %#v, want both attempts", entries)
		}
		if entries[0]["status"] != "settled" || entries[1]["status"] != "estimated" {
			t.Fatalf("req-bill statuses = %v/%v", entries[0]["status"], entries[1]["status"])
		}
		evidence, ok := entries[0]["evidence"].(map[string]any)
		if !ok || evidence["owner"] != "model:gpt-test" || evidence["revision"] != float64(7) {
			t.Fatalf("req-bill evidence = %#v", entries[0]["evidence"])
		}
	}

	legacy := rowsByRequest["req-legacy"]
	if len(legacy) != 1 {
		t.Fatalf("req-legacy rows = %#v", legacy)
	}
	if entries := billingEntries(t, legacy[0]); len(entries) != 1 || entries[0]["status"] != "legacy" || entries[0]["evidence"] != nil {
		t.Fatalf("req-legacy explanation = %#v", entries)
	}

	bad := rowsByRequest["req-bad"]
	if len(bad) != 1 {
		t.Fatalf("req-bad rows = %#v", bad)
	}
	if entries := billingEntries(t, bad[0]); len(entries) != 1 || entries[0]["evidence"] != nil {
		t.Fatalf("req-bad must drop the unreadable snapshot: %#v", entries)
	}

	noUsage := rowsByRequest["req-nousage"]
	if len(noUsage) != 1 {
		t.Fatalf("req-nousage rows = %#v", noUsage)
	}
	if _, ok := noUsage[0]["billing"]; ok {
		t.Fatalf("a row with no usage must carry no billing key: %#v", noUsage[0])
	}
}

// The member app's request list answers "what did I spend": it is a different
// query and must not start leaking the admin-only rule snapshot (the rule owner
// and revision are operator-facing). The admin list over the same rows is where
// the explanation lives.
func TestMemberRequestsCarryNoBillingExplanation(t *testing.T) {
	e := newTeamTestEnv(t)
	e.enable()
	alice := e.member("alice", "member")

	const owner = "member-owner-must-not-leak"
	if _, err := e.db.Exec(
		`INSERT INTO team_requests(request_id,user_id,key_id,path,status,latency_ms) VALUES('req-m',?,0,'/v1/chat/completions',200,120)`,
		alice.userID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.ProxyLog.Insert(&domain.ProxyLog{RequestID: "req-m", ChannelID: 1, Model: "gpt-test", Status: 200, Attempt: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Usage.Insert(&domain.UsageRecord{
		RequestID: "req-m", UserID: alice.userID, ChannelID: 1, Model: "gpt-test",
		Path: "chat/completions", Status: 200, TotalTokens: 5, Cost: 0.02,
		BillingStatus: "settled", BillingEvidence: `{"owner":"` + owner + `","revision":1}`,
	}); err != nil {
		t.Fatal(err)
	}

	raw := alice.request("GET", "/me/requests", nil, 200)
	if bytes.Contains(raw, []byte(owner)) || bytes.Contains(raw, []byte("billing")) {
		t.Fatalf("member request list leaked the billing snapshot: %s", raw)
	}
	if !bytes.Contains(e.admin("GET", "/admin/proxy-logs?limit=10", nil, 200), []byte(owner)) {
		t.Fatal("admin logs did not annotate the same request")
	}
}

package store_test

import (
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

// Every quota in the product used to count tokens. A customer who buys "100
// dollars of usage" needs the same ledger in money, and the two budgets are
// enforced together: whichever runs out first refuses the request.
func TestMoneyQuotasAccrueOnAllThreeLevels(t *testing.T) {
	db := openTestDB(t)
	keyID, err := db.DownstreamKey.Create(&domain.DownstreamKey{
		Name: "budgeted", Enabled: true, GroupName: "paid",
		QuotaTotalCost: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Group.Upsert("paid", 0, 50, 0, 0); err != nil {
		t.Fatal(err)
	}
	res, err := db.Exec(`INSERT INTO team_users(username,name,password_hash,role,policy_id,quota_total_cost) VALUES('u','U','x','member',1,25)`)
	if err != nil {
		t.Fatal(err)
	}
	userID, _ := res.LastInsertId()

	record := &domain.UsageRecord{
		RequestID: "cost-1", DownstreamKeyID: keyID, Model: "m", Path: "chat/completions",
		TotalTokens: 1000, Cost: 0.25, Status: 200, GroupName: "paid", UserID: userID,
	}
	if err := db.RecordRelayUsage(record, keyID); err != nil {
		t.Fatal(err)
	}

	key, err := db.DownstreamKey.GetByID(keyID)
	if err != nil {
		t.Fatal(err)
	}
	if key.QuotaUsedCost != 0.25 {
		t.Fatalf("key spend = %v, want 0.25", key.QuotaUsedCost)
	}
	group, err := db.Group.Get("paid")
	if err != nil {
		t.Fatal(err)
	}
	if group.QuotaUsedCost != 0.25 {
		t.Fatalf("group spend = %v, want 0.25", group.QuotaUsedCost)
	}
	var userSpend float64
	if err := db.QueryRow(`SELECT quota_used_cost FROM team_users WHERE id=?`, userID).Scan(&userSpend); err != nil {
		t.Fatal(err)
	}
	if userSpend != 0.25 {
		t.Fatalf("account spend = %v, want 0.25", userSpend)
	}

	// Both units count, and either one being exhausted is enough. The key here
	// has no token quota at all, so only money can stop it.
	if store.QuotaExceeded(key) {
		t.Fatal("a key well under its spend budget reports exhausted")
	}
	key.QuotaUsedCost = key.QuotaTotalCost
	if !store.QuotaExceeded(key) {
		t.Fatal("a key that spent its whole budget is not exhausted")
	}
	// The token budget still refuses on its own, even with money to spare.
	key.QuotaTotalCost, key.QuotaUsedCost = 0, 0
	key.QuotaTotalTokens, key.QuotaUsedTokens = 100, 100
	if !store.QuotaExceeded(key) {
		t.Fatal("a key that spent its whole token budget is not exhausted")
	}

	// A pure call-priced request carries no tokens: the spend still books, or
	// a customer could run up an unbounded bill on a token-only ledger.
	flat := &domain.UsageRecord{
		RequestID: "cost-2", DownstreamKeyID: keyID, Model: "m", Path: "chat/completions",
		TotalTokens: 0, Cost: 1.5, Status: 200, GroupName: "paid", UserID: userID,
	}
	if err := db.RecordRelayUsage(flat, keyID); err != nil {
		t.Fatal(err)
	}
	key, _ = db.DownstreamKey.GetByID(keyID)
	if key.QuotaUsedCost != 1.75 {
		t.Fatalf("token-less request did not book its cost: %v", key.QuotaUsedCost)
	}
}

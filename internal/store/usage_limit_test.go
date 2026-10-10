package store_test

import (
	"fmt"
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
)

// spendThrough records one usage row for a channel, the way the relay does.
func spendThrough(t *testing.T, db interface {
	RecordRelayUsage(*domain.UsageRecord, int64) error
}, channelID int64, label string, cost float64, tokens int64) {
	t.Helper()
	if err := db.RecordRelayUsage(&domain.UsageRecord{
		RequestID: "req-" + label, ChannelID: channelID, Model: "gpt-test",
		Path: "chat/completions", PromptTokens: int(tokens), TotalTokens: int(tokens),
		Status: 200, Cost: cost,
	}, 0); err != nil {
		t.Fatal(err)
	}
}

// A channel budget is the operator's own decision coming due: the request that
// crosses it parks the channel, and the row says which limit it was.
func TestChannelUsageLimitParksTheChannel(t *testing.T) {
	db := openTestDB(t)
	channelID, err := db.Channel.Create(&domain.Channel{
		Name: "budget", Status: domain.StatusEnabled, UsageLimitCost: 0.01,
	})
	if err != nil {
		t.Fatal(err)
	}

	spendThrough(t, db, channelID, "under", 0.004, 100)
	channel, err := db.Channel.GetByID(channelID)
	if err != nil {
		t.Fatal(err)
	}
	if channel.Status != domain.StatusEnabled || channel.UsageLimitHit != "" {
		t.Fatalf("after 0.004 the channel = %q/%q, want it still enabled", channel.Status, channel.UsageLimitHit)
	}
	if channel.UsageUsedCost != 0.004 || channel.UsageUsedTokens != 100 {
		t.Fatalf("counters = %v/%d, want the ledger's 0.004/100", channel.UsageUsedCost, channel.UsageUsedTokens)
	}

	// Crossing it is the moment it parks: 0.004 + 0.008 >= 0.01.
	spendThrough(t, db, channelID, "over", 0.008, 50)
	channel, err = db.Channel.GetByID(channelID)
	if err != nil {
		t.Fatal(err)
	}
	if channel.Status != domain.StatusAutoDisabled {
		t.Fatalf("status = %q, want auto_disabled once the limit is reached", channel.Status)
	}
	if channel.UsageLimitHit != "cost" {
		t.Fatalf("hit = %q, want cost", channel.UsageLimitHit)
	}
	if channel.UsageLimitHitAt == "" {
		t.Fatal("hit_at is empty, want the moment it tripped")
	}

	// The counters are the ledger, not a second opinion about it.
	var ledgerCost float64
	var ledgerTokens int64
	if err := db.QueryRow(`SELECT COALESCE(SUM(cost),0), COALESCE(SUM(total_tokens),0) FROM usage_records WHERE channel_id = ?`, channelID).
		Scan(&ledgerCost, &ledgerTokens); err != nil {
		t.Fatal(err)
	}
	if ledgerCost != channel.UsageUsedCost || ledgerTokens != channel.UsageUsedTokens {
		t.Fatalf("counters %v/%d != ledger %v/%d", channel.UsageUsedCost, channel.UsageUsedTokens, ledgerCost, ledgerTokens)
	}

	// A token limit trips on its own, in the other unit.
	if _, err := db.Channel.Create(&domain.Channel{Name: "tokens", Status: domain.StatusEnabled, UsageLimitTokens: 10}); err != nil {
		t.Fatal(err)
	}
	tokenChannel, err := db.Channel.GetByID(2)
	if err != nil {
		t.Fatal(err)
	}
	spendThrough(t, db, tokenChannel.ID, "tokens", 0, 25)
	tokenChannel, err = db.Channel.GetByID(tokenChannel.ID)
	if err != nil {
		t.Fatal(err)
	}
	if tokenChannel.Status != domain.StatusAutoDisabled || tokenChannel.UsageLimitHit != "tokens" {
		t.Fatalf("token-limited channel = %q/%q, want auto_disabled/tokens", tokenChannel.Status, tokenChannel.UsageLimitHit)
	}
}

// A channel with no limit, or one still under it, is left alone: this feature
// must not become a second auto-disable nobody asked for.
func TestChannelWithoutALimitIsNeverParked(t *testing.T) {
	db := openTestDB(t)
	channelID, err := db.Channel.Create(&domain.Channel{Name: "unlimited", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	spendThrough(t, db, channelID, "huge", 999, 1_000_000)
	channel, err := db.Channel.GetByID(channelID)
	if err != nil {
		t.Fatal(err)
	}
	if channel.Status != domain.StatusEnabled || channel.UsageLimitHit != "" {
		t.Fatalf("channel = %q/%q, want it enabled (no limit set)", channel.Status, channel.UsageLimitHit)
	}
}

// Raising a limit releases a channel the old limit parked — otherwise the
// operator's fix (the limit was too low) would leave it parked forever.
func TestSavingAHigherLimitReleasesTheParkedChannel(t *testing.T) {
	db := openTestDB(t)
	channelID, err := db.Channel.Create(&domain.Channel{Name: "raised", Status: domain.StatusEnabled, UsageLimitCost: 0.01})
	if err != nil {
		t.Fatal(err)
	}
	spendThrough(t, db, channelID, "trip", 0.02, 10)
	parked, err := db.Channel.GetByID(channelID)
	if err != nil {
		t.Fatal(err)
	}
	if parked.Status != domain.StatusAutoDisabled {
		t.Fatalf("status = %q, want the limit to have parked it", parked.Status)
	}

	parked.UsageLimitCost = 1
	if err := db.Channel.Update(parked); err != nil {
		t.Fatal(err)
	}
	released, err := db.Channel.GetByID(channelID)
	if err != nil {
		t.Fatal(err)
	}
	if released.Status != domain.StatusEnabled {
		t.Fatalf("status = %q, want enabled after raising the limit", released.Status)
	}
	if released.UsageLimitHit != "" || released.UsageLimitHitAt != "" {
		t.Fatalf("hit state = %q/%q, want it cleared", released.UsageLimitHit, released.UsageLimitHitAt)
	}
	// Re-derived from the ledger, so the console's read-out is the real spend
	// (0.02) rather than whatever the counter held.
	if fmt.Sprintf("%.4f", released.UsageUsedCost) != "0.0200" {
		t.Fatalf("used cost = %v, want the ledger's 0.02", released.UsageUsedCost)
	}

	// Lowering it again below the spend parks it on the next save, because the
	// limit is re-measured against the ledger.
	released.UsageLimitCost = 0.005
	if err := db.Channel.Update(released); err != nil {
		t.Fatal(err)
	}
	reloaded, err := db.Channel.GetByID(channelID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.UsageUsedCost != 0.02 {
		t.Fatalf("used cost after lowering = %v, want the ledger's 0.02", reloaded.UsageUsedCost)
	}
	// The next request that lands trips it again.
	spendThrough(t, db, channelID, "trip-again", 0.001, 1)
	reloaded, err = db.Channel.GetByID(channelID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Status != domain.StatusAutoDisabled || reloaded.UsageLimitHit != "cost" {
		t.Fatalf("after lowering the limit: %q/%q, want auto_disabled/cost", reloaded.Status, reloaded.UsageLimitHit)
	}
}

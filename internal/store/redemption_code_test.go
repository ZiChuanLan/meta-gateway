package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/store"
)

func TestRedemptionCodeLifecycle(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().UTC()

	// Mint codes.
	codes, err := db.CreateRedemptionCodes(3, 100_000, 1, nil)
	if err != nil || len(codes) != 3 {
		t.Fatalf("mint: %d codes err=%v", len(codes), err)
	}
	if codes[0].Code == "" || codes[0].QuotaTokens != 100_000 {
		t.Fatalf("code = %+v", codes[0])
	}
	if codes[0].Code == codes[1].Code {
		t.Fatal("duplicate codes")
	}

	// List shows 3 unredeemed.
	all, _ := db.ListRedemptionCodes(false)
	if len(all) != 3 {
		t.Fatalf("list = %d", len(all))
	}

	// Redeem one.
	quota, err := db.RedeemCode(codes[0].Code, 42, now)
	if err != nil || quota != 100_000 {
		t.Fatalf("redeem: quota=%d err=%v", quota, err)
	}
	// Second redeem of the same code must fail.
	if _, err := db.RedeemCode(codes[0].Code, 43, now); !errors.Is(err, store.ErrCodeInvalid) {
		t.Fatalf("double redeem err = %v", err)
	}
	// Unknown code fails.
	if _, err := db.RedeemCode("MG-NOPE1234", 42, now); !errors.Is(err, store.ErrCodeInvalid) {
		t.Fatalf("unknown code err = %v", err)
	}

	// Redeemed filter.
	unredeemed, _ := db.ListRedemptionCodes(true)
	if len(unredeemed) != 2 {
		t.Fatalf("unredeemed = %d, want 2", len(unredeemed))
	}

	// Void an unredeemed code.
	unredeemed[0].ID = 0 // ensure we re-read the id
	_ = unredeemed
	list, _ := db.ListRedemptionCodes(false)
	var target store.RedemptionCode
	for _, c := range list {
		if c.RedeemedByKeyID == 0 && c.ID != codes[0].ID {
			target = c
			break
		}
	}
	if target.ID == 0 {
		t.Fatal("no unredeemed target")
	}
	if err := db.DeleteRedemptionCode(target.ID); err != nil {
		t.Fatal(err)
	}
	// Voiding a redeemed code fails.
	if err := db.DeleteRedemptionCode(codes[0].ID); !errors.Is(err, store.ErrCodeInvalid) {
		t.Fatalf("void redeemed err = %v", err)
	}
}

func TestRedemptionCodeExpiry(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().UTC()
	past := now.Add(-time.Hour)

	codes, err := db.CreateRedemptionCodes(1, 50_000, 1, &past)
	if err != nil || len(codes) != 1 {
		t.Fatalf("mint expired: %v", err)
	}
	if _, err := db.RedeemCode(codes[0].Code, 7, now); !errors.Is(err, store.ErrCodeInvalid) {
		t.Fatalf("expired redeem err = %v", err)
	}
}

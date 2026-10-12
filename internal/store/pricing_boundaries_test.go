package store_test

import (
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

func rate(v float64) *float64 { return &v }
func TestBillingRuleStatesPreserveLegacy(t *testing.T) {
	db, _, member, _ := adoptFixture(t)
	key := strconv.FormatInt(member, 10)
	if err := db.ModelMetadata.Upsert(&domain.ModelMetadata{ModelName: "glm-5.2", PricePromptPer1k: 9}); err != nil {
		t.Fatal(err)
	}
	before, priced, err := db.ResolveBillingLayer("glm-5.2", "", member)
	if err != nil || !priced || before.Prompt != .001 || before.State != "legacy" {
		t.Fatalf("legacy=%+v err=%v", before, err)
	}
	free := domain.BillingRule{State: "free", Timezone: "UTC", Source: "manual"}
	saved, err := db.SaveBillingRule("member", key, free, 0)
	if err != nil {
		t.Fatal(err)
	}
	layer, priced, err := db.ResolveBillingLayer("glm-5.2", "", member)
	if err != nil || !priced || layer.State != "free" || layer.Prompt != 0 {
		t.Fatalf("free fallback leaked %+v %v", layer, err)
	}
	if _, err = db.SaveBillingRule("member", key, free, 0); !errors.Is(err, store.ErrPriceConflict) {
		t.Fatalf("stale write=%v", err)
	}
	saved.State = "inherit"
	if _, err = db.SaveBillingRule("member", key, *saved, saved.Revision); err != nil {
		t.Fatal(err)
	}
	layer, priced, err = db.ResolveBillingLayer("glm-5.2", "", member)
	if err != nil || !priced || layer.Prompt != 9 {
		t.Fatalf("inherit=%+v %v", layer, err)
	}
	prompt, _, _, _ := prices(t, db, member)
	if prompt != .001 {
		t.Fatal("legacy data changed")
	}
}
func TestQuoteCopyFreeCacheAndVersion(t *testing.T) {
	db, member, _, _ := adoptFixture(t)
	key := strconv.FormatInt(member, 10)
	q := domain.PriceQuoteSnapshot{ScopeKey: "reference:glm-5.2", Model: "glm-5.2", Purpose: "reference", Source: "catalog", Unit: domain.QuoteUnit{Kind: "iso", Code: "USD"}, Status: "calculable", Values: domain.QuoteValues{Mode: "token", Input: rate(2), Output: rate(0), CacheRead: rate(0), CacheWrite: rate(2.5)}, ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)}
	id, err := db.SavePriceQuote(q)
	if err != nil {
		t.Fatal(err)
	}
	r, err := db.CopyQuoteRule("member", key, id, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	layer, _, err := db.ResolveBillingLayer(q.Model, "", member)
	if err != nil {
		t.Fatal(err)
	}
	rates := layer.Rates(1000, time.Now())
	if rates.CacheRead != 0 || rates.Output != 0 || rates.CacheWrite != .0025 {
		t.Fatalf("free fields lost %+v", rates)
	}
	q.Values.Input = rate(9)
	newID, err := db.SavePriceQuote(q)
	if err != nil {
		t.Fatal(err)
	}
	layer, _, err = db.ResolveBillingLayer(q.Model, "", member)
	if err != nil || layer.Prompt != .002 {
		t.Fatal("quote refresh repriced rule")
	}
	if _, err = db.CopyQuoteRule("member", key, id, r.Revision, nil); !errors.Is(err, store.ErrPriceConflict) {
		t.Fatalf("old quote copied: %v", err)
	}
	q.Unit = domain.QuoteUnit{Kind: "site_credit", Code: "site:1"}
	q.ScopeKey = "site:1:glm-5.2"
	id, err = db.SavePriceQuote(q)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.CopyQuoteRule("member", key, id, r.Revision, nil); err == nil {
		t.Fatal("native credit silently assumed USD")
	}
	if _, err = db.CopyQuoteRule("member", key, newID, r.Revision, nil); err != nil {
		t.Fatal(err)
	}
}
func TestBillingRuleConcurrentCAS(t *testing.T) {
	db, member, _, _ := adoptFixture(t)
	key := strconv.FormatInt(member, 10)
	r := domain.BillingRule{State: "explicit", Mode: "request", PerRequest: rate(.05), Timezone: "UTC", Source: "manual"}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := db.SaveBillingRule("member", key, r, 0); results <- err }()
	}
	wg.Wait()
	close(results)
	ok, conflicts := 0, 0
	for err := range results {
		if err == nil {
			ok++
		} else if errors.Is(err, store.ErrPriceConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if ok != 1 || conflicts != 1 {
		t.Fatalf("CAS successes=%d conflicts=%d", ok, conflicts)
	}
}

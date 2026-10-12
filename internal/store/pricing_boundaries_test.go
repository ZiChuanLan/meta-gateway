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
	rule, err := db.CopyQuoteRule("member", key, id, r.Revision, nil)
	if err != nil {
		t.Fatalf("reviewed quote must stay copyable after a refresh: %v", err)
	}
	stale := q
	stale.Values.Input = rate(1)
	stale.ExpiresAt = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	staleID, err := db.SavePriceQuote(stale)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.CopyQuoteRule("member", key, staleID, rule.Revision, nil); !errors.Is(err, store.ErrPriceConflict) {
		t.Fatalf("expired quote copied: %v", err)
	}
	q.Unit = domain.QuoteUnit{Kind: "site_credit", Code: "site:1"}
	q.ScopeKey = "site:1:glm-5.2"
	id, err = db.SavePriceQuote(q)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.CopyQuoteRule("member", key, id, rule.Revision, nil); err == nil {
		t.Fatal("native credit silently assumed USD")
	}
	if _, err = db.CopyQuoteRule("member", key, newID, rule.Revision, nil); err != nil {
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

func TestQuoteCopyRejectsAnotherAccountAtTheSameSite(t *testing.T) {
	db, err := store.OpenTest(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	site, err := db.Site.Create(&domain.Site{Name: "site", BaseURL: "https://site.example", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	account, err := db.Credential.Create(&domain.Credential{SiteID: site, Kind: "access_token", Status: domain.StatusEnabled, SecretEnc: []byte("encrypted-fixture")})
	if err != nil {
		t.Fatal(err)
	}
	other, err := db.Credential.Create(&domain.Credential{SiteID: site, Kind: "access_token", Status: domain.StatusEnabled, SecretEnc: []byte("encrypted-other")})
	if err != nil {
		t.Fatal(err)
	}
	channel, err := db.Channel.Create(&domain.Channel{Name: "channel", SiteID: &site, CredentialID: &account, Status: domain.StatusEnabled, GroupName: "default"})
	if err != nil {
		t.Fatal(err)
	}
	route, err := db.Route.Create(&domain.Route{ModelPattern: "m", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	member, err := db.RouteMember.Create(&domain.RouteMember{RouteID: route, ChannelID: channel, Enabled: true, Weight: 100})
	if err != nil {
		t.Fatal(err)
	}
	q := domain.PriceQuoteSnapshot{ScopeKey: "account:m", Model: "m", Purpose: "upstream", Source: "site_account", SiteID: site, AccountID: other, UpstreamGroup: "default", Unit: domain.QuoteUnit{Kind: "site_credit", Code: "credit:site/1"}, Status: "calculable", Values: domain.QuoteValues{Mode: "token", Input: rate(2), Output: rate(3)}}
	id, err := db.SavePriceQuote(q)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.CopyQuoteRule("member", strconv.FormatInt(member, 10), id, 0, rate(1)); err == nil {
		t.Fatal("another account's quote was accepted")
	}
	q.AccountID = account
	id, err = db.SavePriceQuote(q)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.CopyQuoteRule("member", strconv.FormatInt(member, 10), id, 0, rate(1)); err != nil {
		t.Fatal(err)
	}
}

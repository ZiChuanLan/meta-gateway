package store_test

import (
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

// adoptFixture wires one route with two members on separate channels, so the
// fill-only-empty rule can be checked against a priced and an unpriced member.
func adoptFixture(t *testing.T) (*store.DB, int64, int64, int64) {
	t.Helper()
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	siteID, err := db.Site.Create(&domain.Site{Name: "s", BaseURL: "https://a.example", Platform: "new-api", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatalf("site: %v", err)
	}
	channelA, err := db.Channel.Create(&domain.Channel{SiteID: &siteID, Name: "A", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatalf("channel: %v", err)
	}
	channelB, err := db.Channel.Create(&domain.Channel{SiteID: &siteID, Name: "B", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatalf("channel: %v", err)
	}
	routeID, err := db.Route.Create(&domain.Route{ModelPattern: "glm-5.2", Enabled: true})
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	unpriced, err := db.RouteMember.Create(&domain.RouteMember{RouteID: routeID, ChannelID: channelA, Enabled: true, Auto: true})
	if err != nil {
		t.Fatalf("member A: %v", err)
	}
	// The operator already typed a prompt price on this one; an observed quote
	// must leave it alone.
	priced, err := db.RouteMember.Create(&domain.RouteMember{RouteID: routeID, ChannelID: channelB, Enabled: true, Auto: true, PricePromptPer1k: 0.001})
	if err != nil {
		t.Fatalf("member B: %v", err)
	}
	return db, unpriced, priced, routeID
}

func prices(t *testing.T, db *store.DB, memberID int64) (prompt, completion, cache, perRequest float64) {
	t.Helper()
	prompt, completion, cache, perRequest, _, err := db.RouteMember.MemberPrices(memberID)
	if err != nil {
		t.Fatalf("member prices: %v", err)
	}
	return prompt, completion, cache, perRequest
}

// A token-mode quote lands in the per-1k columns. The unit conversion is the
// trap: published prices are USD per 1M tokens, the billing columns are USD per
// 1k, so 0.15 / 1M must become 0.00015 / 1k — and completions 100× the prompt
// price must NOT be lost on the way.
func TestAdoptObservedPricesFillsTokenColumns(t *testing.T) {
	db, unpriced, _, _ := adoptFixture(t)

	adopted, err := db.RouteMember.AdoptObservedPrices(unpriced, 0.00015, 0.005, 0.00003, 0)
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	if len(adopted) != 3 {
		t.Fatalf("adopted = %v, want prompt+completion+cache", adopted)
	}
	prompt, completion, cache, perRequest := prices(t, db, unpriced)
	if prompt != 0.00015 || completion != 0.005 || cache != 0.00003 || perRequest != 0 {
		t.Fatalf("prices = %v/%v/%v/%v", prompt, completion, cache, perRequest)
	}

	// A second adoption is a no-op: every field the quote could fill is filled.
	adopted, err = db.RouteMember.AdoptObservedPrices(unpriced, 9, 9, 9, 0)
	if err != nil {
		t.Fatalf("re-adopt: %v", err)
	}
	if len(adopted) != 0 {
		t.Fatalf("re-adopt = %v, want nothing written", adopted)
	}
	if prompt, _, _, _ = prices(t, db, unpriced); prompt != 0.00015 {
		t.Fatalf("a second adoption changed the prompt price to %v", prompt)
	}
}

// A member the operator priced by hand keeps its number: the fill-only-empty
// rule is what makes "adopt" safe to press without reading the fine print.
func TestAdoptObservedPricesNeverOverwritesAnOperatorPrice(t *testing.T) {
	db, _, priced, _ := adoptFixture(t)

	adopted, err := db.RouteMember.AdoptObservedPrices(priced, 0.00015, 0.005, 0.00003, 0)
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	if len(adopted) != 2 || adopted[0] != "price_completion_per_1k" || adopted[1] != "price_cache_per_1k" {
		t.Fatalf("adopted = %v, want completion+cache only", adopted)
	}
	prompt, completion, _, _ := prices(t, db, priced)
	if prompt != 0.001 {
		t.Fatalf("prompt price overwritten to %v", prompt)
	}
	if completion != 0.005 {
		t.Fatalf("completion not filled: %v", completion)
	}
}

// A per-call quote goes to price_per_request only. Mixing it into the per-1k
// columns would bill token usage with a per-call number.
func TestAdoptObservedPricesPerCallQuoteStaysPerCall(t *testing.T) {
	db, unpriced, _, _ := adoptFixture(t)

	if _, err := db.RouteMember.AdoptObservedPrices(unpriced, 0, 0, 0, 0.02); err != nil {
		t.Fatalf("adopt: %v", err)
	}
	prompt, _, _, perRequest := prices(t, db, unpriced)
	if prompt != 0 || perRequest != 0.02 {
		t.Fatalf("prices = prompt %v / per-request %v, want only the per-call field", prompt, perRequest)
	}

	// And once priced per call, a token quote must not sneak in.
	adopted, err := db.RouteMember.AdoptObservedPrices(unpriced, 0.00015, 0.005, 0, 0)
	if err != nil || len(adopted) != 0 {
		t.Fatalf("token quote after per-call = %v err %v, want nothing", adopted, err)
	}
}

// The token family fills together. A row with a prompt price and a zero
// completion price would bill every completion as free — the one outcome the
// price layers must never produce silently.
func TestAdoptObservedPricesFillsTheTokenFamilyTogether(t *testing.T) {
	db, _, priced, _ := adoptFixture(t)
	// Priced member has only a prompt price: adopting a full quote must fill
	// completion (and cache), never leave the family half-written.
	if _, err := db.RouteMember.AdoptObservedPrices(priced, 0.00015, 0.005, 0.00003, 0); err != nil {
		t.Fatalf("adopt: %v", err)
	}
	_, completion, cache, _ := prices(t, db, priced)
	if completion != 0.005 || cache != 0.00003 {
		t.Fatalf("family not filled: completion %v cache %v", completion, cache)
	}
}

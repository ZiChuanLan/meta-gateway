package proxy

import (
	"testing"

	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
	"github.com/lan/meta-gateway/internal/usage"
)

// Per-request pricing exists for upstreams that sell calls rather than tokens.
// It is a third price layer beside prompt/completion/cache, resolved by the
// same two-step fallback and multiplied by the same billing ratio, so a call
// priced per request needs no separate rule anywhere else.
func TestBillingPerRequestPrice(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	enc, _ := crypto.New("per-request-billing-key")
	service, err := buildPoolService(db, enc)
	if err != nil {
		t.Fatal(err)
	}
	keyID, err := db.DownstreamKey.Create(&domain.DownstreamKey{Name: "client", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	routeID, err := db.Route.Create(&domain.Route{ModelPattern: "call-priced", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	channelID, err := db.Channel.Create(&domain.Channel{Name: "c", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}

	// A member that ONLY sells calls: without it counting as a priced layer,
	// the lookup would fall through to the model metadata (unpriced) and bill
	// every request at zero.
	memberID, err := db.RouteMember.Create(&domain.RouteMember{
		RouteID: routeID, ChannelID: channelID, Weight: 100, Enabled: true,
		PricePerRequest: 0.05,
	})
	if err != nil {
		t.Fatal(err)
	}
	req := Request{Model: "call-priced", RouteID: routeID, MemberID: memberID, DownstreamKeyID: keyID}
	if cost := service.billingCost(req, usage.Tokens{}); cost != 0.05 {
		t.Fatalf("per-request cost = %v, want 0.05 even with no tokens", cost)
	}
	// Tokens do not change a call price.
	if cost := service.billingCost(req, usage.Tokens{PromptTokens: 100000, CompletionTokens: 100000}); cost != 0.05 {
		t.Fatalf("per-request cost with tokens = %v, want 0.05", cost)
	}

	// A member that prices BOTH: the flat fee is added to the token amount.
	mixed, err := db.RouteMember.Create(&domain.RouteMember{
		RouteID: routeID, ChannelID: channelID, GroupName: "mixed", Weight: 100, Enabled: true,
		PricePromptPer1k: 2, PriceCompletionPer1k: 4, PricePerRequest: 0.01,
	})
	if err != nil {
		t.Fatal(err)
	}
	mixedReq := Request{Model: "call-priced", RouteID: routeID, MemberID: mixed, DownstreamKeyID: keyID}
	cost := service.billingCost(mixedReq, usage.Tokens{PromptTokens: 1000, CompletionTokens: 1000})
	// 1000 prompt tokens at 2/1k = 2, 1000 completion tokens at 4/1k = 4, plus
	// the flat 0.01 call fee.
	if cost < 6.0099 || cost > 6.0101 {
		t.Fatalf("mixed cost = %v, want 6.01 (2 + 4 of token billing plus a 0.01 call fee)", cost)
	}

	// The model layer carries the fallback the same way: a model priced only
	// per request still bills, with no member price at all.
	if err := db.ModelMetadata.Upsert(&domain.ModelMetadata{ModelName: "model-priced", PricePerRequest: 0.2}); err != nil {
		t.Fatal(err)
	}
	modelReq := Request{Model: "model-priced", DownstreamKeyID: keyID}
	if cost := service.billingCost(modelReq, usage.Tokens{}); cost != 0.2 {
		t.Fatalf("model per-request cost = %v, want 0.2", cost)
	}

	// The billing ratio multiplies the call fee like any other amount.
	if err := db.ModelRatio.SetRatio("call-priced", 2); err != nil {
		t.Fatalf("ratio: %v", err)
	}
	if cost := service.billingCost(req, usage.Tokens{}); cost != 0.1 {
		t.Fatalf("ratio-scaled call cost = %v, want 0.1", cost)
	}
}

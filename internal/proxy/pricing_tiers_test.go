package proxy

import (
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
	"github.com/lan/meta-gateway/internal/usage"
)

// Context-length ladders and time-of-day windows reach the bill through the
// same path as every other price, so these tests drive billingCost itself
// rather than the parser: what matters is the amount the relay persists.
func TestBillingContextLadder(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	enc, _ := crypto.New("ladder-billing-key")
	service, err := buildPoolService(db, enc)
	if err != nil {
		t.Fatal(err)
	}
	keyID, err := db.DownstreamKey.Create(&domain.DownstreamKey{Name: "client", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	routeID, err := db.Route.Create(&domain.Route{ModelPattern: "tiered", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	channelID, err := db.Channel.Create(&domain.Channel{Name: "c", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}

	// Flat columns deliberately left at zero: the ladder alone must price the
	// layer, which is the case the old "any column non-zero" test got wrong.
	ladder, err := domain.EncodePriceTiers([]domain.PriceTier{
		{MaxPromptTokens: 1000, Prompt: 1, Completion: 2},
		{MaxPromptTokens: 0, Prompt: 10, Completion: 20},
	})
	if err != nil {
		t.Fatal(err)
	}
	memberID, err := db.RouteMember.Create(&domain.RouteMember{
		RouteID: routeID, ChannelID: channelID, Weight: 100, Enabled: true,
		PriceTiers: ladder,
	})
	if err != nil {
		t.Fatal(err)
	}
	req := Request{Model: "tiered", RouteID: routeID, MemberID: memberID, DownstreamKeyID: keyID}

	// Short request → the small rung: 0.5 * 1 + 0.5 * 2 = 1.5
	if cost := service.billingCost(req, usage.Tokens{PromptTokens: 500, CompletionTokens: 500}); cost < 1.4999 || cost > 1.5001 {
		t.Fatalf("short request cost = %v, want 1.5", cost)
	}
	// Long request → the open rung: 5 * 10 + 0.5 * 20 = 60
	if cost := service.billingCost(req, usage.Tokens{PromptTokens: 5000, CompletionTokens: 500}); cost < 59.99 || cost > 60.01 {
		t.Fatalf("long request cost = %v, want 60", cost)
	}
	// Cache-creation tokens count as input for rung selection and billing.
	// 1000 + 200 creation = 1200 → the open rung: 1.2 * 10 = 12
	if cost := service.billingCost(req, usage.Tokens{PromptTokens: 1000, CacheCreationTokens: 200}); cost < 11.99 || cost > 12.01 {
		t.Fatalf("cache-creation cost = %v, want 12", cost)
	}
	// The billing ratio still multiplies the rung price.
	if err := db.ModelRatio.SetRatio("tiered", 2); err != nil {
		t.Fatalf("ratio: %v", err)
	}
	if cost := service.billingCost(req, usage.Tokens{PromptTokens: 500, CompletionTokens: 500}); cost < 2.9999 || cost > 3.0001 {
		t.Fatalf("ratio-scaled tier cost = %v, want 3", cost)
	}
	if err := db.ModelRatio.SetRatio("tiered", -1); err != nil {
		t.Fatalf("ratio reset: %v", err)
	}
}

// A window that covers the current hour must scale the same request. The window
// is built around the clock on purpose: a fixed "22:00–06:00" would make this
// test pass or fail depending on when the suite runs.
func TestBillingTimeWindow(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	enc, _ := crypto.New("window-billing-key")
	service, err := buildPoolService(db, enc)
	if err != nil {
		t.Fatal(err)
	}
	keyID, err := db.DownstreamKey.Create(&domain.DownstreamKey{Name: "client", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	routeID, err := db.Route.Create(&domain.Route{ModelPattern: "peaky", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	channelID, err := db.Channel.Create(&domain.Channel{Name: "c", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}

	hour := time.Now().Hour()
	during, err := domain.EncodePriceWindows([]domain.PriceWindow{
		{FromHour: hour, ToHour: (hour + 1) % 24, Multiplier: 0.5},
	})
	if err != nil {
		t.Fatal(err)
	}
	duringMember, err := db.RouteMember.Create(&domain.RouteMember{
		RouteID: routeID, ChannelID: channelID, Weight: 100, Enabled: true,
		PricePromptPer1k: 1, PriceSchedule: during,
	})
	if err != nil {
		t.Fatal(err)
	}

	// 1000 prompt tokens at 1/1k = 1, halved by the window in force.
	req := Request{Model: "peaky", RouteID: routeID, MemberID: duringMember, DownstreamKeyID: keyID}
	if cost := service.billingCost(req, usage.Tokens{PromptTokens: 1000}); cost < 0.4999 || cost > 0.5001 {
		t.Fatalf("windowed cost = %v, want 0.5", cost)
	}

	// A window two hours away must not apply. Skipping two hours keeps it
	// disjoint from the one above whatever the clock says.
	away, err := domain.EncodePriceWindows([]domain.PriceWindow{
		{FromHour: (hour + 2) % 24, ToHour: (hour + 3) % 24, Multiplier: 0.1},
	})
	if err != nil {
		t.Fatal(err)
	}
	awayMember, err := db.RouteMember.Create(&domain.RouteMember{
		RouteID: routeID, ChannelID: channelID, GroupName: "later", Weight: 100, Enabled: true,
		PricePromptPer1k: 1, PriceSchedule: away,
	})
	if err != nil {
		t.Fatal(err)
	}
	awayReq := Request{Model: "peaky", RouteID: routeID, MemberID: awayMember, DownstreamKeyID: keyID}
	if cost := service.billingCost(awayReq, usage.Tokens{PromptTokens: 1000}); cost < 0.9999 || cost > 1.0001 {
		t.Fatalf("out-of-window cost = %v, want 1 (full price)", cost)
	}
}

// The model layer carries the same two features, so a gateway that prices by
// model rather than by channel behaves identically.
func TestBillingModelLayerLadder(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	enc, _ := crypto.New("model-ladder-key")
	service, err := buildPoolService(db, enc)
	if err != nil {
		t.Fatal(err)
	}
	keyID, err := db.DownstreamKey.Create(&domain.DownstreamKey{Name: "client", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	ladder, err := domain.EncodePriceTiers([]domain.PriceTier{{MaxPromptTokens: 100, Prompt: 1, Completion: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.ModelMetadata.Upsert(&domain.ModelMetadata{ModelName: "model-tiered", PriceTiers: ladder}); err != nil {
		t.Fatal(err)
	}
	req := Request{Model: "model-tiered", DownstreamKeyID: keyID}
	if cost := service.billingCost(req, usage.Tokens{PromptTokens: 100}); cost < 0.0999 || cost > 0.1001 {
		t.Fatalf("model ladder cost = %v, want 0.1", cost)
	}
}

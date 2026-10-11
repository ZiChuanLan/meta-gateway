package store

import (
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
)

// Billing already cascades member price -> model price. This pins the
// provenance that explains the first layer afterwards: who wrote it, and when.
func TestMemberPriceSourceRecordsItsWriter(t *testing.T) {
	db, err := OpenTest(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	routeID, err := db.Route.Create(&domain.Route{ModelPattern: "price-source-model", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	siteID, err := db.Site.Create(&domain.Site{Name: "pricing-site", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	credID, err := db.Credential.Create(&domain.Credential{SiteID: siteID, Kind: "api_key", SecretEnc: []byte("x"), Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	channelID, err := db.Channel.Create(&domain.Channel{Name: "pricing-channel", SiteID: &siteID, CredentialID: &credID,
		BaseURL: "https://pricing.invalid", TypeHint: "openai-compatible", Status: domain.StatusEnabled, Weight: 100})
	if err != nil {
		t.Fatal(err)
	}
	memberID, err := db.RouteMember.Create(&domain.RouteMember{RouteID: routeID, ChannelID: channelID, Enabled: true, Weight: 100})
	if err != nil {
		t.Fatal(err)
	}

	// A patch that carries prices is the operator speaking.
	manual := &domain.RouteMember{ID: memberID, Weight: 100, Enabled: true, PricePromptPer1k: 1.5, PriceCompletionPer1k: 3}
	if err := db.RouteMember.PatchConfiguration(manual, map[string]bool{
		"price_prompt_per_1k": true, "price_completion_per_1k": true,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := db.RouteMember.GetByID(memberID)
	if err != nil || got == nil {
		t.Fatalf("member read: %v %v", got, err)
	}
	if got.PriceSource != "manual" || got.PriceUpdatedAt == "" {
		t.Fatalf("manual patch provenance: source=%q at=%q", got.PriceSource, got.PriceUpdatedAt)
	}

	// Adopting a site quote is the site speaking, and it fills a flat price only
	// (the token family is already set, so the per-request slot is the free one).
	if _, err := db.RouteMember.AdoptObservedPrices(memberID, 9, 9, 0, 0.02); err != nil {
		t.Fatal(err)
	}
	got, err = db.RouteMember.GetByID(memberID)
	if err != nil || got == nil {
		t.Fatalf("member read after adopt: %v %v", got, err)
	}
	if got.PriceSource != "site" {
		t.Fatalf("adopt provenance: source=%q want site", got.PriceSource)
	}
	if got.PricePerRequest != 0.02 {
		t.Fatalf("adopt wrote price_per_request=%v want 0.02", got.PricePerRequest)
	}
	if got.PricePromptPer1k != 1.5 {
		t.Fatalf("adopt overwrote an existing prompt price: %v", got.PricePromptPer1k)
	}

	// The relay's candidate pool and the console's overview share one scan: the
	// provenance must survive that projection, not just the member-only reads.
	overviews, err := db.RouteMember.ListRouteOverviews()
	if err != nil {
		t.Fatal(err)
	}
	for _, overview := range overviews {
		if overview.Route.ID != routeID {
			continue
		}
		if len(overview.Members) != 1 {
			t.Fatalf("members in overview = %d", len(overview.Members))
		}
		member := overview.Members[0].Member
		if member.PriceSource != "site" || member.PriceUpdatedAt == "" {
			t.Fatalf("candidate projection lost provenance: source=%q at=%q", member.PriceSource, member.PriceUpdatedAt)
		}
		return
	}
	t.Fatalf("route %d missing from overviews", routeID)
}

package proxy

import (
	"testing"

	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
	"github.com/lan/meta-gateway/internal/usage"
)

func TestAliasBillingFollowsServingUpstreamInPersonalAndTeamModes(t *testing.T) {
	db, err := store.OpenTest(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	enc, _ := crypto.New("alias-billing-test-key")
	service, err := buildPoolService(db, enc)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for _, price := range []float64{0.25, 0.75} {
		channel, e := db.Channel.Create(&domain.Channel{Name: "source", Status: domain.StatusEnabled})
		if e != nil {
			t.Fatal(e)
		}
		route, e := db.RouteMember.SetChannelModelAlias(channel, "upstream-model", "public")
		if e != nil {
			t.Fatal(e)
		}
		members, e := db.RouteMember.ListByRoute(route)
		if e != nil {
			t.Fatal(e)
		}
		for _, member := range members {
			if member.ChannelID == channel {
				member.PricePromptPer1k = price
				if e = db.RouteMember.Update(&member); e != nil {
					t.Fatal(e)
				}
				ids = append(ids, member.ID)
			}
		}
	}
	for _, access := range []*domain.TeamAccess{nil, {}} {
		for index, want := range []float64{0.25, 0.75} {
			got := service.billingCost(Request{Model: "public", MemberID: ids[index], TeamAccess: access}, usage.Tokens{PromptTokens: 1000})
			if got != want {
				t.Fatalf("upstream %d cost=%v want=%v", ids[index], got, want)
			}
		}
	}
}

func TestAliasRetainsModelMetadataPriceWithoutMixingLayers(t *testing.T) {
	db, err := store.OpenTest(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	enc, _ := crypto.New("alias-model-prices")
	service, err := buildPoolService(db, enc)
	if err != nil {
		t.Fatal(err)
	}
	channel, err := db.Channel.Create(&domain.Channel{Name: "source", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	route, err := db.RouteMember.SetChannelModelAlias(channel, "upstream", "public")
	if err != nil {
		t.Fatal(err)
	}
	members, err := db.RouteMember.ListByRoute(route)
	if err != nil {
		t.Fatal(err)
	}
	member := members[0]
	if err = db.ModelMetadata.Upsert(&domain.ModelMetadata{ModelName: "upstream", PricePromptPer1k: 1.25, PriceCompletionPer1k: 2.5}); err != nil {
		t.Fatal(err)
	}
	req := Request{Model: "public", MemberID: member.ID}
	tokens := usage.Tokens{PromptTokens: 1000, CompletionTokens: 1000}
	if got := service.billingCost(req, tokens); got != 3.75 {
		t.Fatalf("renamed model became free: %v", got)
	}
	if err = db.ModelMetadata.Upsert(&domain.ModelMetadata{ModelName: "public", PriceCompletionPer1k: 4}); err != nil {
		t.Fatal(err)
	}
	if got := service.billingCost(req, tokens); got != 4 {
		t.Fatalf("public price layer mixed with source: %v", got)
	}
	member.PricePromptPer1k = .5
	if err = db.RouteMember.Update(&member); err != nil {
		t.Fatal(err)
	}
	if got := service.billingCost(req, tokens); got != .5 {
		t.Fatalf("member price no longer authoritative: %v", got)
	}
	// Old or externally edited rows must never turn a charge into a credit.
	member.PricePromptPer1k = -1
	member.PriceCompletionPer1k = 2
	if err = db.RouteMember.Update(&member); err != nil {
		t.Fatal(err)
	}
	if got := service.billingCost(req, tokens); got != 0 {
		t.Fatalf("invalid price produced a charge or credit: %v", got)
	}
}

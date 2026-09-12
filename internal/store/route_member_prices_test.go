package store_test

import (
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
)

func TestRouteMemberPricesRoundTrip(t *testing.T) {
	db := openTestDB(t)
	routeID, err := db.Route.Create(&domain.Route{ModelPattern: "m", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	channelID, err := db.Channel.Create(&domain.Channel{Name: "c", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	memberID, err := db.RouteMember.Create(&domain.RouteMember{
		RouteID: routeID, ChannelID: channelID, Weight: 100, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	member, err := db.RouteMember.GetByID(memberID)
	if err != nil || member == nil {
		t.Fatalf("get member: %v %v", member, err)
	}
	if member.PricePromptPer1k != 0 || member.PriceCompletionPer1k != 0 || member.PriceCachePer1k != 0 {
		t.Fatalf("fresh member prices = %+v", member)
	}

	member.PricePromptPer1k = 2
	member.PriceCompletionPer1k = 4
	member.PriceCachePer1k = 0.5
	if err := db.RouteMember.Update(member); err != nil {
		t.Fatal(err)
	}
	got, err := db.RouteMember.GetByID(memberID)
	if err != nil || got == nil {
		t.Fatal(err)
	}
	if got.PricePromptPer1k != 2 || got.PriceCompletionPer1k != 4 || got.PriceCachePer1k != 0.5 {
		t.Fatalf("prices round trip = %+v", got)
	}

	prompt, completion, cache, found, err := db.RouteMember.MemberPrices(routeID, channelID)
	if err != nil || !found {
		t.Fatalf("member prices lookup: found=%v err=%v", found, err)
	}
	if prompt != 2 || completion != 4 || cache != 0.5 {
		t.Fatalf("lookup = %v/%v/%v", prompt, completion, cache)
	}

	// An unpriced channel×route pair reports found=false (fall-through).
	_, _, _, found, err = db.RouteMember.MemberPrices(routeID, channelID+77)
	if err != nil || found {
		t.Fatalf("unpriced pair: found=%v err=%v", found, err)
	}
}

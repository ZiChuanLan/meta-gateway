package store_test

import (
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
)

// A route+channel pair legitimately holds SEVERAL member rows: the partial
// unique index only covers (route_id, channel_id, group_name) WHERE
// mapping_json = ”. Route groups and alias members both multiply the rows, so
// member prices must resolve by member id — resolving by the pair could bill a
// request through a member it never touched.
func TestMemberPricesResolvesTheExactMember(t *testing.T) {
	db := openTestDB(t)
	routeID, err := db.Route.Create(&domain.Route{ModelPattern: "m", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	channelID, err := db.Channel.Create(&domain.Channel{Name: "c", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	// Cheap member in the default group, low priority.
	cheapID, err := db.RouteMember.Create(&domain.RouteMember{
		RouteID: routeID, ChannelID: channelID, GroupName: "default",
		Priority: 1, Weight: 100, Enabled: true,
		PricePromptPer1k: 1, PriceCompletionPer1k: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Expensive member of the SAME route+channel in another group, higher priority.
	vipID, err := db.RouteMember.Create(&domain.RouteMember{
		RouteID: routeID, ChannelID: channelID, GroupName: "vip",
		Priority: 99, Weight: 100, Enabled: true,
		PricePromptPer1k: 50, PriceCompletionPer1k: 50,
	})
	if err != nil {
		t.Fatal(err)
	}

	prompt, completion, _, found, err := db.RouteMember.MemberPrices(cheapID)
	if err != nil || !found {
		t.Fatalf("cheap lookup: found=%v err=%v", found, err)
	}
	if prompt != 1 || completion != 1 {
		t.Fatalf("default-group member billed %v/%v, want 1/1 (sibling group leaked in)", prompt, completion)
	}

	prompt, completion, _, found, err = db.RouteMember.MemberPrices(vipID)
	if err != nil || !found {
		t.Fatalf("vip lookup: found=%v err=%v", found, err)
	}
	if prompt != 50 || completion != 50 {
		t.Fatalf("vip member billed %v/%v, want 50/50", prompt, completion)
	}

	// An unknown member reports found=false (fall through to the next layer).
	if _, _, _, found, err = db.RouteMember.MemberPrices(cheapID + vipID + 999); err != nil || found {
		t.Fatalf("unknown member: found=%v err=%v", found, err)
	}
}

// Copying a member group must carry the prices over: the copy is meant to be a
// usable clone, and silently unpriced members would bill through a different
// layer than the group they were cloned from.
func TestCopyMemberGroupCarriesPrices(t *testing.T) {
	db := openTestDB(t)
	routeID, err := db.Route.Create(&domain.Route{ModelPattern: "m", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	channelID, err := db.Channel.Create(&domain.Channel{Name: "c", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.RouteMember.Create(&domain.RouteMember{
		RouteID: routeID, ChannelID: channelID, GroupName: "default",
		Priority: 5, Weight: 100, Enabled: true,
		PricePromptPer1k: 3, PriceCompletionPer1k: 6, PriceCachePer1k: 1.5,
	}); err != nil {
		t.Fatal(err)
	}
	copied, err := db.RouteMember.CopyMemberGroup(routeID, "default", "vip")
	if err != nil || copied != 1 {
		t.Fatalf("copy = %d err=%v", copied, err)
	}
	members, err := db.RouteMember.ListByRoute(routeID)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range members {
		if m.GroupName != "vip" {
			continue
		}
		if m.PricePromptPer1k != 3 || m.PriceCompletionPer1k != 6 || m.PriceCachePer1k != 1.5 {
			t.Fatalf("cloned member lost its prices: %v/%v/%v",
				m.PricePromptPer1k, m.PriceCompletionPer1k, m.PriceCachePer1k)
		}
		return
	}
	t.Fatal("cloned vip member not found")
}

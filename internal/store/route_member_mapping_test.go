package store_test

import (
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
)

// TestRouteMemberMappingJSONRoundTrip verifies the per-member alias mapping
// (shared aliases) survives create/update and appears in route overviews.
func TestRouteMemberMappingJSONRoundTrip(t *testing.T) {
	db := openTestDB(t)
	siteID, _ := db.Site.Create(&domain.Site{Name: "site", Status: domain.StatusEnabled})
	credentialID, err := db.Credential.Create(&domain.Credential{SiteID: siteID, Kind: "api_key", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	channelA, err := db.Channel.Create(&domain.Channel{SiteID: &siteID, CredentialID: &credentialID, Name: "ch-a", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	channelB, err := db.Channel.Create(&domain.Channel{SiteID: &siteID, CredentialID: &credentialID, Name: "ch-b", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	routeID, err := db.Route.Create(&domain.Route{ModelPattern: "shared-alias", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	// Two channels share one alias route, each rewriting to its own model.
	firstID, err := db.RouteMember.Create(&domain.RouteMember{
		RouteID: routeID, ChannelID: channelA, MappingJSON: `{"real":"model-a"}`,
		Priority: 10, Weight: 100, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	secondID, err := db.RouteMember.Create(&domain.RouteMember{
		RouteID: routeID, ChannelID: channelB, MappingJSON: `{"real":"model-b"}`,
		Priority: 10, Weight: 100, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	member, err := db.RouteMember.GetByID(firstID)
	if err != nil {
		t.Fatal(err)
	}
	if member.MappingJSON != `{"real":"model-a"}` {
		t.Fatalf("GetByID mapping=%q", member.MappingJSON)
	}

	member.MappingJSON = `{"real":"model-a-updated"}`
	if err := db.RouteMember.Update(member); err != nil {
		t.Fatal(err)
	}

	overviews, err := db.RouteMember.ListRouteOverviews()
	if err != nil {
		t.Fatal(err)
	}
	var route *domain.RouteOverview
	for i := range overviews {
		if overviews[i].Route.ID == routeID {
			route = &overviews[i]
			break
		}
	}
	if route == nil {
		t.Fatal("shared alias route missing from overviews")
	}
	byChannel := map[int64]string{}
	for _, candidate := range route.Members {
		byChannel[candidate.Member.ChannelID] = candidate.Member.MappingJSON
	}
	if byChannel[channelA] != `{"real":"model-a-updated"}` || byChannel[channelB] != `{"real":"model-b"}` {
		t.Fatalf("overview member mappings=%v", byChannel)
	}

	// The routing candidate path (used by the proxy) must carry the mapping too.
	route2, candidates, err := db.RouteMember.RoutingCandidates("shared-alias")
	if err != nil {
		t.Fatal(err)
	}
	if route2 == nil {
		t.Fatal("routing candidates found no route")
	}
	if got := candidates[0].Member.MappingJSON; got == "" {
		t.Fatal("routing candidate missing member mapping_json")
	}
	_ = secondID
}

// TestSharedAliasSameChannelMultipleModels verifies several real models in the
// SAME channel can share one alias name: each gets its own mapped member row on
// the same route, while duplicate plain (mapping-less) members stay forbidden.
func TestSharedAliasSameChannelMultipleModels(t *testing.T) {
	db := openTestDB(t)
	siteID, _ := db.Site.Create(&domain.Site{Name: "site", Status: domain.StatusEnabled})
	credentialID, err := db.Credential.Create(&domain.Credential{SiteID: siteID, Kind: "api_key", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	channelID, err := db.Channel.Create(&domain.Channel{SiteID: &siteID, CredentialID: &credentialID, Name: "ch", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	routeID, err := db.Route.Create(&domain.Route{ModelPattern: "shared", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	base := domain.RouteMember{RouteID: routeID, ChannelID: channelID, Priority: 10, Weight: 100, Enabled: true}
	// First: the channel genuinely offers the alias name (plain member).
	plainID, err := db.RouteMember.Create(&base)
	if err != nil {
		t.Fatal(err)
	}
	// A duplicate plain member must still be rejected by the partial unique index.
	if _, err := db.RouteMember.Create(&base); err == nil {
		t.Fatal("duplicate plain member should be rejected")
	}
	// Two real models renamed to the same alias: both mapped members allowed.
	modelA, err := db.RouteMember.Create(&domain.RouteMember{RouteID: routeID, ChannelID: channelID, MappingJSON: `{"real":"model-a"}`, Priority: 10, Weight: 100, Enabled: true})
	if err != nil {
		t.Fatalf("first mapped member rejected: %v", err)
	}
	modelB, err := db.RouteMember.Create(&domain.RouteMember{RouteID: routeID, ChannelID: channelID, MappingJSON: `{"real":"model-b"}`, Priority: 10, Weight: 100, Enabled: true})
	if err != nil {
		t.Fatalf("second mapped member (shared alias) rejected: %v", err)
	}

	members, err := db.RouteMember.ListByRoute(routeID)
	if err != nil {
		t.Fatal(err)
	}
	mapped := 0
	for _, member := range members {
		if member.MappingJSON != "" {
			mapped++
		}
	}
	if mapped != 2 {
		t.Fatalf("mapped members = %d, want 2", mapped)
	}
	// The plain member survived alongside the mapped ones.
	foundPlain := false
	for _, member := range members {
		if member.ID == plainID {
			foundPlain = true
		}
	}
	if !foundPlain {
		t.Fatal("plain member vanished")
	}
	_ = modelA
	_ = modelB
}
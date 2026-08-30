package httpapi_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
)

// TestRouteGroupCopyAndListEndpoints pins the admin API surface for the
// "new group seeded from default" flow and the group pick list used by the
// key dialog.
func TestRouteGroupCopyAndListEndpoints(t *testing.T) {
	srv, db, _ := revealTestServer(t)
	base := srv.URL

	routeID, err := db.Route.Create(&domain.Route{ModelPattern: "copy-api", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	mkChannel := func(name string) int64 {
		id, err := db.Channel.Create(&domain.Channel{Name: name, Status: domain.StatusEnabled})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	c1, c2 := mkChannel("api-c1"), mkChannel("api-c2")
	createMember := func(channelID int64, group string, priority int) {
		if _, err := db.RouteMember.Create(&domain.RouteMember{
			RouteID: routeID, ChannelID: channelID, GroupName: group,
			Priority: priority, Enabled: true, Weight: 1,
		}); err != nil {
			t.Fatal(err)
		}
	}
	createMember(c1, "default", 5)
	createMember(c2, "default", 3)
	createMember(c1, "Z", 9)

	// Copying default into Z skips the channel already there.
	status, body, _ := adminCall(t, base, http.MethodPost,
		fmt.Sprintf("/admin/routes/%d/groups/copy", routeID),
		map[string]string{"from": "default", "to": "Z"})
	if status != http.StatusOK {
		t.Fatalf("copy status = %d body %v", status, body)
	}
	if copied, _ := body["copied"].(float64); copied != 1 {
		t.Fatalf("copy copied = %v, want 1", body["copied"])
	}
	members, err := db.RouteMember.ListByRoute(routeID)
	if err != nil {
		t.Fatal(err)
	}
	zMembers := 0
	for _, m := range members {
		if m.GroupName == "Z" {
			zMembers++
		}
	}
	if zMembers != 2 {
		t.Fatalf("group Z member count = %d, want 2", zMembers)
	}

	// Missing target group is rejected before touching the store.
	status, _, _ = adminCall(t, base, http.MethodPost,
		fmt.Sprintf("/admin/routes/%d/groups/copy", routeID),
		map[string]string{"from": "default"})
	if status != http.StatusBadRequest {
		t.Fatalf("copy without to status = %d, want 400", status)
	}

	// The pick list contains every distinct group name across routes.
	status, body, _ = adminCall(t, base, http.MethodGet, "/admin/route-groups", nil)
	if status != http.StatusOK {
		t.Fatalf("route-groups status = %d", status)
	}
	groups, _ := body["groups"].([]any)
	if len(groups) != 2 || groups[0] != "Z" || groups[1] != "default" {
		t.Fatalf("route-groups = %v, want [Z default]", groups)
	}
}

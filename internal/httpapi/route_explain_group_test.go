package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/routing"
)

func TestRouteExplanationUsesRequestedGroupWithoutForwarding(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(http.StatusOK) }))
	defer upstream.Close()
	serverURL, _, routeID, db := setupImageRelayWithStore(t, upstream.URL, "test-model")
	var channelID int64
	if err := db.QueryRow(`SELECT channel_id FROM route_members WHERE route_id = ?`, routeID).Scan(&channelID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.RouteMember.Create(&domain.RouteMember{RouteID: routeID, ChannelID: channelID, GroupName: "private", Priority: 7, Weight: 100, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		requested, effective string
		fallback             bool
		count                int
	}{
		{"", "", false, 2}, {"default", "default", false, 1},
		{"private", "private", false, 1}, {"missing", "default", true, 1},
	} {
		body := get(t, serverURL+"/admin/routes/explain?model=test-model&route_group="+url.QueryEscape(tc.requested))
		var explanation routing.Explanation
		if err := json.Unmarshal(body, &explanation); err != nil {
			t.Fatal(err)
		}
		if explanation.RequestedGroup != tc.requested || explanation.RouteGroup != tc.effective || explanation.GroupFallback != tc.fallback || len(explanation.Candidates) != tc.count {
			t.Fatalf("group %q: %+v", tc.requested, explanation)
		}
		if tc.effective != "" && explanation.Candidates[0].Candidate.Member.GroupName != tc.effective {
			t.Fatal("explanation mixed route groups")
		}
	}
	// Pinning applies only when the pinned member belongs to the selected
	// pool. The explanation must agree with the selector's retry override.
	if _, err := db.Exec(`UPDATE routes SET routing_mode = 'single', single_member_id = (SELECT id FROM route_members WHERE route_id = ? AND group_name = 'default') WHERE id = ?`, routeID, routeID); err != nil {
		t.Fatal(err)
	}
	for _, group := range []string{"default", "private"} {
		var explanation routing.Explanation
		if err := json.Unmarshal(get(t, serverURL+"/admin/routes/explain?model=test-model&route_group="+group), &explanation); err != nil {
			t.Fatal(err)
		}
		if group == "default" {
			if explanation.RetryTimesOverride == nil || *explanation.RetryTimesOverride != 0 {
				t.Fatalf("pinned pool retry override = %v", explanation.RetryTimesOverride)
			}
		} else if explanation.RetryTimesOverride != nil {
			t.Fatalf("pin outside requested group changed its retry policy: %v", *explanation.RetryTimesOverride)
		}
	}
	// With no default members, the repository retains its legacy fallback
	// to all groups. Never label a mixed pool as its first member's group.
	if _, err := db.Exec(`UPDATE route_members SET group_name = 'team' WHERE route_id = ? AND group_name = 'default'`, routeID); err != nil {
		t.Fatal(err)
	}
	var mixed routing.Explanation
	if err := json.Unmarshal(get(t, serverURL+"/admin/routes/explain?model=test-model&route_group=missing"), &mixed); err != nil {
		t.Fatal(err)
	}
	if !mixed.GroupFallback || mixed.RouteGroup != "" || len(mixed.Candidates) != 2 {
		t.Fatalf("legacy fallback mislabeled: %+v", mixed)
	}
	if calls.Load() != 0 {
		t.Fatal("route explanation made an upstream request")
	}
	for _, table := range []string{"proxy_logs", "usage_records"} {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("explanation wrote %s", table)
		}
	}
	request, _ := http.NewRequest(http.MethodGet, serverURL+"/admin/routes/explain?model=test-model&route_group="+strings.Repeat("x", 65), nil)
	request.Header.Set("Authorization", "Bearer admin-test")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid group status=%d", response.StatusCode)
	}
}

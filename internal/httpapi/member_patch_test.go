package httpapi_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
)

func TestMemberPartialUpdatePreservesWeightAndCooling(t *testing.T) {
	srv, db, _ := revealTestServer(t)
	ch, err := db.Channel.Create(&domain.Channel{Name: "channel", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	route, err := db.Route.Create(&domain.Route{ModelPattern: "model", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	member, err := db.RouteMember.Create(&domain.RouteMember{RouteID: route, ChannelID: ch, Weight: 37, Priority: 8, Enabled: true, GroupName: "vip", PricePromptPer1k: 2})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.RouteMember.RecordFailure(member, time.Now(), time.Hour, "failed"); err != nil {
		t.Fatal(err)
	}
	status, _, raw := adminCall(t, srv.URL, http.MethodPut, fmt.Sprintf("/admin/route-members/%d", member), map[string]any{"priority": 9, "fail_count": 0, "cooldown_until": nil, "last_error": ""})
	if status != 200 {
		t.Fatalf("status=%d %s", status, raw)
	}
	got, _ := db.RouteMember.GetByID(member)
	if got.Priority != 9 || got.Weight != 37 || got.GroupName != "vip" || got.PricePromptPer1k != 2 || got.CooldownUntil == nil || got.FailCount != 1 {
		t.Fatalf("bad patch: %+v", got)
	}
	status, _, raw = adminCall(t, srv.URL, http.MethodPost, fmt.Sprintf("/admin/channels/%d/model-alias", ch), map[string]string{"model": "model", "alias": "public"})
	if status != 200 {
		t.Fatalf("alias=%d %s", status, raw)
	}
	got, _ = db.RouteMember.GetByID(member)
	if got == nil || got.Weight != 37 || got.CooldownUntil == nil {
		t.Fatalf("alias lost binding: %+v", got)
	}
	status, _, _ = adminCall(t, srv.URL, http.MethodPut, fmt.Sprintf("/admin/route-members/%d", member), map[string]any{"price_prompt_per_1k": -1})
	if status != 400 {
		t.Fatalf("negative price accepted: %d", status)
	}
	if _, err = db.Exec(`UPDATE route_members SET enabled=0,auto_disabled=1 WHERE id=?`, member); err != nil {
		t.Fatal(err)
	}
	status, _, _ = adminCall(t, srv.URL, http.MethodPut, fmt.Sprintf("/admin/route-members/%d", member), map[string]any{"enabled": false})
	if status != 200 {
		t.Fatalf("manual disabled intent: %d", status)
	}
	got, _ = db.RouteMember.GetByID(member)
	if got.AutoDisabled || got.Enabled {
		t.Fatalf("manual disabled intent lost: %+v", got)
	}
}

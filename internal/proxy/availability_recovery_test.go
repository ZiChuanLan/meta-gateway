package proxy

import (
	"context"
	"github.com/lan/meta-gateway/internal/relay"
	"net/http"
	"strings"
	"testing"
)

func TestPinnedRecoveryProbeReachesAutoDisabledAliasOnly(t *testing.T) {
	upstream := &queuedRelay{results: []*relay.Result{response(http.StatusOK, `{"choices":[{"message":{"content":"ok"}}]}`)}}
	service, db, id, _ := setupProxy(t, upstream)
	member, err := db.RouteMember.GetByID(id)
	if err != nil {
		t.Fatal(err)
	}
	member.MappingJSON = `{"real":"original-model"}`
	member.Enabled = false
	if err := db.RouteMember.Update(member); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE route_members SET auto_disabled=1 WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	result, meta := service.ChatCompletionsWithMeta(context.Background(), Request{RequestID: "recovery", Model: "model", Body: []byte(`{"model":"model","messages":[{"role":"user","content":"hi"}]}`), Probe: true, PreferMemberID: id})
	if result.Err != nil || result.StatusCode != 200 || meta == nil || meta.MemberID != id {
		t.Fatalf("recovery result=%+v meta=%+v", result, meta)
	}
	result.Body.Close()
	if len(upstream.bodies) != 1 || !strings.Contains(string(upstream.bodies[0]), `"model":"original-model"`) {
		t.Fatalf("wire body=%q", upstream.bodies)
	}
	var logs int
	db.QueryRow(`SELECT COUNT(*) FROM proxy_logs WHERE request_id='recovery'`).Scan(&logs)
	if logs != 0 {
		t.Fatalf("probe polluted traffic log: %d", logs)
	}
	// A manually disabled row never gains this exception.
	db.Exec(`UPDATE route_members SET auto_disabled=0 WHERE id=?`, id)
	result, _ = service.ChatCompletionsWithMeta(context.Background(), Request{Model: "model", Body: []byte(`{"model":"model","messages":[]}`), Probe: true, PreferMemberID: id})
	if result.Err == nil || len(upstream.bodies) != 1 {
		t.Fatalf("manual disable bypassed: %+v calls=%d", result, len(upstream.bodies))
	}
}

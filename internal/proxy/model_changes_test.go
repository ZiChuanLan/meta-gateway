package proxy

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/relay"
	"github.com/lan/meta-gateway/internal/store"
)

// Exercise discovery -> maintenance preview/apply -> the shipped routing and
// proxy path. Only the remote transport is a fixture; mapping is not reimplemented.
func TestModelChangeReplacementReachesProxy(t *testing.T) {
	upstream := &queuedRelay{results: []*relay.Result{response(http.StatusOK, `{"ok":true}`)}}
	service, db, memberID, _ := setupProxy(t, upstream)
	member, err := db.RouteMember.GetByID(memberID)
	if err != nil {
		t.Fatal(err)
	}
	routeBefore, err := db.Route.GetByID(member.RouteID)
	if err != nil {
		t.Fatal(err)
	}
	for _, models := range [][]string{{"model"}, {"replacement"}} {
		if _, err := db.DiscoveredModel.Reconcile(context.Background(), store.ReconcileInput{ChannelID: member.ChannelID, Models: models, CheckedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	changes, err := db.ModelChanges()
	if err != nil {
		t.Fatal(err)
	}
	req := store.ModelChangeRequest{MemberIDs: []int64{memberID}, TargetChannelID: member.ChannelID, TargetModel: "replacement"}
	for _, change := range changes.Items {
		if change.Kind == "removed" && change.ModelName == "model" {
			req.ChangeIDs = []int64{change.ID}
		}
	}
	preview, err := db.PreviewModelChanges(req)
	if err != nil {
		t.Fatal(err)
	}
	req.PreviewToken = preview.PreviewToken
	if _, err := db.ApplyModelChanges(req); err != nil {
		t.Fatal(err)
	}
	result := service.ChatCompletions(context.Background(), Request{RequestID: "model-change", Model: "model", Body: []byte(`{"model":"model","messages":[{"role":"user","content":"hi"}]}`)})
	if result.Err != nil || result.StatusCode != http.StatusOK {
		t.Fatalf("proxy: %+v", result)
	}
	defer result.Body.Close()
	if len(upstream.bodies) != 1 || !strings.Contains(string(upstream.bodies[0]), `"model":"replacement"`) {
		t.Fatalf("wrong upstream payload: %q", upstream.bodies)
	}
	routeAfter, err := db.Route.GetByID(member.RouteID)
	if err != nil {
		t.Fatal(err)
	}
	if routeAfter.ModelPattern != routeBefore.ModelPattern || routeAfter.ID != routeBefore.ID {
		t.Fatalf("public route changed: %+v", routeAfter)
	}
}

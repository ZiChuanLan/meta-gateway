package siteprobe

import (
	"context"
	"github.com/lan/meta-gateway/internal/domain"
	"testing"
)

func TestApplyTrafficWithoutSiteRoundsDoesNotPanic(t *testing.T) {
	source := &ratioSource{}
	source.set(9, 1)
	f := newFixture(t, source, "m", "m")
	for i := 0; i < 5; i++ {
		if _, err := f.db.ProxyLog.Insert(&domain.ProxyLog{ChannelID: f.channelID, RouteID: f.routeID, Model: "m", UpstreamModel: "m", Status: 503}); err != nil {
			t.Fatal(err)
		}
	}
	actions, err := f.service.Apply(context.Background(), ApplyRequest{DryRun: true, Policy: DefaultPolicy()})
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 1 || actions[0].Kind != "disable" {
		t.Fatalf("actions=%+v", actions)
	}
}

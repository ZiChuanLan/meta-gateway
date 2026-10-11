package httpapi

import (
	"context"
	"errors"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/proxy"
	"github.com/lan/meta-gateway/internal/relay"
	"github.com/lan/meta-gateway/internal/store"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

type brokenObservationBody struct{}

func (brokenObservationBody) Read([]byte) (int, error) { return 0, errors.New("upstream stream broke") }
func (brokenObservationBody) Close() error             { return nil }

func TestObservedDeliveryDistinguishesCompleteBrokenAndCanceled(t *testing.T) {
	db, err := store.OpenTest(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	routeID, err := db.Route.Create(&domain.Route{ModelPattern: "alias", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	handler := &RelayHandler{db: db}
	for _, tc := range []struct {
		name, want string
		body       io.ReadCloser
		canceled   bool
	}{
		{"complete", "ok", io.NopCloser(strings.NewReader(`{"choices":[]}`)), false},
		{"broken", "failed", brokenObservationBody{}, false},
		{"canceled", "canceled", io.NopCloser(strings.NewReader(`{"choices":[]}`)), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.canceled {
				cancel()
			}
			logID, err := db.ProxyLog.Insert(&domain.ProxyLog{RequestID: tc.name, ChannelID: 8, MemberID: 9, RouteID: routeID, Model: "alias", UpstreamModel: "real", Status: 200})
			if err != nil {
				t.Fatal(err)
			}
			handler.writeObservedUpstreamResult(httptest.NewRecorder(), ctx, proxy.Request{RequestID: tc.name, Model: "alias", OpenAIPath: "chat/completions"},
				&proxy.AttemptMeta{RouteID: routeID, MemberID: 9, ChannelID: 8, UpstreamModel: "real", AttemptLogID: logID, RouteGroup: "default"}, &relay.Result{StatusCode: 200, Body: tc.body}, false, nil, nil, nil)
			var outcome, logOutcome string
			if err := db.QueryRow(`SELECT outcome FROM route_request_outcomes WHERE request_id=?`, tc.name).Scan(&outcome); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRow(`SELECT completion_outcome FROM proxy_logs WHERE request_id=?`, tc.name).Scan(&logOutcome); err != nil {
				t.Fatal(err)
			}
			if outcome != tc.want || logOutcome != tc.want {
				t.Fatalf("outcome=%s log=%s want %s", outcome, logOutcome, tc.want)
			}
		})
	}
}

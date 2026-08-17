package proxy

import (
	"context"
	"net/http"
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/relay"
)

func TestNonIdempotentWriteDoesNotRotateAPIKeys(t *testing.T) {
	upstream := &queuedRelay{results: []*relay.Result{
		response(http.StatusServiceUnavailable, `{"error":"uncertain delivery"}`),
		response(http.StatusOK, `{"data":[]}`),
	}}
	service, db, highMemberID, _ := setupProxy(t, upstream)
	service.SetChannelRetryTimes(3)
	service.SetKeyPoolRotation(true)
	member, err := db.RouteMember.GetByID(highMemberID)
	if err != nil {
		t.Fatal(err)
	}
	channel, err := db.Channel.GetByID(member.ChannelID)
	if err != nil || channel.SiteID == nil {
		t.Fatalf("channel=%+v err=%v", channel, err)
	}
	route, err := db.Route.GetByID(member.RouteID)
	if err != nil {
		t.Fatal(err)
	}
	retries := 3
	route.RetryTimes = &retries
	if err := db.Route.Update(route); err != nil {
		t.Fatal(err)
	}
	secondSecret, err := service.enc.Encrypt([]byte("second-secret"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Credential.Create(&domain.Credential{
		SiteID:    *channel.SiteID,
		Kind:      "api_key",
		SecretEnc: []byte(secondSecret),
		Status:    domain.StatusEnabled,
	}); err != nil {
		t.Fatal(err)
	}

	result := service.ChatCompletions(context.Background(), Request{
		RequestID:  "unsafe-image-generation",
		Model:      "model",
		Method:     http.MethodPost,
		OpenAIPath: "images/generations",
		Body:       []byte(`{"model":"model","prompt":"draw"}`),
	})
	if result.Body != nil {
		defer result.Body.Close()
	}
	if len(upstream.calls) != 1 {
		t.Fatalf("non-idempotent write was replayed %d times, want exactly one", len(upstream.calls))
	}
}

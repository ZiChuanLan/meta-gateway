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

func TestImageWritesNeverReplayAfterRefreshOrIdempotencyHeader(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			upstream := &queuedRelay{results: []*relay.Result{
				response(status, `{"error":"image request failed"}`),
				response(http.StatusOK, `{"data":[]}`),
			}}
			service, db, memberID, _ := setupProxy(t, upstream)
			member, err := db.RouteMember.GetByID(memberID)
			if err != nil {
				t.Fatal(err)
			}
			channel, err := db.Channel.GetByID(member.ChannelID)
			if err != nil {
				t.Fatal(err)
			}
			credential, err := db.Credential.GetByID(*channel.CredentialID)
			if err != nil {
				t.Fatal(err)
			}
			credential.Kind = "session"
			if err := db.Credential.Update(credential); err != nil {
				t.Fatal(err)
			}
			refresher := &fakeRefresher{ok: true}
			service.SetCredentialRefresher(refresher)
			service.SetChannelRetryTimes(3)
			result := service.ChatCompletions(context.Background(), Request{
				RequestID: "image-no-replay", Model: "model", Method: http.MethodPost,
				OpenAIPath: "images/edits", Body: []byte(`{"model":"model","prompt":"edit"}`),
				Headers: map[string]string{"Idempotency-Key": "image-operation"},
			})
			if result != nil && result.Body != nil {
				defer result.Body.Close()
			}
			if len(upstream.calls) != 1 || refresher.calls != 0 {
				t.Fatalf("image request replayed: requests=%d refreshes=%d", len(upstream.calls), refresher.calls)
			}
		})
	}
}

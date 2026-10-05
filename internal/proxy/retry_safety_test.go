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

// TestRetrySafeRequestMatrix pins the whole non-idempotent-write contract in one
// place. The behaviour tests above prove the proxy honours it for images; this
// one proves *which* paths are on the list, because the failure mode of getting
// it wrong is a silently replayed generation and a second charge.
func TestRetrySafeRequestMatrix(t *testing.T) {
	cases := []struct {
		path          string
		idempotency   bool
		wantRetrySafe bool
		why           string
	}{
		// Generation families are billed on acceptance, so they are never replayed.
		{"images/generations", false, false, "image generation is charged on acceptance"},
		{"images/edits", false, false, "image edit is charged on acceptance"},
		{"images/variations", false, false, "image variation is charged on acceptance"},
		{"audio/speech", false, false, "speech synthesis is charged on acceptance"},
		{"audio/transcriptions", false, false, "transcription is charged on acceptance"},
		{"audio/translations", false, false, "translation is charged on acceptance"},
		// Video and music have no registered endpoint; they reach the gateway
		// through the /v1/* passthrough, so nothing else knows to protect them.
		{"videos/generations", false, false, "video generation arrives via the /v1/* passthrough"},
		{"video/generations", false, false, "same family, singular spelling"},
		{"music/generations", false, false, "music generation is charged on acceptance"},
		{"responses", false, false, "the Responses API is not replayable"},

		// An Idempotency-Key is the caller's promise that the upstream can dedupe,
		// so it re-opens retries for everything except images.
		{"audio/speech", true, true, "an idempotency key re-opens retries for audio"},
		{"videos/generations", true, true, "an idempotency key re-opens retries for video"},
		{"responses", true, true, "an idempotency key re-opens retries for responses"},
		{"images/generations", true, false, "a header cannot dedupe an image across channels"},

		// Read-like calls keep their retries.
		{"chat/completions", false, true, "chat is read-like"},
		{"completions", false, true, "completions is read-like"},
		{"embeddings", false, true, "embeddings is read-like"},
		{"moderations", false, true, "moderations is read-like"},
		{"systemone", false, true, "an unknown custom path is read-like until proven otherwise"},
		{"v1/chat/completions", false, true, "a path that still carries the v1/ prefix is normalised"},
	}

	for _, tc := range cases {
		name := tc.path
		if tc.idempotency {
			name += "+idempotency-key"
		}
		t.Run(name, func(t *testing.T) {
			req := Request{Method: http.MethodPost, OpenAIPath: tc.path, Headers: map[string]string{}}
			if tc.idempotency {
				req.Headers["Idempotency-Key"] = "abc"
			}
			if got := retrySafeRequest(req); got != tc.wantRetrySafe {
				t.Fatalf("retrySafeRequest(%q) = %v, want %v — %s", tc.path, got, tc.wantRetrySafe, tc.why)
			}
		})
	}

	// A read is always replayable, whatever the path says.
	if !retrySafeRequest(Request{Method: http.MethodGet, OpenAIPath: "images/generations"}) {
		t.Fatal("GET images/generations must stay retry-safe: it is a read, not a write")
	}
	// Header lookup is case-insensitive, so a client spelling it differently
	// still gets the escape hatch.
	req := Request{
		Method:     http.MethodPost,
		OpenAIPath: "videos/generations",
		Headers:    map[string]string{"idempotency-key": "abc"},
	}
	if !retrySafeRequest(req) {
		t.Fatal("a lower-case idempotency-key header must be honoured")
	}
}

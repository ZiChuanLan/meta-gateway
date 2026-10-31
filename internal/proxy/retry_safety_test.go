package proxy

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/lan/meta-gateway/internal/adapters"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/relay"
	"github.com/lan/meta-gateway/internal/store"
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

// addSecondKey registers one more enabled API key on the member's channel, so a
// test can tell key rotation apart from channel failover: same channel = same
// upstream URL in queuedRelay.calls.
func addSecondKey(t *testing.T, service *Service, db *store.DB, memberID int64, secret string) {
	t.Helper()
	channel, err := db.Channel.GetByID(channelOfMember(t, db, memberID))
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := service.enc.Encrypt([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Credential.Create(&domain.Credential{
		SiteID:    *channel.SiteID,
		Kind:      "api_key",
		SecretEnc: []byte(encrypted),
		Status:    domain.StatusEnabled,
	}); err != nil {
		t.Fatal(err)
	}
}

// TestImageFailoverOptInRotatesKeysOnRateLimit covers the escape hatch the
// operator asked for: with images failover on, a channel that answers 429 is
// walked to its next key instead of failing the client's request. 429 is safe
// to replay because the upstream refused before generating (and billing)
// anything.
func TestImageFailoverOptInRotatesKeysOnRateLimit(t *testing.T) {
	upstream := &queuedRelay{results: []*relay.Result{
		response(http.StatusTooManyRequests, `{"error":"rate limited"}`),
		response(http.StatusOK, `{"data":[]}`),
	}}
	service, db, memberID, _ := setupProxy(t, upstream)
	service.SetImagesFailoverEnabled(true)
	addSecondKey(t, service, db, memberID, "second-secret")

	result := service.ChatCompletions(context.Background(), Request{
		RequestID:  "image-429-failover",
		Model:      "model",
		Method:     http.MethodPost,
		OpenAIPath: "images/edits",
		Body:       []byte(`{"model":"model","prompt":"edit"}`),
	})
	if result != nil && result.Body != nil {
		defer result.Body.Close()
	}
	if len(upstream.calls) != 2 {
		t.Fatalf("upstream calls = %d, want 2: a 429 must move to the next key", len(upstream.calls))
	}
	if result == nil || result.StatusCode != http.StatusOK {
		t.Fatalf("result = %+v, want the second key's 200", result)
	}
}

// TestImageFailoverOptInMovesToAnotherChannel is the other half of the same
// request: when the channel's single key is rate-limited, the walk continues on
// the next channel instead of returning the 429 to the client.
func TestImageFailoverOptInMovesToAnotherChannel(t *testing.T) {
	upstream := &queuedRelay{results: []*relay.Result{
		response(http.StatusTooManyRequests, `{"error":"rate limited"}`),
		response(http.StatusOK, `{"data":[]}`),
	}}
	service, _, _, _ := setupProxy(t, upstream)
	service.SetImagesFailoverEnabled(true)

	result := service.ChatCompletions(context.Background(), Request{
		RequestID:  "image-429-channel",
		Model:      "model",
		Method:     http.MethodPost,
		OpenAIPath: "images/edits",
		Body:       []byte(`{"model":"model","prompt":"edit"}`),
	})
	if result != nil && result.Body != nil {
		defer result.Body.Close()
	}
	if len(upstream.calls) != 2 {
		t.Fatalf("upstream calls = %d, want 2: a 429 must move to the next channel", len(upstream.calls))
	}
	if upstream.calls[0] == upstream.calls[1] {
		t.Fatalf("both attempts hit %s: the walk stayed on the rate-limited channel", upstream.calls[0])
	}
	if result == nil || result.StatusCode != http.StatusOK {
		t.Fatalf("result = %+v, want the second channel's 200", result)
	}
}

// TestImageFailoverOptInStopsOnAmbiguousFailures pins the half of the contract
// that protects the operator's money: 5xx, 504 and a dead transport may all be
// answers to a generation the upstream already charged for, so they end the
// request after one attempt even with the opt-in on.
func TestImageFailoverOptInStopsOnAmbiguousFailures(t *testing.T) {
	cases := []struct {
		name   string
		result *relay.Result
	}{
		{"504 gateway timeout", response(http.StatusGatewayTimeout, `{"error":"timeout"}`)},
		{"500 upstream error", response(http.StatusInternalServerError, `{"error":"boom"}`)},
		{"502 bad gateway", response(http.StatusBadGateway, `{"error":"bad gateway"}`)},
		// A 4xx that is not on the whitelist must not slip out through the
		// generic "4xx means try another channel" branch.
		{"409 conflict", response(http.StatusConflict, `{"error":"conflict"}`)},
		{"transport timeout", &relay.Result{Err: context.DeadlineExceeded}},
		{"dead stream", &relay.Result{StatusCode: http.StatusOK, Err: ErrEmptyCompletion}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			upstream := &queuedRelay{results: []*relay.Result{
				tc.result,
				response(http.StatusOK, `{"data":[]}`),
			}}
			service, db, memberID, _ := setupProxy(t, upstream)
			service.SetImagesFailoverEnabled(true)
			addSecondKey(t, service, db, memberID, "second-secret")

			result := service.ChatCompletions(context.Background(), Request{
				RequestID:  "image-ambiguous",
				Model:      "model",
				Method:     http.MethodPost,
				OpenAIPath: "images/edits",
				Body:       []byte(`{"model":"model","prompt":"edit"}`),
			})
			if result != nil && result.Body != nil {
				defer result.Body.Close()
			}
			if len(upstream.calls) != 1 {
				t.Fatalf("upstream calls = %d, want 1: %s may already be billed", len(upstream.calls), tc.name)
			}
		})
	}
}

// TestImageFailoverWarnedTierRetriesServerErrors covers the second, warned
// tier: with it on, a 500/502 is walked to the next key and then to the next
// channel instead of ending the request, because the operator accepted that a
// server error may arrive after the picture was generated and charged.
func TestImageFailoverWarnedTierRetriesServerErrors(t *testing.T) {
	cases := []struct {
		name   string
		result *relay.Result
	}{
		{"500 upstream error", response(http.StatusInternalServerError, `{"error":"boom"}`)},
		{"502 bad gateway", response(http.StatusBadGateway, `{"error":"bad gateway"}`)},
		{"507 insufficient storage", response(http.StatusInsufficientStorage, `{"error":"full"}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			upstream := &queuedRelay{results: []*relay.Result{
				tc.result,
				response(http.StatusOK, `{"data":[]}`),
			}}
			service, db, memberID, _ := setupProxy(t, upstream)
			service.SetImagesFailoverEnabled(true)
			service.SetImagesFailoverIncludeAmbiguous(true)
			addSecondKey(t, service, db, memberID, "second-secret")

			result := service.ChatCompletions(context.Background(), Request{
				RequestID:  "image-warned-tier",
				Model:      "model",
				Method:     http.MethodPost,
				OpenAIPath: "images/edits",
				Body:       []byte(`{"model":"model","prompt":"edit"}`),
			})
			if result != nil && result.Body != nil {
				defer result.Body.Close()
			}
			if len(upstream.calls) != 2 {
				t.Fatalf("upstream calls = %d, want 2: the warned tier must replay %s", len(upstream.calls), tc.name)
			}
			if result == nil || result.StatusCode != http.StatusOK {
				t.Fatalf("result = %+v, want the second attempt's 200", result)
			}
		})
	}
}

// TestImageFailoverWarnedTierStillStopsOnLostAnswers is the boundary the
// operator cannot buy away: 504, a transport timeout, a dead stream and an
// empty 2xx all mean "it may already exist", so even the warned tier stops
// after one attempt.
func TestImageFailoverWarnedTierStillStopsOnLostAnswers(t *testing.T) {
	cases := []struct {
		name   string
		result *relay.Result
	}{
		{"504 gateway timeout", response(http.StatusGatewayTimeout, `{"error":"timeout"}`)},
		{"transport timeout", &relay.Result{Err: context.DeadlineExceeded}},
		{"dead stream", &relay.Result{StatusCode: http.StatusOK, Err: ErrEmptyCompletion}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			upstream := &queuedRelay{results: []*relay.Result{
				tc.result,
				response(http.StatusOK, `{"data":[]}`),
			}}
			service, db, memberID, _ := setupProxy(t, upstream)
			service.SetImagesFailoverEnabled(true)
			service.SetImagesFailoverIncludeAmbiguous(true)
			addSecondKey(t, service, db, memberID, "second-secret")

			result := service.ChatCompletions(context.Background(), Request{
				RequestID:  "image-warned-tier-stop",
				Model:      "model",
				Method:     http.MethodPost,
				OpenAIPath: "images/edits",
				Body:       []byte(`{"model":"model","prompt":"edit"}`),
			})
			if result != nil && result.Body != nil {
				defer result.Body.Close()
			}
			if len(upstream.calls) != 1 {
				t.Fatalf("upstream calls = %d, want 1: %s may already be billed", len(upstream.calls), tc.name)
			}
		})
	}
}

// TestImageFailoverWarnedTierRetriesTheSameKeyFirst is the case that makes the
// tier useful at all: one upstream, nothing to walk to. A 500 must be re-sent on
// the same key before the channel walk, otherwise "retry until a picture comes
// back" would do nothing on the most common self-hosted setup.
func TestImageFailoverWarnedTierRetriesTheSameKeyFirst(t *testing.T) {
	upstream := &queuedRelay{results: []*relay.Result{
		response(http.StatusInternalServerError, `{"error":"boom"}`),
		response(http.StatusOK, `{"data":[]}`),
	}}
	service, _, _, _ := setupProxy(t, upstream)
	service.SetImagesFailoverEnabled(true)
	service.SetImagesFailoverIncludeAmbiguous(true)
	service.SetChannelRetryTimes(1)

	result := service.ChatCompletions(context.Background(), Request{
		RequestID:  "image-warned-tier-single",
		Model:      "model",
		Method:     http.MethodPost,
		OpenAIPath: "images/edits",
		Body:       []byte(`{"model":"model","prompt":"edit"}`),
	})
	if result != nil && result.Body != nil {
		defer result.Body.Close()
	}
	if len(upstream.calls) != 2 {
		t.Fatalf("upstream calls = %d, want 2: the warned tier must re-send", len(upstream.calls))
	}
	if upstream.calls[0] != upstream.calls[1] {
		t.Fatalf("attempts hit %s then %s: a 5xx must be re-sent on the same key before the walk",
			upstream.calls[0], upstream.calls[1])
	}
	if result == nil || result.StatusCode != http.StatusOK {
		t.Fatalf("result = %+v, want the re-send's 200", result)
	}
}

// TestImageFailoverRefusalsStillWalkKeys keeps the same-key budget scoped to
// 5xx: a rate limit cannot heal in a millisecond, so it must move to the next
// key/channel instead of being hammered — even in the warned tier.
func TestImageFailoverRefusalsStillWalkKeys(t *testing.T) {
	upstream := &queuedRelay{results: []*relay.Result{
		response(http.StatusTooManyRequests, `{"error":"rate limited"}`),
		response(http.StatusOK, `{"data":[]}`),
	}}
	service, _, _, _ := setupProxy(t, upstream)
	service.SetImagesFailoverEnabled(true)
	service.SetImagesFailoverIncludeAmbiguous(true)
	service.SetChannelRetryTimes(2)

	result := service.ChatCompletions(context.Background(), Request{
		RequestID:  "image-refusal-walks",
		Model:      "model",
		Method:     http.MethodPost,
		OpenAIPath: "images/edits",
		Body:       []byte(`{"model":"model","prompt":"edit"}`),
	})
	if result != nil && result.Body != nil {
		defer result.Body.Close()
	}
	if len(upstream.calls) != 2 {
		t.Fatalf("upstream calls = %d, want 2", len(upstream.calls))
	}
	if upstream.calls[0] == upstream.calls[1] {
		t.Fatalf("a 429 was re-sent to %s: refusals must walk, not hammer", upstream.calls[0])
	}
}

// TestImageFailoverBluntTierReplaysLostAnswers is the tier the operator asked
// for by name: "if it timed out, just send it again". 504, a transport timeout
// and an empty 2xx are exactly the answers the two safer tiers refuse to replay,
// because the picture may already exist — here the operator has decided a usable
// picture beats a dead request, so the walk continues.
func TestImageFailoverBluntTierReplaysLostAnswers(t *testing.T) {
	cases := []struct {
		name   string
		result *relay.Result
	}{
		{"504 gateway timeout", response(http.StatusGatewayTimeout, `{"error":"timeout"}`)},
		{"transport timeout", &relay.Result{Err: context.DeadlineExceeded}},
		{"empty 2xx", &relay.Result{StatusCode: http.StatusOK, Err: ErrEmptyCompletion}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			upstream := &queuedRelay{results: []*relay.Result{
				tc.result,
				response(http.StatusOK, `{"data":[]}`),
			}}
			service, _, _, _ := setupProxy(t, upstream)
			service.SetImagesFailoverEnabled(true)
			service.SetImagesFailoverIncludeUncertain(true)
			service.SetChannelRetryTimes(1)

			result := service.ChatCompletions(context.Background(), Request{
				RequestID:  "image-blunt-tier",
				Model:      "model",
				Method:     http.MethodPost,
				OpenAIPath: "images/edits",
				Body:       []byte(`{"model":"model","prompt":"edit"}`),
			})
			if result != nil && result.Body != nil {
				defer result.Body.Close()
			}
			if len(upstream.calls) != 2 {
				t.Fatalf("upstream calls = %d, want 2: the blunt tier must re-send a %s", len(upstream.calls), tc.name)
			}
			if result == nil || result.StatusCode != http.StatusOK {
				t.Fatalf("result = %+v, want the re-send's 200", result)
			}
		})
	}
}

// TestImageFailoverBluntTierIsInertWithoutTheMainSwitch keeps the three tiers
// ordered: the blunt flag alone re-opens nothing.
func TestImageFailoverBluntTierIsInertWithoutTheMainSwitch(t *testing.T) {
	upstream := &queuedRelay{results: []*relay.Result{
		response(http.StatusGatewayTimeout, `{"error":"timeout"}`),
		response(http.StatusOK, `{"data":[]}`),
	}}
	service, _, _, _ := setupProxy(t, upstream)
	service.SetImagesFailoverIncludeUncertain(true)
	service.SetChannelRetryTimes(2)

	result := service.ChatCompletions(context.Background(), Request{
		RequestID:  "image-blunt-inert",
		Model:      "model",
		Method:     http.MethodPost,
		OpenAIPath: "images/edits",
		Body:       []byte(`{"model":"model","prompt":"edit"}`),
	})
	if result != nil && result.Body != nil {
		defer result.Body.Close()
	}
	if len(upstream.calls) != 1 {
		t.Fatalf("upstream calls = %d, want 1: the blunt tier is a sub-option, not a switch of its own", len(upstream.calls))
	}
}

// TestImageFailoverWarnedTierIsInertWithoutTheMainSwitch keeps the two tiers
// ordered: the warned flag alone must not re-open anything, because the console
// only offers it as a sub-option of image failover.
func TestImageFailoverWarnedTierIsInertWithoutTheMainSwitch(t *testing.T) {
	upstream := &queuedRelay{results: []*relay.Result{
		response(http.StatusInternalServerError, `{"error":"boom"}`),
		response(http.StatusOK, `{"data":[]}`),
	}}
	service, db, memberID, _ := setupProxy(t, upstream)
	service.SetImagesFailoverIncludeAmbiguous(true)
	addSecondKey(t, service, db, memberID, "second-secret")

	result := service.ChatCompletions(context.Background(), Request{
		RequestID:  "image-warned-tier-inert",
		Model:      "model",
		Method:     http.MethodPost,
		OpenAIPath: "images/edits",
		Body:       []byte(`{"model":"model","prompt":"edit"}`),
	})
	if result != nil && result.Body != nil {
		defer result.Body.Close()
	}
	if len(upstream.calls) != 1 {
		t.Fatalf("upstream calls = %d, want 1: the warned tier must not act alone", len(upstream.calls))
	}
}

// TestImageFailoverOptInLeavesOtherWritesAlone keeps the hatch narrow: audio,
// video and the rest of the non-idempotent families keep their own matrix,
// where only an Idempotency-Key re-opens retries.
func TestImageFailoverOptInLeavesOtherWritesAlone(t *testing.T) {
	upstream := &queuedRelay{results: []*relay.Result{
		response(http.StatusTooManyRequests, `{"error":"rate limited"}`),
		response(http.StatusOK, `{"data":[]}`),
	}}
	service, db, memberID, _ := setupProxy(t, upstream)
	service.SetImagesFailoverEnabled(true)
	addSecondKey(t, service, db, memberID, "second-secret")

	result := service.ChatCompletions(context.Background(), Request{
		RequestID:  "audio-429",
		Model:      "model",
		Method:     http.MethodPost,
		OpenAIPath: "audio/speech",
		Body:       []byte(`{"model":"model","input":"hi"}`),
	})
	if result != nil && result.Body != nil {
		defer result.Body.Close()
	}
	if len(upstream.calls) != 1 {
		t.Fatalf("upstream calls = %d, want 1: the image hatch must not cover audio", len(upstream.calls))
	}
}

// TestImageFailureMayReplayMatrix pins which answers count as "the upstream
// refused before doing any work". Getting this list wrong is invisible in
// production until a bill shows up twice.
//
// Three tiers: the conservative one replays only answers that provably preceded
// generation, the warned one (includeAmbiguous) also replays 5xx that answered,
// and the blunt one (includeUncertain) replays everything except a local
// adapter bug — it stops being an argument about safety and becomes "try again
// until a picture comes back".
func TestImageFailureMayReplayMatrix(t *testing.T) {
	cases := []struct {
		name     string
		result   *relay.Result
		want     bool
		wantWide bool
		wantAll  bool
	}{
		{"429", response(http.StatusTooManyRequests, ""), true, true, true},
		{"503", response(http.StatusServiceUnavailable, ""), true, true, true},
		{"529", response(529, ""), true, true, true},
		{"530", response(530, ""), true, true, true},
		{"400", response(http.StatusBadRequest, ""), true, true, true},
		{"401", response(http.StatusUnauthorized, ""), true, true, true},
		{"404", response(http.StatusNotFound, ""), true, true, true},
		{"415", response(http.StatusUnsupportedMediaType, ""), true, true, true},
		{"500", response(http.StatusInternalServerError, ""), false, true, true},
		{"502", response(http.StatusBadGateway, ""), false, true, true},
		{"507", response(http.StatusInsufficientStorage, ""), false, true, true},
		{"504", response(http.StatusGatewayTimeout, ""), false, false, true},
		{"408", response(http.StatusRequestTimeout, ""), false, false, true},
		{"2xx", response(http.StatusOK, `{"data":[]}`), true, true, true},
		{"2xx but empty", &relay.Result{StatusCode: http.StatusOK, Err: ErrEmptyCompletion}, false, false, true},
		{"transport error", &relay.Result{Err: context.DeadlineExceeded}, false, false, true},
		{"stream broke", &relay.Result{Err: io.ErrUnexpectedEOF}, false, false, true},
		// A local adapter failure is our bug, not an upstream answer: re-sending
		// it just burns the retry budget on every channel in turn.
		{"local adapter failure", &relay.Result{Err: adapters.ErrUnsupportedPath}, false, false, false},
		{"nil", nil, false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := imageFailureMayReplay(tc.result, false, false); got != tc.want {
				t.Fatalf("imageFailureMayReplay(%s, safe) = %v, want %v", tc.name, got, tc.want)
			}
			if got := imageFailureMayReplay(tc.result, true, false); got != tc.wantWide {
				t.Fatalf("imageFailureMayReplay(%s, includeAmbiguous) = %v, want %v", tc.name, got, tc.wantWide)
			}
			if got := imageFailureMayReplay(tc.result, false, true); got != tc.wantAll {
				t.Fatalf("imageFailureMayReplay(%s, includeUncertain) = %v, want %v", tc.name, got, tc.wantAll)
			}
		})
	}
}

// TestRetrySafeRequestMatrix pins the whole non-idempotent-write contract in
// one place. The behaviour tests above prove the proxy honours it for images; this
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

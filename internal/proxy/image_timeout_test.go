package proxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/relay"
)

// The image endpoints get their own response-header ceiling, and this pins why:
// they are slow by nature and never retried, so the chat timeout is the only
// thing that can turn a working upstream into a 502.
//
// Production, 2026-10-07: two image edits returned 502 after exactly 60.06s
// ("http2: timeout awaiting response headers") against upstreams that had served
// the same model in 55s minutes earlier. There was nothing wrong with the
// upstream — only with our patience.
func TestImageEndpointsUseTheirOwnResponseHeaderCeiling(t *testing.T) {
	// An upstream that answers later than the chat ceiling and sooner than the
	// image one: the same latency has to produce two different outcomes.
	const (
		chatCeiling  = 120 * time.Millisecond
		imageCeiling = 3 * time.Second
		upstreamLag  = 600 * time.Millisecond
	)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(upstreamLag)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[]}`)
	}))
	defer upstream.Close()

	client := func(timeout time.Duration) *http.Client {
		return &http.Client{Transport: &http.Transport{ResponseHeaderTimeout: timeout}}
	}
	service, db, highMemberID, lowMemberID := setupProxy(t, relay.NewWithClient(client(chatCeiling)))
	service.SetImageRelay(relay.NewWithClient(client(imageCeiling)))
	// Both candidates point at the same upstream, so the chat request fails on
	// its own ceiling instead of spending the test on a DNS timeout.
	pointChannelAt(t, db, channelOfMember(t, db, highMemberID), upstream.URL)
	pointChannelAt(t, db, channelOfMember(t, db, lowMemberID), upstream.URL)

	chat := service.ChatCompletions(context.Background(), Request{
		RequestID:  "image-ceiling-chat",
		Model:      "model",
		Method:     http.MethodPost,
		OpenAIPath: "chat/completions",
		Body:       []byte(`{"model":"model"}`),
	})
	if chat.Body != nil {
		defer chat.Body.Close()
	}
	// The transport-level timeout surfaces without a status, so the invariant is
	// that the chat attempt did NOT succeed inside its own ceiling.
	if chat.StatusCode == http.StatusOK {
		t.Fatalf("chat status=200: the %s ceiling did not apply to the chat client", chatCeiling)
	}

	image := service.ChatCompletions(context.Background(), Request{
		RequestID:  "image-ceiling-image",
		Model:      "model",
		Method:     http.MethodPost,
		OpenAIPath: "images/edits",
		Body:       []byte(`{"model":"model"}`),
	})
	if image.Body != nil {
		defer image.Body.Close()
	}
	if image.StatusCode != http.StatusOK {
		t.Fatalf("image status=%d, want 200: the image ceiling must outlast the chat one", image.StatusCode)
	}
}

// A deployment that never installs the second client keeps the historical
// behaviour: one client for everything.
func TestWithoutAnImageRelayEverythingUsesOneClient(t *testing.T) {
	service := &Service{}
	plain := &stubRelay{}
	service.relay = plain
	if got := service.upstreamFor("images/edits"); got != Relay(plain) {
		t.Fatal("images/edits did not fall back to the single relay")
	}
	if got := service.upstreamFor("chat/completions"); got != Relay(plain) {
		t.Fatal("chat/completions did not use the single relay")
	}

	imageRelay := &stubRelay{}
	service.SetImageRelay(imageRelay)
	if got := service.upstreamFor("images/generations"); got != Relay(imageRelay) {
		t.Fatal("images/generations did not use the image relay")
	}
	if got := service.upstreamFor("/images/edits"); got != Relay(imageRelay) {
		t.Fatal("a leading slash defeated the image relay")
	}
	if got := service.upstreamFor("chat/completions"); got != Relay(plain) {
		t.Fatal("chat/completions must keep the chat client")
	}
}

type stubRelay struct{}

func (s *stubRelay) ChatCompletionsContext(context.Context, string, string, []byte, bool) *relay.Result {
	return nil
}

func (s *stubRelay) ForwardContext(context.Context, string, string, string, []byte) *relay.Result {
	return nil
}

func (s *stubRelay) ForwardWithHeaders(context.Context, string, string, http.Header, []byte) *relay.Result {
	return nil
}

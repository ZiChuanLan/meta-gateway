package keepalive

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/proxy"
	"github.com/lan/meta-gateway/internal/store"
)

// recordingCaller stands in for the proxy's route-free transport: it keeps what
// the service sent so a test can assert the call reached the CHANNEL (not a
// route) with the body callplan built.
type recordingCaller struct {
	mu     sync.Mutex
	calls  []recordedCall
	result proxy.DirectTestResult
}

type recordedCall struct {
	channelID int64
	model     string
	body      map[string]any
}

func (c *recordingCaller) DirectChat(_ context.Context, channelID int64, model string, body []byte) proxy.DirectTestResult {
	var decoded map[string]any
	_ = json.Unmarshal(body, &decoded)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, recordedCall{channelID: channelID, model: model, body: decoded})
	return c.result
}

func (c *recordingCaller) sent() []recordedCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]recordedCall(nil), c.calls...)
}

// keepaliveServiceFixture builds one site with one enabled channel that has
// advertised two models and no route at all.
func keepaliveServiceFixture(t *testing.T, policy string) (*store.DB, *recordingCaller, int64) {
	t.Helper()
	db, err := store.OpenTest(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	siteID, err := db.Site.Create(&domain.Site{
		Name: "ka-site", BaseURL: "https://ka.example",
		Platform: "openai-compatible", Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	site, err := db.Site.GetByID(siteID)
	if err != nil {
		t.Fatal(err)
	}
	site.CallPolicy = policy
	site.KeepaliveEnabled = true
	site.KeepaliveIdleDays = 15
	site.KeepaliveSafetyMarginDays = 2
	if err := db.Site.UpdateCallPolicy(site); err != nil {
		t.Fatal(err)
	}
	credentialID, err := db.Credential.Create(&domain.Credential{
		SiteID: siteID, Kind: "api_key", SecretEnc: []byte("enc"), Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	channelID, err := db.Channel.Create(&domain.Channel{
		SiteID: &siteID, CredentialID: &credentialID, Name: "ka-channel",
		BaseURL: "https://ka.example", ModelsCSV: "first-fetched,second-fetched",
		GroupName: "default", Weight: 100, Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	caller := &recordingCaller{result: proxy.DirectTestResult{Model: "first-fetched", OK: true, StatusCode: 200}}
	return db, caller, channelID
}

// The call is dispatched to the account, not to a model the gateway serves: no
// route exists here, and one must not be needed — otherwise keeping an account
// alive would mean putting its model on offer.
func TestSendNowReachesTheChannelWithoutARoute(t *testing.T) {
	db, caller, channelID := keepaliveServiceFixture(t, domain.CallPolicyAllowProbe)
	service := NewService(db, caller, nil, nil)
	service.SetConfig(Config{DefaultIdleDays: 15})

	event, err := service.SendNow(context.Background(), channelID)
	if err != nil {
		t.Fatalf("SendNow: %v", err)
	}
	if !event.OK || event.StatusCode != 200 {
		t.Fatalf("event = %+v, want a recorded success", event)
	}
	calls := caller.sent()
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want exactly one", len(calls))
	}
	if calls[0].channelID != channelID {
		t.Errorf("channel = %d, want the pinned %d", calls[0].channelID, channelID)
	}
	// The first model the channel advertised is the one it is called on.
	if calls[0].model != "first-fetched" {
		t.Errorf("model = %q, want the channel's first fetched model", calls[0].model)
	}
	if _, ok := calls[0].body["messages"]; !ok {
		t.Errorf("body has no messages: %v", calls[0].body)
	}
	// allow_probe means the cheap shape is welcome: one token, one message.
	if got := calls[0].body["max_tokens"]; got != float64(1) {
		t.Errorf("max_tokens = %v, want the minimal form's 1", got)
	}
	if stream, ok := calls[0].body["stream"]; !ok || stream != false {
		t.Errorf("stream = %v, want false", calls[0].body["stream"])
	}

	events, err := db.Channel.RecentKeepaliveEvents(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || !events[0].OK || events[0].Model != "first-fetched" {
		t.Fatalf("footprint = %+v, want one successful call for first-fetched", events)
	}
	// The footprint is read on its own, so it has to carry the site name rather
	// than leave the operator with a bare channel name.
	if events[0].SiteName != "ka-site" {
		t.Errorf("footprint site = %q, want the site name", events[0].SiteName)
	}
}

// A site that bans probing must never receive the one-token signature on this
// path either: the shape is callplan's decision, shared with the model probe.
func TestSendNowShapesTheCallForAProbingBannedSite(t *testing.T) {
	db, caller, channelID := keepaliveServiceFixture(t, domain.CallPolicyRealCallsOnly)
	service := NewService(db, caller, nil, nil)
	service.SetConfig(Config{DefaultIdleDays: 15})

	if _, err := service.SendNow(context.Background(), channelID); err != nil {
		t.Fatalf("SendNow: %v", err)
	}
	calls := caller.sent()
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want exactly one", len(calls))
	}
	if got, ok := calls[0].body["max_tokens"].(float64); !ok || got < 64 {
		t.Errorf("max_tokens = %v, want a real-form budget", calls[0].body["max_tokens"])
	}
	messages, _ := calls[0].body["messages"].([]any)
	if len(messages) < 2 {
		t.Errorf("messages = %v, want the real form's system + user pair", calls[0].body["messages"])
	}
	if form := calls[0].body["model"]; form != "first-fetched" {
		t.Errorf("body model = %v, want the resolved model", form)
	}
}

// A failing upstream is a fact to record, not an error to return: the round must
// keep going, and the footprint is where the operator reads what happened.
func TestSendNowRecordsAnUpstreamFailure(t *testing.T) {
	db, caller, channelID := keepaliveServiceFixture(t, domain.CallPolicyAllowProbe)
	caller.result = proxy.DirectTestResult{Model: "first-fetched", StatusCode: 500, Error: "upstream status 500: boom"}
	service := NewService(db, caller, nil, nil)
	service.SetConfig(Config{DefaultIdleDays: 15})

	event, err := service.SendNow(context.Background(), channelID)
	if err != nil {
		t.Fatalf("SendNow returned %v: a failed call is recorded, not raised", err)
	}
	if event.OK {
		t.Fatalf("event = %+v, want a failure", event)
	}
	if event.Error == "" || event.StatusCode != 500 {
		t.Errorf("event = %+v, want the upstream status and its detail", event)
	}
	events, err := db.Channel.RecentKeepaliveEvents(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].OK {
		t.Fatalf("footprint = %+v, want one failed call", events)
	}
}

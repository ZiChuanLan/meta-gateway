package proxy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/relay"
	"github.com/lan/meta-gateway/internal/routing"
	"github.com/lan/meta-gateway/internal/store"
)

// capturingRelay records every upstream URL and body and replays one canned
// response, so a mapping test can assert on the exact wire request.
type capturingRelay struct {
	urls     []string
	bodies   []string
	response string
	status   int
}

func (r *capturingRelay) reply() *relay.Result {
	status := r.status
	if status == 0 {
		status = http.StatusOK
	}
	body := r.response
	if body == "" {
		body = `{"ok":true}`
	}
	return response(status, body)
}

func (r *capturingRelay) ChatCompletionsContext(_ context.Context, upstreamURL, _ string, body []byte, _ bool) *relay.Result {
	r.urls = append(r.urls, upstreamURL)
	r.bodies = append(r.bodies, string(body))
	return r.reply()
}

func (r *capturingRelay) ForwardContext(_ context.Context, _, upstreamURL, _ string, body []byte) *relay.Result {
	r.urls = append(r.urls, upstreamURL)
	r.bodies = append(r.bodies, string(body))
	return r.reply()
}

func (r *capturingRelay) ForwardWithHeaders(_ context.Context, _, upstreamURL string, _ http.Header, body []byte) *relay.Result {
	r.urls = append(r.urls, upstreamURL)
	r.bodies = append(r.bodies, string(body))
	return r.reply()
}

// mappingService builds a single-channel service whose base URL is the given
// upstream root, so a test can assert the exact URL the adapter built.
func mappingService(t *testing.T, upstream Relay, baseURL string) (*Service, *store.DB, int64) {
	t.Helper()
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	enc, err := crypto.New("upstream-map-test-key")
	if err != nil {
		t.Fatal(err)
	}
	secret, err := enc.Encrypt([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	siteID, _ := db.Site.Create(&domain.Site{Name: "site", Status: domain.StatusEnabled, BaseURL: baseURL})
	credentialID, err := db.Credential.Create(&domain.Credential{SiteID: siteID, Kind: "api_key", SecretEnc: []byte(secret), Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	channelID, err := db.Channel.Create(&domain.Channel{
		SiteID: &siteID, CredentialID: &credentialID, Name: "mapped",
		BaseURL: baseURL, Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	routeID, err := db.Route.Create(&domain.Route{ModelPattern: "model", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.RouteMember.Create(&domain.RouteMember{RouteID: routeID, ChannelID: channelID, Priority: 10, Weight: 100, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)
	selector := routing.NewWithDependencies(db.RouteMember, fixedClock{now: now}, firstRandom{})
	service := New(selector, upstream, db, enc, 0, time.Minute)
	service.now = func() time.Time { return now }
	return service, db, channelID
}

// A channel with no mapping must behave exactly as before: the adapter's own
// <base>/v1/<path> join, and the body byte-identical to the client's.
func TestUpstreamMapAbsentIsPassthrough(t *testing.T) {
	upstream := &capturingRelay{response: `{"choices":[{"message":{"content":"hi"}}]}`}
	service, _, _ := mappingService(t, upstream, "https://up.example")

	body := `{"model":"model","messages":[{"role":"user","content":"hello"}]}`
	result := service.ChatCompletions(context.Background(), Request{
		RequestID: "no-map", Model: "model", Body: []byte(body),
		Method: http.MethodPost, OpenAIPath: "chat/completions",
	})
	if result.Err != nil {
		t.Fatalf("relay: %v", result.Err)
	}
	defer result.Body.Close()
	wantURL := "https://up.example/v1/chat/completions"
	if len(upstream.urls) != 1 || upstream.urls[0] != wantURL {
		t.Fatalf("urls = %v, want [%s]", upstream.urls, wantURL)
	}
	if upstream.bodies[0] != body {
		t.Fatalf("body must be untouched, got %s", upstream.bodies[0])
	}
}

// The headline case: a provider whose API root is NOT /v1 (Zhipu's
// /api/paas/v4, Volcengine's /api/v3) no longer gains an inserted /v1 segment.
func TestUpstreamMapRemovesInsertedV1Segment(t *testing.T) {
	upstream := &capturingRelay{response: `{"data":[]}`}
	service, db, channelID := mappingService(t, upstream, "https://open.bigmodel.cn/api/paas/v4")

	channel, err := db.Channel.GetByID(channelID)
	if err != nil {
		t.Fatal(err)
	}
	// A leading slash makes the target absolute; the base keeps its own last
	// segment, which is what a provider outside /v1 wants.
	channel.UpstreamPathMap = `{"chat/completions":"/chat/completions"}`
	if err := db.Channel.Update(channel); err != nil {
		t.Fatal(err)
	}

	result := service.ChatCompletions(context.Background(), Request{
		RequestID: "strip-v1", Model: "model",
		Body:   []byte(`{"model":"model","messages":[{"role":"user","content":"hello"}]}`),
		Method: http.MethodPost, OpenAIPath: "chat/completions",
	})
	if result.Err != nil {
		t.Fatalf("relay: %v", result.Err)
	}
	defer result.Body.Close()
	// Without the mapping this would be .../api/paas/v4/v1/chat/completions,
	// which the real provider answers 404 for. A bare target (no leading slash)
	// replaces the LAST segment of the base path instead:
	// .../api/paas/v4 → .../api/paas/chat/completions.
	wantURL := "https://open.bigmodel.cn/api/paas/v4/chat/completions"
	if len(upstream.urls) != 1 || upstream.urls[0] != wantURL {
		t.Fatalf("urls = %v, want [%s]", upstream.urls, wantURL)
	}
}

// A bare (no leading slash) target is appended to the base path, exactly like
// an absolute one: the base keeps its own root, which is what a provider serving
// outside /v1 needs.
func TestUpstreamMapBareTargetKeepsBaseRoot(t *testing.T) {
	upstream := &capturingRelay{response: `{"data":[]}`}
	service, db, channelID := mappingService(t, upstream, "https://ark.cn-beijing.volces.com/api/v3")

	channel, err := db.Channel.GetByID(channelID)
	if err != nil {
		t.Fatal(err)
	}
	channel.UpstreamPathMap = `{"chat/completions":"chat/completions"}`
	if err := db.Channel.Update(channel); err != nil {
		t.Fatal(err)
	}

	result := service.ChatCompletions(context.Background(), Request{
		RequestID: "bare-target", Model: "model",
		Body:   []byte(`{"model":"model","messages":[{"role":"user","content":"hello"}]}`),
		Method: http.MethodPost, OpenAIPath: "chat/completions",
	})
	if result.Err != nil {
		t.Fatalf("relay: %v", result.Err)
	}
	defer result.Body.Close()
	// No /v1 inserted (the old behaviour was …/api/v3/v1/chat/completions).
	wantURL := "https://ark.cn-beijing.volces.com/api/v3/chat/completions"
	if len(upstream.urls) != 1 || upstream.urls[0] != wantURL {
		t.Fatalf("urls = %v, want [%s]", upstream.urls, wantURL)
	}
}

// The full non-OpenAI-upstream script: endpoint override + request mapping +
// response mapping, exercised through the real relay loop.
func TestUpstreamMapTranslatesTypeSafeShapedUpstream(t *testing.T) {
	// The upstream answers in its own protocol, not OpenAI chat.
	upstream := &capturingRelay{
		response: `{"model":"jev-1.13.0","answers":{"ask":{"type":"choice","choice":"billing","confidence":0.83}},"usage":{"input_tokens":402,"output_tokens":73}}`,
	}
	service, db, channelID := mappingService(t, upstream, "https://api.typesafe.ai")

	channel, err := db.Channel.GetByID(channelID)
	if err != nil {
		t.Fatal(err)
	}
	channel.UpstreamPathOverride = "systemone"
	channel.UpstreamRequestMap = `[
		{"from":"messages.0.content","to":"state"},
		{"to":"model","value":{"str":"jev-latest"}},
		{"to":"questions.department","value":{"str":"Which team should handle this?"}},
		{"to":"questions.department_criteria.billing","value":{"str":"Payments, invoicing"}}
	]`
	channel.UpstreamResponseMap = `[
		{"from":"answers.ask.choice","to":"choices.0.message.content","move":true},
		{"to":"choices.0.message.role","value":{"str":"assistant"}},
		{"to":"object","value":{"str":"chat.completion"}}
	]`
	if err := db.Channel.Update(channel); err != nil {
		t.Fatal(err)
	}

	result := service.ChatCompletions(context.Background(), Request{
		RequestID: "typesafe-map", Model: "model",
		Body:   []byte(`{"model":"model","messages":[{"role":"user","content":"My payouts keep failing"}]}`),
		Method: http.MethodPost, OpenAIPath: "chat/completions",
	})
	if result.Err != nil {
		t.Fatalf("relay: %v", result.Err)
	}
	defer result.Body.Close()

	// 1. Endpoint: the bare-name override keeps the conventional /v1 slot.
	if len(upstream.urls) != 1 || upstream.urls[0] != "https://api.typesafe.ai/v1/systemone" {
		t.Fatalf("urls = %v, want [https://api.typesafe.ai/v1/systemone]", upstream.urls)
	}
	// 2. Outbound body: the upstream's own envelope, built from the client chat.
	var sent map[string]any
	if err := json.Unmarshal([]byte(upstream.bodies[0]), &sent); err != nil {
		t.Fatalf("sent body is not JSON: %v (%s)", err, upstream.bodies[0])
	}
	if sent["state"] != "My payouts keep failing" {
		t.Fatalf("state = %v", sent["state"])
	}
	if sent["model"] != "jev-latest" {
		t.Fatalf("model = %v", sent["model"])
	}
	questions, ok := sent["questions"].(map[string]any)
	if !ok {
		t.Fatalf("questions = %T (%v)", sent["questions"], sent["questions"])
	}
	if questions["department"] != "Which team should handle this?" {
		t.Fatalf("questions.department = %v", questions["department"])
	}
	if criteria, ok := questions["department_criteria"].(map[string]any); !ok || criteria["billing"] != "Payments, invoicing" {
		t.Fatalf("nested question creation failed: %v", questions["department_criteria"])
	}

	// 3. Inbound body: a real OpenAI chat.completion the client can parse.
	raw, err := io.ReadAll(result.Body)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Object  string `json:"object"`
		Choices []struct {
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Answers map[string]any `json:"answers"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("response is not JSON: %v (%s)", err, raw)
	}
	if got.Object != "chat.completion" {
		t.Fatalf("object = %q", got.Object)
	}
	if len(got.Choices) != 1 {
		t.Fatalf("choices = %+v", got.Choices)
	}
	if got.Choices[0].Message.Role != "assistant" || got.Choices[0].Message.Content != "billing" {
		t.Fatalf("message = %+v", got.Choices[0].Message)
	}
	// move:true deleted the moved leaf, so the upstream envelope cannot leak that
	// value through to the client...
	if ask, ok := got.Answers["ask"].(map[string]any); ok {
		if _, exists := ask["choice"]; exists {
			t.Fatalf("moved leaf must be deleted from the source, got %s", raw)
		}
	}
	// ...while the rest of the source envelope is untouched.
	if len(got.Answers) == 0 {
		t.Fatalf("copy/move must not clear unrelated source fields, got %s", raw)
	}
}

// A malformed mapping must never fail the request: the relay forwards the
// original bytes. This is the fail-open contract.
func TestUpstreamMapMalformedFailsOpen(t *testing.T) {
	upstream := &capturingRelay{response: `{"choices":[{"message":{"content":"hi"}}]}`}
	service, db, channelID := mappingService(t, upstream, "https://up.example")

	channel, err := db.Channel.GetByID(channelID)
	if err != nil {
		t.Fatal(err)
	}
	// Written straight to the DB, bypassing the admin validation (the only way a
	// malformed value can reach the engine).
	channel.UpstreamPathMap = `{broken json`
	channel.UpstreamRequestMap = `[{"from":"a..b","to":"c"}]`
	channel.UpstreamResponseMap = `[{"from":"nonexistent","to":"x"}]`
	if err := db.Channel.Update(channel); err != nil {
		t.Fatal(err)
	}

	body := `{"model":"model","messages":[{"role":"user","content":"hello"}]}`
	result := service.ChatCompletions(context.Background(), Request{
		RequestID: "fail-open", Model: "model", Body: []byte(body),
		Method: http.MethodPost, OpenAIPath: "chat/completions",
	})
	if result.Err != nil {
		t.Fatalf("a malformed map must not fail the relay: %v", result.Err)
	}
	defer result.Body.Close()
	if len(upstream.urls) != 1 || upstream.urls[0] != "https://up.example/v1/chat/completions" {
		t.Fatalf("urls = %v (unparseable path map must be ignored)", upstream.urls)
	}
	if upstream.bodies[0] != body {
		t.Fatalf("body = %s (an unmatched map must leave the body untouched)", upstream.bodies[0])
	}
	responseBody, _ := io.ReadAll(result.Body)
	if !strings.Contains(string(responseBody), `"hi"`) {
		t.Fatalf("response = %s", responseBody)
	}
}

// The channel row must round-trip every mapping column, including through the
// admin overview projection (AGENTS 3.1: a column missing from ListOverviews
// silently round-trips as its zero value and the edit form would wipe it).
func TestChannelStoreRoundTripsUpstreamMap(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	siteID, _ := db.Site.Create(&domain.Site{Name: "site", Status: domain.StatusEnabled})
	channelID, err := db.Channel.Create(&domain.Channel{
		SiteID: &siteID, Name: "mapped", BaseURL: "https://up.example", Status: domain.StatusEnabled,
		UpstreamPathOverride: "systemone",
		UpstreamPathMap:      `{"chat/completions":"chat/completions"}`,
		UpstreamRequestMap:   `[{"from":"messages.0.content","to":"state"}]`,
		UpstreamResponseMap:  `[{"from":"answers.a","to":"choices.0.message.content"}]`,
	})
	if err != nil {
		t.Fatal(err)
	}

	assertMapped := func(label string, channel domain.Channel) {
		t.Helper()
		if channel.UpstreamPathOverride != "systemone" {
			t.Fatalf("%s: override = %q", label, channel.UpstreamPathOverride)
		}
		if channel.UpstreamPathMap != `{"chat/completions":"chat/completions"}` {
			t.Fatalf("%s: path map = %q", label, channel.UpstreamPathMap)
		}
		if channel.UpstreamRequestMap != `[{"from":"messages.0.content","to":"state"}]` {
			t.Fatalf("%s: request map = %q", label, channel.UpstreamRequestMap)
		}
		if channel.UpstreamResponseMap != `[{"from":"answers.a","to":"choices.0.message.content"}]` {
			t.Fatalf("%s: response map = %q", label, channel.UpstreamResponseMap)
		}
	}

	fetched, err := db.Channel.GetByID(channelID)
	if err != nil || fetched == nil {
		t.Fatalf("GetByID: %v %v", fetched, err)
	}
	assertMapped("GetByID", *fetched)

	all, err := db.Channel.List()
	if err != nil {
		t.Fatal(err)
	}
	assertMapped("List", all[0])

	enabled, err := db.Channel.ListEnabled()
	if err != nil {
		t.Fatal(err)
	}
	assertMapped("ListEnabled", enabled[0])

	probeable, err := db.Channel.ListProbeable()
	if err != nil {
		t.Fatal(err)
	}
	assertMapped("ListProbeable", probeable[0])

	overviews, err := db.Channel.ListOverviews(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(overviews) != 1 {
		t.Fatalf("overviews = %d", len(overviews))
	}
	assertMapped("ListOverviews", overviews[0].Channel)

	// Update must persist them too.
	updated := *fetched
	updated.UpstreamPathOverride = ""
	updated.UpstreamPathMap = ""
	updated.UpstreamRequestMap = ""
	updated.UpstreamResponseMap = ""
	if err := db.Channel.Update(&updated); err != nil {
		t.Fatal(err)
	}
	cleared, err := db.Channel.GetByID(channelID)
	if err != nil {
		t.Fatal(err)
	}
	if cleared.UpstreamPathOverride != "" || cleared.UpstreamPathMap != "" ||
		cleared.UpstreamRequestMap != "" || cleared.UpstreamResponseMap != "" {
		t.Fatalf("clearing did not persist: %+v", cleared)
	}
}

// A client asking for `max` used to reach System One verbatim and come back as
// `400 field ReasoningEffort invalid, should be one of: low, medium, high,
// xhigh, none` (the API's own words, 2026-09-25). The provider's rung set is
// known from the resolved endpoint, so the request is rewritten before it
// leaves — and the attempt log records the rewrite.
func TestForwardClampsReasoningEffortToProviderVocabulary(t *testing.T) {
	upstream := &capturingRelay{
		response: `{"model":"jev-1.13.0","answers":{"ask":{"type":"choice","choice":"billing"}},"usage":{"input_tokens":12,"output_tokens":3}}`,
	}
	// No type hint: the endpoint is the only thing naming the protocol, which is
	// exactly how a channel hand-pointed at `…/v1/systemone` looks.
	service, db, channelID := mappingService(t, upstream, "https://api.typesafe.ai")

	channel, err := db.Channel.GetByID(channelID)
	if err != nil {
		t.Fatal(err)
	}
	channel.UpstreamPathOverride = "systemone"
	channel.UpstreamRequestMap = `[
		{"from":"messages.0.content","to":"state"},
		{"to":"model","value":{"str":"jev-latest"}}
	]`
	if err := db.Channel.Update(channel); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ sent, want string }{
		{"max", "xhigh"},
		{"minimal", "none"},
		{"high", "high"},
	} {
		t.Run(tc.sent, func(t *testing.T) {
			result := service.ChatCompletions(context.Background(), Request{
				RequestID: "typesafe-reasoning-" + tc.sent, Model: "model",
				Body:   []byte(`{"model":"model","reasoning_effort":"` + tc.sent + `","messages":[{"role":"user","content":"hi"}]}`),
				Method: http.MethodPost, OpenAIPath: "chat/completions",
			})
			if result.Err != nil {
				t.Fatalf("relay: %v", result.Err)
			}
			defer result.Body.Close()
			if len(upstream.bodies) == 0 {
				t.Fatal("upstream saw no request")
			}
			var sent map[string]any
			if err := json.Unmarshal([]byte(upstream.bodies[len(upstream.bodies)-1]), &sent); err != nil {
				t.Fatalf("sent body is not JSON: %v (%s)", err, upstream.bodies[len(upstream.bodies)-1])
			}
			if sent["reasoning_effort"] != tc.want {
				t.Fatalf("reasoning_effort = %v, want %v (body %s)", sent["reasoning_effort"], tc.want, upstream.bodies[len(upstream.bodies)-1])
			}
		})
	}
}

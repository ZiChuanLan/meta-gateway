package proxy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/lan/meta-gateway/internal/adapters"
	"github.com/lan/meta-gateway/internal/relay"
)

// responsesBody is a minimal Responses API request document.
const responsesBody = `{"model":"model","instructions":"sys","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]}`

// TestResponsesNativePassthrough verifies an OpenAI-compatible upstream that
// DOES expose /v1/responses receives the native body untouched (no
// translation) — existing behavior preserved.
func TestResponsesNativePassthrough(t *testing.T) {
	upstream := &queuedRelay{results: []*relay.Result{response(http.StatusOK, `{"object":"response","status":"completed","output":[]}`)}}
	service, db, highMemberID, _ := setupProxy(t, upstream)
	service.SetAdapterRegistry(adapters.NewRegistry(nil))

	if _, err := db.RouteMember.GetByID(highMemberID); err != nil {
		t.Fatal(err)
	}
	req := Request{
		RequestID:          "resp-native",
		Model:              "model",
		Body:               []byte(responsesBody),
		Stream:             false,
		Method:             http.MethodPost,
		OpenAIPath:         "responses",
		DownstreamProtocol: "responses",
	}
	result, meta := service.ForwardWithMeta(context.Background(), req)
	if result == nil || result.Err != nil {
		t.Fatalf("result=%+v err=%v", result, result.Err)
	}
	if meta == nil || meta.MemberID != highMemberID {
		t.Fatalf("meta=%+v", meta)
	}
	if len(upstream.calls) != 1 || !strings.HasSuffix(upstream.calls[0], "/responses") {
		t.Fatalf("native passthrough must hit /responses, calls=%v", upstream.calls)
	}
	if len(upstream.bodies) != 1 || strings.TrimSpace(string(upstream.bodies[0])) != strings.TrimSpace(responsesBody) {
		t.Fatalf("native body must pass verbatim, got %s", upstream.bodies[0])
	}
	// Body return must be the Responses contract, not a chat completion.
	var probe struct {
		Object string `json:"object"`
	}
	if err := json.Unmarshal(bodyBytes(t, result), &probe); err != nil {
		t.Fatalf("response body: %v", err)
	}
	if probe.Object != "response" {
		t.Fatalf("passthrough must keep the Responses contract, got object=%q", probe.Object)
	}
}

// TestResponsesFallbackTranslatesOn404 verifies the in-place fallback: the
// upstream 404s on /v1/responses, so the SAME channel is replayed with the
// request pivoted to chat/completions and the chat answer converts back to
// the Responses contract.
func TestResponsesFallbackTranslatesOn404(t *testing.T) {
	chatAnswer := `{"id":"chatcmpl-1","object":"chat.completion","created":1,"model":"model",` +
		`"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"pivoted answer"}}],` +
		`"usage":{"prompt_tokens":7,"completion_tokens":2,"total_tokens":9}}`
	upstream := &queuedRelay{results: []*relay.Result{
		response(http.StatusNotFound, `{"error":{"message":"not found"}}`),
		response(http.StatusOK, chatAnswer),
	}}
	service, db, highMemberID, _ := setupProxy(t, upstream)
	service.SetAdapterRegistry(adapters.NewRegistry(nil))

	if _, err := db.RouteMember.GetByID(highMemberID); err != nil {
		t.Fatal(err)
	}
	req := Request{
		RequestID:          "resp-fallback",
		Model:              "model",
		Body:               []byte(responsesBody),
		Stream:             false,
		Method:             http.MethodPost,
		OpenAIPath:         "responses",
		DownstreamProtocol: "responses",
	}
	result, meta := service.ForwardWithMeta(context.Background(), req)
	if result == nil || result.Err != nil {
		t.Fatalf("result=%+v err=%v", result, result.Err)
	}
	if meta == nil || meta.MemberID != highMemberID {
		t.Fatalf("fallback must stay on the same channel, meta=%+v", meta)
	}
	// Two upstream calls: native 404, then translated chat/completions.
	if len(upstream.calls) != 2 {
		t.Fatalf("expected native + translated calls, got %v", upstream.calls)
	}
	if !strings.HasSuffix(upstream.calls[1], "/chat/completions") {
		t.Fatalf("fallback must hit /chat/completions, calls=%v", upstream.calls)
	}
	var chat struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(upstream.bodies[1], &chat); err != nil {
		t.Fatalf("fallback body not chat JSON: %v (%s)", err, upstream.bodies[1])
	}
	if len(chat.Messages) != 2 || chat.Messages[0].Role != "system" || chat.Messages[1].Content != "hi" {
		t.Fatalf("fallback chat body wrong: %+v", chat.Messages)
	}
	// The client receives the Responses contract.
	var doc struct {
		Object string `json:"object"`
		Status string `json:"status"`
		Output []struct {
			Type    string `json:"type"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(bodyBytes(t, result), &doc); err != nil {
		t.Fatalf("converted body: %v", err)
	}
	if doc.Object != "response" || doc.Status != "completed" || len(doc.Output) != 1 || doc.Output[0].Content[0].Text != "pivoted answer" {
		t.Fatalf("responses contract wrong: %+v", doc)
	}
	if doc.Usage.InputTokens != 7 || doc.Usage.OutputTokens != 2 {
		t.Fatalf("usage not mapped: %+v", doc.Usage)
	}
}

// TestResponsesFallbackOnceOnly guards the fallback against loops: a 404 on
// the translated attempt is a channel failure, not another fallback.
func TestResponsesFallbackOnceOnly(t *testing.T) {
	upstream := &queuedRelay{results: []*relay.Result{
		response(http.StatusNotFound, `{"error":{"message":"no responses"}}`),
		response(http.StatusNotFound, `{"error":{"message":"no chat either"}}`),
	}}
	service, _, _, _ := setupProxy(t, upstream)
	service.SetAdapterRegistry(adapters.NewRegistry(nil))
	service.SetCrossChannelFailoverEnabled(false)
	service.SetRetryPolicy(0, 0)

	req := Request{
		RequestID:          "resp-fallback-once",
		Model:              "model",
		Body:               []byte(responsesBody),
		Stream:             false,
		Method:             http.MethodPost,
		OpenAIPath:         "responses",
		DownstreamProtocol: "responses",
	}
	result, _ := service.ForwardWithMeta(context.Background(), req)
	if len(upstream.calls) != 2 {
		t.Fatalf("expected exactly 2 calls (native + one fallback), got %d %v", len(upstream.calls), upstream.calls)
	}
	if result == nil || result.Err != nil || result.StatusCode != http.StatusNotFound {
		t.Fatalf("the translated 404 must surface, result=%+v", result)
	}
}

// TestResponsesStreamFallbackReshapes exercises the stream reshape through
// the fallback path via the proxy-level stream wrapper of the matrix pair.
func TestResponsesStreamFallbackReshapes(t *testing.T) {
	chatStream := "data: {\"id\":\"c\",\"model\":\"model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"}}]}\n\n" +
		"data: {\"id\":\"c\",\"model\":\"model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"streamed\"}}]}\n\n" +
		"data: {\"id\":\"c\",\"model\":\"model\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: {\"id\":\"c\",\"model\":\"model\",\"choices\":[],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1,\"total_tokens\":3}}\n\n" +
		"data: [DONE]\n\n"
	upstream := &queuedRelay{results: []*relay.Result{
		response(http.StatusNotFound, `{"error":{"message":"no responses"}}`),
		{StatusCode: http.StatusOK, Body: nopCloser(strings.NewReader(chatStream)), Header: make(http.Header), LatencyMs: 2},
	}}
	service, db, highMemberID, _ := setupProxy(t, upstream)
	service.SetAdapterRegistry(adapters.NewRegistry(nil))

	if _, err := db.RouteMember.GetByID(highMemberID); err != nil {
		t.Fatal(err)
	}
	streamReq := `{"model":"model","stream":true,"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]}`
	req := Request{
		RequestID:          "resp-stream",
		Model:              "model",
		Body:               []byte(streamReq),
		Stream:             true,
		Method:             http.MethodPost,
		OpenAIPath:         "responses",
		DownstreamProtocol: "responses",
	}
	result, meta := service.ForwardWithMeta(context.Background(), req)
	if result == nil || result.Err != nil {
		t.Fatalf("result err=%v", result.Err)
	}
	if meta == nil || meta.MemberID != highMemberID {
		t.Fatalf("meta=%+v", meta)
	}
	body := bodyBytes(t, result)
	for _, want := range []string{"event: response.created", "event: response.output_text.delta", "\"delta\":\"streamed\"", "event: response.completed", "\"input_tokens\":2"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("stream reshape missing %q in:\n%s", want, body)
		}
	}
}

func bodyBytes(t *testing.T, result *relay.Result) []byte {
	t.Helper()
	if result == nil || result.Body == nil {
		t.Fatal("missing response body")
	}
	raw, err := io.ReadAll(result.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	_ = result.Body.Close()
	return raw
}

type nopCloserReader struct{ r io.Reader }

func (n nopCloserReader) Read(p []byte) (int, error) { return n.r.Read(p) }
func (n nopCloserReader) Close() error               { return nil }

func nopCloser(r io.Reader) io.ReadCloser { return nopCloserReader{r} }

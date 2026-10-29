package proxy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/lan/meta-gateway/internal/adapters"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/relay"
)

const compatibilityToolRequest = `{"model":"model","messages":[{"role":"user","content":"Look up Paris"}],"stream":true,"stream_options":{"include_usage":true},"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}}],"tool_choice":{"type":"function","function":{"name":"lookup"}}}`

// Argument fragments, the usage-only terminal chunk, and DONE must survive
// the complete routing/adapter/HTTP relay path, not just an SSE helper.
const compatibilityToolStream = "data: " + `{"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"city\":"}}]}}]}` + "\n\n" +
	"data: " + `{"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"Paris\"}"}}]},"finish_reason":"tool_calls"}]}` + "\n\n" +
	"data: " + `{"id":"c1","choices":[],"usage":{"prompt_tokens":4,"completion_tokens":6,"total_tokens":10}}` + "\n\n" +
	"data: [DONE]\n\n"

func checkCompatibilityRequest(t *testing.T, r *http.Request, wantBody, wantKey string) {
	t.Helper()
	if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
		t.Errorf("request = %s %s", r.Method, r.URL.Path)
	}
	if r.Header.Get("Authorization") != "Bearer "+wantKey {
		t.Error("request did not use the selected channel credential")
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		t.Errorf("content type = %q", r.Header.Get("Content-Type"))
	}
	var got, want any
	if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
		t.Error(err)
		return
	}
	if err := json.Unmarshal([]byte(wantBody), &want); err != nil {
		t.Error(err)
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("request semantics changed: got %#v, want %#v", got, want)
	}
}

func checkCompatibilityResponse(t *testing.T, result *relay.Result, status int, contentType, body string) {
	t.Helper()
	if result.Body == nil {
		t.Fatalf("missing response body: status=%d err=%v", result.StatusCode, result.Err)
	}
	defer result.Body.Close()
	got, err := io.ReadAll(result.Body)
	if err != nil {
		t.Fatal(err)
	}
	if result.Err != nil || result.StatusCode != status {
		t.Fatalf("status=%d err=%v, want %d", result.StatusCode, result.Err, status)
	}
	if !strings.HasPrefix(result.Header.Get("Content-Type"), contentType) {
		t.Errorf("response content type = %q", result.Header.Get("Content-Type"))
	}
	if string(got) != body {
		t.Errorf("response changed: got %q, want %q", got, body)
	}
}

func TestOpenAICompatibleHTTPRoundTrip(t *testing.T) {
	cases := []struct {
		name, request, response, contentType string
		stream                               bool
	}{
		{"text stream", `{"model":"model","messages":[{"role":"user","content":"Hi"}],"stream":true}`, "data: " + `{"choices":[{"delta":{"content":"Hello"},"finish_reason":"stop"}]}` + "\n\ndata: [DONE]\n\n", "text/event-stream", true},
		{"tool stream with terminal usage", compatibilityToolRequest, compatibilityToolStream, "text/event-stream", true},
		{"tool result continuation", `{"model":"model","messages":[{"role":"user","content":"Look up Paris"},{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"city\":\"Paris\"}"}}]},{"role":"tool","tool_call_id":"call_1","content":"Sunny"}],"stream":false}`, `{"id":"c2","choices":[{"message":{"role":"assistant","content":"Sunny in Paris"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":3,"total_tokens":13}}`, "application/json", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			seen := make(chan struct{}, 4)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer func() { seen <- struct{}{} }()
				checkCompatibilityRequest(t, r, tc.request, "secret")
				w.Header().Set("Content-Type", tc.contentType)
				// Flush each event to exercise the stream pre-read and replay path.
				for _, part := range strings.SplitAfter(tc.response, "\n\n") {
					_, _ = io.WriteString(w, part)
					w.(http.Flusher).Flush()
				}
			}))
			t.Cleanup(server.Close)
			service, db, high, _ := setupProxy(t, relay.NewWithClient(server.Client()))
			service.SetAdapterRegistry(adapters.NewRegistry(nil))
			pointChannelAt(t, db, channelOfMember(t, db, high), server.URL+"/v1")
			result := service.ChatCompletions(context.Background(), Request{
				RequestID: "compat-roundtrip", Model: "model", Body: []byte(tc.request), Stream: tc.stream,
				PreferMemberID: high,
			})
			checkCompatibilityResponse(t, result, http.StatusOK, tc.contentType, tc.response)
			<-seen
			if len(seen) != 0 {
				t.Fatal("successful request was replayed")
			}
		})
	}
}

func TestOpenAICompatibleHTTPFailover(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		for _, failover := range []bool{false, true} {
			name := http.StatusText(status)
			if failover {
				name += "/backup"
			} else {
				name += "/no-backup"
			}
			t.Run(name, func(t *testing.T) {
				seen := make(chan string, 8)
				errorBody := `{"error":{"message":"upstream unavailable","type":"compatibility_fixture"}}`
				primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					checkCompatibilityRequest(t, r, compatibilityToolRequest, "secret")
					seen <- "primary"
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(status)
					_, _ = io.WriteString(w, errorBody)
				}))
				t.Cleanup(primary.Close)
				backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					checkCompatibilityRequest(t, r, compatibilityToolRequest, "backup-placeholder")
					seen <- "backup"
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, compatibilityToolStream)
				}))
				t.Cleanup(backup.Close)
				service, db, high, low := setupProxy(t, relay.NewWithClient(primary.Client()))
				service.SetAdapterRegistry(adapters.NewRegistry(nil))
				service.SetChannelRetryTimes(0)
				service.SetCrossChannelFailoverEnabled(failover)
				pointChannelAt(t, db, channelOfMember(t, db, high), primary.URL+"/v1")
				channel, err := db.Channel.GetByID(channelOfMember(t, db, low))
				if err != nil {
					t.Fatal(err)
				}
				site, err := db.Site.Create(&domain.Site{Name: "backup-site", Status: domain.StatusEnabled})
				if err != nil {
					t.Fatal(err)
				}
				secret, err := service.enc.Encrypt([]byte("backup-placeholder"))
				if err != nil {
					t.Fatal(err)
				}
				credential, err := db.Credential.Create(&domain.Credential{SiteID: site, Kind: "api_key", SecretEnc: []byte(secret), Status: domain.StatusEnabled})
				if err != nil {
					t.Fatal(err)
				}
				channel.BaseURL, channel.SiteID, channel.CredentialID = backup.URL+"/v1", &site, &credential
				if err := db.Channel.Update(channel); err != nil {
					t.Fatal(err)
				}
				result := service.ChatCompletions(context.Background(), Request{
					RequestID: "compat-failover", Model: "model", Body: []byte(compatibilityToolRequest), Stream: true,
				})
				if failover {
					checkCompatibilityResponse(t, result, http.StatusOK, "text/event-stream", compatibilityToolStream)
				} else {
					checkCompatibilityResponse(t, result, status, "application/json", errorBody)
				}
				if got := <-seen; got != "primary" {
					t.Fatalf("first attempt=%q", got)
				}
				if failover {
					if got := <-seen; got != "backup" {
						t.Fatalf("second attempt=%q", got)
					}
				}
				if len(seen) != 0 {
					t.Fatalf("unexpected additional attempts: %d", len(seen))
				}
			})
		}
	}
}

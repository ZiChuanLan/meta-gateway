package proxy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/relay"
	"github.com/lan/meta-gateway/internal/store"
)

// recordingUpstream captures the single upstream call a direct test makes.
type recordingUpstream struct {
	path          string
	authorization string
	body          map[string]any
}

func newRecordingUpstream(t *testing.T, status int, payload string) (*recordingUpstream, *httptest.Server) {
	t.Helper()
	seen := &recordingUpstream{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.path = r.URL.Path
		seen.authorization = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &seen.body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, payload)
	}))
	t.Cleanup(server.Close)
	return seen, server
}

// TestDirectChatTestReachesModelWithNoRoute is the reason this path exists:
// the model is advertised by the channel but has no route, so route-based
// probing could never reach it — and that is exactly the state every candidate
// is in while an operator is still deciding whether to adopt it.
func TestDirectChatTestReachesModelWithNoRoute(t *testing.T) {
	seen, server := newRecordingUpstream(t, http.StatusOK, `{"id":"chatcmpl-1","usage":{"total_tokens":3}}`)
	service, db, highMemberID, _ := setupProxy(t, relay.NewWithClient(server.Client()))

	channelID := channelOfMember(t, db, highMemberID)
	pointChannelAt(t, db, channelID, server.URL)

	// Guard the premise: nothing has ever routed this model.
	if routes, err := db.RouteMember.ListRouteOverviews(); err != nil {
		t.Fatal(err)
	} else {
		for _, overview := range routes {
			if overview.Route.ModelPattern == "candidate-model" {
				t.Fatalf("fixture already routes candidate-model; the test would prove nothing")
			}
		}
	}

	result := service.DirectChatTest(context.Background(), channelID, "candidate-model", "ping", 1)

	if !result.OK {
		t.Fatalf("direct test failed: %+v", result)
	}
	if result.StatusCode != http.StatusOK {
		t.Fatalf("status=%d want 200", result.StatusCode)
	}
	if result.LatencyMs < 0 {
		t.Fatalf("latency=%d", result.LatencyMs)
	}
	if !strings.HasSuffix(seen.path, "/v1/chat/completions") {
		t.Fatalf("upstream path=%q want the OpenAI chat path", seen.path)
	}
	if seen.authorization != "Bearer secret" {
		t.Fatalf("authorization=%q want the channel credential", seen.authorization)
	}
	if got, _ := seen.body["model"].(string); got != "candidate-model" {
		t.Fatalf("upstream model=%q want candidate-model", got)
	}
	if got, _ := seen.body["messages"].([]any); len(got) != 1 {
		t.Fatalf("upstream messages=%v want exactly the one probe turn", seen.body["messages"])
	}
	if stream, ok := seen.body["stream"].(bool); !ok || stream {
		t.Fatalf("upstream stream=%v want false", seen.body["stream"])
	}
}

// TestDirectChatTestLeavesRoutingStateUntouched pins the contract that makes a
// connection-page check safe: an operator poking at a half-configured channel
// must not be able to cool a member down, block a model name, or write health.
func TestDirectChatTestLeavesRoutingStateUntouched(t *testing.T) {
	_, server := newRecordingUpstream(t, http.StatusNotFound, `{"error":{"message":"model not found"}}`)
	service, db, highMemberID, _ := setupProxy(t, relay.NewWithClient(server.Client()))

	channelID := channelOfMember(t, db, highMemberID)
	pointChannelAt(t, db, channelID, server.URL)

	result := service.DirectChatTest(context.Background(), channelID, "candidate-model", "", 0)
	if result.OK {
		t.Fatalf("a 404 upstream must not read as ok: %+v", result)
	}

	health, err := db.ListModelHealth()
	if err != nil {
		t.Fatal(err)
	}
	if len(health) != 0 {
		t.Fatalf("direct test wrote %d model_health rows, want none", len(health))
	}
	blocked, err := db.IsModelBlocked(channelID, "candidate-model")
	if err != nil {
		t.Fatal(err)
	}
	if blocked {
		t.Fatalf("direct test blacklisted the model name; a synthetic 404 is information, not a fault")
	}
	member, err := db.RouteMember.GetByID(highMemberID)
	if err != nil {
		t.Fatal(err)
	}
	if !member.Enabled {
		t.Fatalf("direct test disabled a route member")
	}
}

func TestDirectChatTestReportsUpstreamStatus(t *testing.T) {
	_, server := newRecordingUpstream(t, http.StatusUnauthorized, `{"error":{"message":"invalid api key"}}`)
	service, db, highMemberID, _ := setupProxy(t, relay.NewWithClient(server.Client()))
	channelID := channelOfMember(t, db, highMemberID)
	pointChannelAt(t, db, channelID, server.URL)

	result := service.DirectChatTest(context.Background(), channelID, "candidate-model", "", 0)

	if result.OK {
		t.Fatalf("401 must not read as ok: %+v", result)
	}
	if result.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401", result.StatusCode)
	}
	if !strings.Contains(result.Error, "upstream status 401") {
		t.Fatalf("error=%q want the upstream status", result.Error)
	}
	// The excerpt is what turns "401" into a five-second fix, so it must survive.
	if !strings.Contains(result.Error, "invalid api key") {
		t.Fatalf("error=%q want the upstream body excerpt", result.Error)
	}
}

func TestDirectChatTestRejectsUntestableTargets(t *testing.T) {
	_, server := newRecordingUpstream(t, http.StatusOK, `{}`)
	service, db, highMemberID, _ := setupProxy(t, relay.NewWithClient(server.Client()))
	channelID := channelOfMember(t, db, highMemberID)
	pointChannelAt(t, db, channelID, server.URL)

	t.Run("unknown channel", func(t *testing.T) {
		result := service.DirectChatTest(context.Background(), 987654, "m", "", 0)
		if result.OK || result.Error != "channel not found" {
			t.Fatalf("result=%+v want channel not found", result)
		}
	})

	t.Run("missing channel id", func(t *testing.T) {
		result := service.DirectChatTest(context.Background(), 0, "m", "", 0)
		if result.OK || result.Error != "channel is required" {
			t.Fatalf("result=%+v want channel is required", result)
		}
	})

	t.Run("missing model", func(t *testing.T) {
		result := service.DirectChatTest(context.Background(), channelID, "  ", "", 0)
		if result.OK || result.Error != "model is required" {
			t.Fatalf("result=%+v want model is required", result)
		}
	})

	t.Run("disabled channel", func(t *testing.T) {
		channel, err := db.Channel.GetByID(channelID)
		if err != nil {
			t.Fatal(err)
		}
		channel.Status = domain.StatusDisabled
		if err := db.Channel.Update(channel); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			channel.Status = domain.StatusEnabled
			_ = db.Channel.Update(channel)
		})
		result := service.DirectChatTest(context.Background(), channelID, "candidate-model", "", 0)
		if result.OK || result.Error != "channel is disabled" {
			t.Fatalf("result=%+v want channel is disabled", result)
		}
	})

	t.Run("no usable credential", func(t *testing.T) {
		emptySite, err := db.Site.Create(&domain.Site{Name: "empty-site", Status: domain.StatusEnabled})
		if err != nil {
			t.Fatal(err)
		}
		bare, err := db.Channel.Create(&domain.Channel{
			SiteID:  &emptySite,
			Name:    "bare",
			BaseURL: server.URL,
			Status:  domain.StatusEnabled,
		})
		if err != nil {
			t.Fatal(err)
		}
		result := service.DirectChatTest(context.Background(), bare, "candidate-model", "", 0)
		if result.OK || !strings.Contains(result.Error, "credential") {
			t.Fatalf("result=%+v want a credential error", result)
		}
	})
}

func channelOfMember(t *testing.T, db *store.DB, memberID int64) int64 {
	t.Helper()
	member, err := db.RouteMember.GetByID(memberID)
	if err != nil {
		t.Fatal(err)
	}
	return member.ChannelID
}

func pointChannelAt(t *testing.T, db *store.DB, channelID int64, baseURL string) {
	t.Helper()
	channel, err := db.Channel.GetByID(channelID)
	if err != nil {
		t.Fatal(err)
	}
	channel.BaseURL = baseURL
	if err := db.Channel.Update(channel); err != nil {
		t.Fatal(err)
	}
}

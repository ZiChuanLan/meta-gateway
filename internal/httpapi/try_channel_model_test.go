package httpapi_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// setupServer creates exactly one channel, so its id is 1. The other admin
// tests rely on that too (see the discovery probe cases).
const fixtureChannelID = 1

// TestTryChannelModelChecksModelOutsideRouting is the console workflow this
// endpoint exists for: the operator is looking at a channel whose models are
// all candidates, none adopted, and wants to know which ones answer before
// committing any of them to a route.
func TestTryChannelModelChecksModelOutsideRouting(t *testing.T) {
	var seenPath, seenModel string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		var body struct {
			Model string `json:"model"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		seenModel = body.Model
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"cmpl-1","usage":{"total_tokens":2}}`)
	}))
	defer upstream.Close()

	base, _, db := setupServer(t, upstream.URL)

	// Premise guard: the model under test is on no route, so a route-based
	// probe could not have reached it.
	overviews, err := db.RouteMember.ListRouteOverviews()
	if err != nil {
		t.Fatal(err)
	}
	for _, overview := range overviews {
		if overview.Route.ModelPattern == "candidate-model" {
			t.Fatal("fixture routes candidate-model; the test would prove nothing")
		}
	}

	status, body, _ := adminCall(t, base, http.MethodPost, "/admin/try/channel-model", map[string]any{
		"channel_id": fixtureChannelID,
		"model":      "candidate-model",
		"max_tokens": 1,
	})

	if status != http.StatusOK {
		t.Fatalf("status=%d body=%v", status, body)
	}
	if ok, _ := body["ok"].(bool); !ok {
		t.Fatalf("body=%v want ok=true", body)
	}
	if code, _ := body["status_code"].(float64); int(code) != http.StatusOK {
		t.Fatalf("status_code=%v want 200", body["status_code"])
	}
	if seenModel != "candidate-model" {
		t.Fatalf("upstream saw model=%q", seenModel)
	}
	if !strings.HasSuffix(seenPath, "/v1/chat/completions") {
		t.Fatalf("upstream path=%q", seenPath)
	}
}

// A refused model is the answer, not an API failure: the console renders it as
// a row, so it must not arrive as a failed fetch.
func TestTryChannelModelReportsRefusalAsAResult(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":{"message":"no such model"}}`)
	}))
	defer upstream.Close()

	base, _, _ := setupServer(t, upstream.URL)

	status, body, _ := adminCall(t, base, http.MethodPost, "/admin/try/channel-model", map[string]any{
		"channel_id": fixtureChannelID,
		"model":      "candidate-model",
	})

	if status != http.StatusOK {
		t.Fatalf("status=%d body=%v want 200 carrying a failed verdict", status, body)
	}
	if ok, _ := body["ok"].(bool); ok {
		t.Fatalf("body=%v want ok=false", body)
	}
	if code, _ := body["status_code"].(float64); int(code) != http.StatusNotFound {
		t.Fatalf("status_code=%v want 404", body["status_code"])
	}
	if message, _ := body["error"].(string); !strings.Contains(message, "no such model") {
		t.Fatalf("error=%q want the upstream message", message)
	}
}

func TestTryChannelModelValidatesRequest(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	}))
	defer upstream.Close()

	base, _, _ := setupServer(t, upstream.URL)

	cases := []struct {
		name string
		body map[string]any
		want string
	}{
		{"missing channel", map[string]any{"model": "m"}, "channel_id is required"},
		{"missing model", map[string]any{"channel_id": fixtureChannelID}, "model is required"},
		{"blank model", map[string]any{"channel_id": fixtureChannelID, "model": "   "}, "model is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, body, _ := adminCall(t, base, http.MethodPost, "/admin/try/channel-model", tc.body)
			if status != http.StatusBadRequest {
				t.Fatalf("status=%d body=%v want 400", status, body)
			}
			if message, _ := body["error"].(string); !strings.Contains(message, tc.want) {
				t.Fatalf("error=%q want %q", message, tc.want)
			}
		})
	}

	t.Run("requires admin auth", func(t *testing.T) {
		raw, _ := json.Marshal(map[string]any{"channel_id": fixtureChannelID, "model": "m"})
		resp, err := http.Post(base+"/admin/try/channel-model", "application/json", strings.NewReader(string(raw)))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status=%d want 401", resp.StatusCode)
		}
	})
}

// The connection drawer is a diagnostic surface: exercising it must leave the
// routing tables exactly as they were.
func TestTryChannelModelLeavesRoutingUntouched(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"bad request"}}`)
	}))
	defer upstream.Close()

	base, _, db := setupServer(t, upstream.URL)

	before, err := db.RouteMember.ListRouteOverviews()
	if err != nil {
		t.Fatal(err)
	}

	status, _, _ := adminCall(t, base, http.MethodPost, "/admin/try/channel-model", map[string]any{
		"channel_id": fixtureChannelID,
		"model":      "candidate-model",
	})
	if status != http.StatusOK {
		t.Fatalf("status=%d", status)
	}

	after, err := db.RouteMember.ListRouteOverviews()
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("route count changed: %d -> %d", len(before), len(after))
	}
	for index := range before {
		was, now := before[index].Members, after[index].Members
		if len(was) != len(now) {
			t.Fatalf("member count changed on %s", before[index].Route.ModelPattern)
		}
		for member := range was {
			if was[member].Member.Enabled != now[member].Member.Enabled {
				t.Fatalf("member enabled flag changed on %s", before[index].Route.ModelPattern)
			}
			if (was[member].Member.CooldownUntil == nil) != (now[member].Member.CooldownUntil == nil) {
				t.Fatalf("member cooldown changed on %s", before[index].Route.ModelPattern)
			}
		}
	}

	health, err := db.ListModelHealth()
	if err != nil {
		t.Fatal(err)
	}
	if len(health) != 0 {
		t.Fatalf("health rows=%d want none", len(health))
	}

	blocked, err := db.IsModelBlocked(fixtureChannelID, "candidate-model")
	if err != nil {
		t.Fatal(err)
	}
	if blocked {
		t.Fatal("model name was blacklisted by a synthetic check")
	}
}

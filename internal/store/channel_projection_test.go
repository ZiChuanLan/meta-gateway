package store_test

import (
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

// channelProjectionFixture builds a route with one member whose channel has every
// per-channel override set to a value distinguishable from every other column.
// The whole point of the values is that *any* off-by-one between the SELECT list
// and the Scan list produces a visibly wrong field rather than a plausible zero.
func channelProjectionFixture(t *testing.T, db *store.DB) (routeID, channelID int64) {
	t.Helper()
	now := time.Now()
	siteID, err := db.Site.Create(&domain.Site{
		Name: "projection-site", BaseURL: "https://projection.example",
		Platform: "openai-compatible", Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	channelID, err = db.Channel.Create(&domain.Channel{
		SiteID: &siteID, Name: "projection-ch", BaseURL: "https://projection.example",
		ModelsCSV: "model-projection", GroupName: "default", Weight: 100,
		Status: domain.StatusEnabled, TypeHint: "openai-compatible",
		MaxReasoningEffort:   "high",
		PayloadRules:         `[{"name":"cap","match":{},"actions":[]}]`,
		MaxConcurrent:        7,
		StreamPolicy:         "buffer",
		ProxyURL:             "http://127.0.0.1:7897",
		HeaderOverride:       `{"x-tenant":"acme"}`,
		SystemPrompt:         "be terse",
		RetryConfig:          `{"max_attempts":3}`,
		StableFirst:          true,
		ModelSyncMode:        domain.ModelSyncModeAuto,
		UpstreamPathOverride: "/api/paas/v4/chat/completions",
		UpstreamPathMap:      `{"models":"models"}`,
		UpstreamRequestMap:   `[{"from":"messages.0.content","to":"state"}]`,
		UpstreamResponseMap:  `[{"to":"choices.0.message.content","template":"{answers.0.noul}"}]`,
	})
	if err != nil {
		t.Fatal(err)
	}
	routeID, err = db.Route.Create(&domain.Route{
		ModelPattern: "model-projection", Enabled: true, RoutingMode: domain.RoutingModeAuto,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.RouteMember.Create(&domain.RouteMember{
		RouteID: routeID, ChannelID: channelID, Priority: 1, Weight: 1, Enabled: true,
		GroupName: "default",
	}); err != nil {
		t.Fatal(err)
	}
	_ = now
	return routeID, channelID
}

// assertProjectedChannel checks every column that the hot-path projections read.
// A column/scan mismatch shows up here as a cross-assigned value — which is
// exactly how `upstream_path_override` ended up holding `created_at`.
func assertProjectedChannel(t *testing.T, label string, ch domain.Channel) {
	t.Helper()
	checks := []struct {
		field string
		got   string
		want  string
	}{
		{"name", ch.Name, "projection-ch"},
		{"base_url", ch.BaseURL, "https://projection.example"},
		{"models_csv", ch.ModelsCSV, "model-projection"},
		{"group_name", ch.GroupName, "default"},
		{"status", ch.Status, domain.StatusEnabled},
		{"type_hint", ch.TypeHint, "openai-compatible"},
		{"max_reasoning_effort", ch.MaxReasoningEffort, "high"},
		{"payload_rules", ch.PayloadRules, `[{"name":"cap","match":{},"actions":[]}]`},
		{"stream_policy", ch.StreamPolicy, "buffer"},
		{"proxy_url", ch.ProxyURL, "http://127.0.0.1:7897"},
		{"header_override", ch.HeaderOverride, `{"x-tenant":"acme"}`},
		{"system_prompt", ch.SystemPrompt, "be terse"},
		{"retry_config", ch.RetryConfig, `{"max_attempts":3}`},
		// model_sync_mode is deliberately absent from both route-member
		// projections: it only drives model adoption during discovery
		// (relay.go reads it via ListEnabled, not via a routing candidate), so the
		// relay's hot-path SELECT is not expected to carry it. Asserting it here
		// would demand a column no consumer reads.
		{"upstream_path_override", ch.UpstreamPathOverride, "/api/paas/v4/chat/completions"},
		{"upstream_path_map", ch.UpstreamPathMap, `{"models":"models"}`},
		{"upstream_request_map", ch.UpstreamRequestMap, `[{"from":"messages.0.content","to":"state"}]`},
		{"upstream_response_map", ch.UpstreamResponseMap, `[{"to":"choices.0.message.content","template":"{answers.0.noul}"}]`},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s: %s = %q, want %q", label, c.field, c.got, c.want)
		}
	}
	if ch.MaxConcurrent != 7 {
		t.Errorf("%s: max_concurrent = %d, want 7", label, ch.MaxConcurrent)
	}
	// stable_first_requests is a runtime counter owned by RecordGraySuccess; it is
	// not a form field, so Channel.Create/Update intentionally do not persist it
	// and a routing candidate reads whatever the counter happens to be. Asserting a
	// round-trip here would be asserting a behaviour the store does not have.
	if !ch.StableFirst {
		t.Errorf("%s: stable_first = false, want true", label)
	}
	if ch.CreatedAt.IsZero() || ch.UpdatedAt.IsZero() {
		t.Errorf("%s: timestamps are zero (columns were cross-assigned): created=%v updated=%v",
			label, ch.CreatedAt, ch.UpdatedAt)
	}
	// The single most valuable assertion: no text column may contain a value that
	// looks like a SQLite timestamp. That is what a shift into the created_at
	// slots produces, and it is not something a zero-value check would catch.
	for _, field := range []struct {
		name  string
		value string
	}{
		{"upstream_path_override", ch.UpstreamPathOverride},
		{"upstream_path_map", ch.UpstreamPathMap},
		{"upstream_request_map", ch.UpstreamRequestMap},
		{"upstream_response_map", ch.UpstreamResponseMap},
		{"base_url", ch.BaseURL},
		{"status", ch.Status},
	} {
		if looksLikeTimestamp(field.value) {
			t.Errorf("%s: %s = %q looks like a cross-assigned timestamp", label, field.name, field.value)
		}
	}
}

// looksLikeTimestamp reports whether a value matches SQLite's
// datetime('now') spelling, which is what a projection/scan shift injects into
// whichever text column sits before the timestamp pair.
func looksLikeTimestamp(value string) bool {
	if len(value) < len("2006-01-02 15:04:05") {
		return false
	}
	_, err := time.Parse("2006-01-02 15:04:05", value[:19])
	return err == nil
}

// Both route-member projections hand the channel to the relay, so both must read
// every column correctly. They are separate hand-written SELECT/Scan pairs, and
// a column added to one but not the other (or added in a different position) is
// invisible until a request runs — RoutingCandidates is the relay's hot path.
func TestRouteMemberProjectionsCarryChannelColumns(t *testing.T) {
	db := openTestDB(t)
	routeID, _ := channelProjectionFixture(t, db)

	t.Run("ListRouteOverviews", func(t *testing.T) {
		overviews, err := db.RouteMember.ListRouteOverviews()
		if err != nil {
			t.Fatal(err)
		}
		var found *domain.RoutingCandidate
		for i := range overviews {
			if overviews[i].Route.ID != routeID {
				continue
			}
			for j := range overviews[i].Members {
				found = &overviews[i].Members[j]
			}
		}
		if found == nil {
			t.Fatalf("route %d missing from ListRouteOverviews", routeID)
		}
		assertProjectedChannel(t, "ListRouteOverviews", found.Channel)
	})

	t.Run("RoutingCandidates", func(t *testing.T) {
		route, candidates, err := db.RouteMember.RoutingCandidates("model-projection", "")
		if err != nil {
			t.Fatal(err)
		}
		if route == nil || len(candidates) != 1 {
			t.Fatalf("RoutingCandidates returned route=%v members=%d, want one member", route, len(candidates))
		}
		assertProjectedChannel(t, "RoutingCandidates", candidates[0].Channel)
	})
}

package httpapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/lan/meta-gateway/internal/config"
	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/httpapi"
	"github.com/lan/meta-gateway/internal/store"
)

// Apply must never bind a channel to a model it does not serve: variants for
// un-adopted models are dropped, and a group left with nothing is skipped
// instead of minting an empty alias route.
func TestUnifyApplySkipsUnadoptedVariants(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	enc, err := crypto.New("unify-apply-test-key")
	if err != nil {
		t.Fatal(err)
	}
	handler := httpapi.New(&config.Config{
		AdminToken:  "admin-secret",
		AdminTokens: []string{"admin-secret"},
	}, db, enc)
	server := httptest.NewServer(handler)
	defer server.Close()

	post := func(t *testing.T, body any) (int, map[string]int) {
		t.Helper()
		encoded, _ := json.Marshal(body)
		request, _ := http.NewRequest(http.MethodPost, server.URL+"/admin/models/unify/apply", bytes.NewReader(encoded))
		request.Header.Set("Authorization", "Bearer admin-secret")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, _ := io.ReadAll(response.Body)
		var result map[string]int
		if err := json.Unmarshal(raw, &result); err != nil {
			t.Fatalf("bad response %d: %s", response.StatusCode, raw)
		}
		return response.StatusCode, result
	}

	c1, err := db.Channel.Create(&domain.Channel{Name: "C1", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	c2, err := db.Channel.Create(&domain.Channel{Name: "C2", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	// C1 serves the 5.6-sol upstream through an alias binding, so it is
	// adopted; C2 has nothing bound at all.
	alias, err := db.Route.Create(&domain.Route{ModelPattern: "c1-alias", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.RouteMember.Create(&domain.RouteMember{
		RouteID: alias, ChannelID: c1, Enabled: true,
		MappingJSON: `{"real":"5.6-sol"}`,
	}); err != nil {
		t.Fatal(err)
	}

	status, result := post(t, map[string]any{
		"groups": []map[string]any{{
			"canonical": "5.6-sol",
			"variants": []map[string]any{
				{"channel_id": c1, "model_name": "5.6-sol"},    // adopted -> bound
				{"channel_id": c2, "model_name": "5.6-sol-1"},  // never adopted -> dropped
				{"channel_id": c2, "model_name": "wong-2.5"},   // never adopted -> dropped
			},
		}},
	})
	if status != http.StatusOK {
		t.Fatalf("apply status=%d result=%+v", status, result)
	}
	if result["routes_created"] != 1 || result["members_created"] != 1 ||
		result["members_skipped"] != 2 || result["batch_count"] != 1 {
		t.Fatalf("unexpected apply outcome: %+v", result)
	}

	route, err := db.Route.GetByModel("5.6-sol")
	if err != nil || route == nil {
		t.Fatalf("alias route missing: %v %v", route, err)
	}
	members, err := db.RouteMember.ListByRoute(route.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 1 || members[0].ChannelID != c1 {
		t.Fatalf("expected exactly the adopted C1 member, got %+v", members)
	}

	// A group whose every variant is un-adopted must not create an empty alias.
	status, result = post(t, map[string]any{
		"groups": []map[string]any{{
			"canonical": "wong-2.5",
			"variants":  []map[string]any{{"channel_id": c2, "model_name": "wong-2.5"}},
		}},
	})
	if status != http.StatusOK {
		t.Fatalf("empty-group apply status=%d result=%+v", status, result)
	}
	if result["routes_created"] != 0 || result["members_created"] != 0 ||
		result["members_skipped"] != 1 || result["batch_count"] != 0 {
		t.Fatalf("expected all variants dropped, got %+v", result)
	}
	if route, err := db.Route.GetByModel("wong-2.5"); err != nil || route != nil {
		t.Fatalf("un-adopted group must not mint an alias route: route=%v err=%v", route, err)
	}
}

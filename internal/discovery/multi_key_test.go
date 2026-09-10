package discovery_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lan/meta-gateway/internal/adapters"
	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/discovery"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

// A New API host returns different model lists per key (each key is bound to a
// group). Discovery must merge every key's list instead of stopping at the
// first success, and snapshot per-key visibility for routing.
func TestRefreshMergesModelsAcrossKeys(t *testing.T) {
	keyModels := map[string]string{
		"group-codex":  `{"data":[{"id":"codex-1"},{"id":"shared-2"}]}`,
		"group-gemini": `{"data":[{"id":"gemini-3"},{"id":"shared-2"}]}`,
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		body, ok := keyModels[bearer]
		if !ok {
			http.Error(w, `{"error":{"message":"unknown key"}}`, 401)
			return
		}
		_, _ = io.WriteString(w, body)
	}))
	defer upstream.Close()

	db, err := store.Open(filepath.Join(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	enc, _ := crypto.New("multi-key-test")
	codexSecret, _ := enc.Encrypt([]byte("group-codex"))
	geminiSecret, _ := enc.Encrypt([]byte("group-gemini"))
	siteID, _ := db.Site.Create(&domain.Site{Name: "site", BaseURL: upstream.URL, Platform: "new-api", Status: domain.StatusEnabled})
	codexID, _ := db.Credential.Create(&domain.Credential{SiteID: siteID, Kind: "api_key", SecretEnc: []byte(codexSecret), Status: domain.StatusEnabled})
	geminiID, _ := db.Credential.Create(&domain.Credential{SiteID: siteID, Kind: "api_key", SecretEnc: []byte(geminiSecret), Status: domain.StatusEnabled})
	channelID, err := db.Channel.Create(&domain.Channel{SiteID: &siteID, Name: "channel", BaseURL: upstream.URL, Status: domain.StatusEnabled, ModelSyncMode: domain.ModelSyncModeAuto})
	if err != nil {
		t.Fatal(err)
	}

	service := discovery.New(db, enc, adapters.NewRegistry(nil))
	result, err := service.Refresh(t.Context(), channelID)
	if err != nil {
		t.Fatal(err)
	}
	// Merged, sorted, de-duplicated.
	if strings.Join(result.Models, ",") != "codex-1,gemini-3,shared-2" {
		t.Fatalf("merged models = %v", result.Models)
	}
	// Per-key visibility recorded: each key only lists its own group's models.
	codexSet, err := db.Credential.ModelSetByCredential(codexID)
	if err != nil {
		t.Fatal(err)
	}
	geminiSet, err := db.Credential.ModelSetByCredential(geminiID)
	if err != nil {
		t.Fatal(err)
	}
	assertSet(t, codexSet, "codex-1", "shared-2")
	assertSet(t, geminiSet, "gemini-3", "shared-2")

	// The site-level query used by the relay also reflects both keys.
	siteSets, err := db.Credential.ModelSetsBySite(siteID)
	if err != nil {
		t.Fatal(err)
	}
	if len(siteSets) != 2 || len(siteSets[codexID]) != 2 || len(siteSets[geminiID]) != 2 {
		t.Fatalf("site model sets = %v", siteSets)
	}
}

// A failed key must not block the whole discovery: the healthy key's models
// still sync, and the broken key keeps its previously recorded set.
func TestRefreshSkipsFailingKeyAndKeepsItsPriorSet(t *testing.T) {
	var failBroken atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if bearer == "broken-key" && failBroken.Load() {
			http.Error(w, `{"error":{"message":"forbidden"}}`, 403)
			return
		}
		if bearer == "broken-key" {
			_, _ = io.WriteString(w, `{"data":[{"id":"broken-model"}]}`)
			return
		}
		_, _ = io.WriteString(w, `{"data":[{"id":"healthy-model"}]}`)
	}))
	defer upstream.Close()

	db, err := store.Open(filepath.Join(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	enc, _ := crypto.New("skip-key-test")
	brokenSecret, _ := enc.Encrypt([]byte("broken-key"))
	healthySecret, _ := enc.Encrypt([]byte("healthy-key"))
	siteID, _ := db.Site.Create(&domain.Site{Name: "site", BaseURL: upstream.URL, Platform: "new-api", Status: domain.StatusEnabled})
	brokenID, _ := db.Credential.Create(&domain.Credential{SiteID: siteID, Kind: "api_key", SecretEnc: []byte(brokenSecret), Status: domain.StatusEnabled})
	healthyID, _ := db.Credential.Create(&domain.Credential{SiteID: siteID, Kind: "api_key", SecretEnc: []byte(healthySecret), Status: domain.StatusEnabled})
	channelID, err := db.Channel.Create(&domain.Channel{SiteID: &siteID, Name: "channel", BaseURL: upstream.URL, Status: domain.StatusEnabled, ModelSyncMode: domain.ModelSyncModeAuto})
	if err != nil {
		t.Fatal(err)
	}

	// First pass: both keys succeed; the broken key's set is recorded.
	service := discovery.New(db, enc, adapters.NewRegistry(nil))
	if _, err := service.Refresh(t.Context(), channelID); err != nil {
		t.Fatal(err)
	}
	brokenBefore, _ := db.Credential.ModelSetByCredential(brokenID)
	assertSet(t, brokenBefore, "broken-model")

	// Second pass: the broken key now fails (403); refresh still succeeds with
	// the healthy key's models, and the broken key's prior set is preserved.
	failBroken.Store(true)
	if _, err := service.Refresh(t.Context(), channelID); err != nil {
		t.Fatal(err)
	}
	healthySet, _ := db.Credential.ModelSetByCredential(healthyID)
	assertSet(t, healthySet, "healthy-model")
	brokenSet, err := db.Credential.ModelSetByCredential(brokenID)
	if err != nil {
		t.Fatal(err)
	}
	if len(brokenSet) != 1 {
		t.Fatalf("broken key set should be preserved, got %v", brokenSet)
	}
	assertSet(t, brokenSet, "broken-model")
}

func assertSet(t *testing.T, set map[string]struct{}, want ...string) {
	t.Helper()
	if len(set) != len(want) {
		t.Fatalf("set = %v, want %v", set, want)
	}
	for _, name := range want {
		if _, ok := set[name]; !ok {
			t.Fatalf("set = %v missing %q", set, name)
		}
	}
}

package proxy

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/relay"
	"github.com/lan/meta-gateway/internal/routing"
	"github.com/lan/meta-gateway/internal/store"
	"github.com/lan/meta-gateway/internal/usage"
)

// Routing must pick the key capable of serving the requested model. With two
// group-scoped keys (one lists codex-* models, the other gemini-*), a request
// for a codex model must only use the codex key, and a shared model may use
// either. Keys without any recorded set stay usable (pool fallback).
func TestKeyPoolServesModelFromDiscoveredSet(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	enc, _ := crypto.New("pool-test-key")
	codexSecret, _ := enc.Encrypt([]byte("codex-secret"))
	geminiSecret, _ := enc.Encrypt([]byte("gemini-secret"))
	siteID, _ := db.Site.Create(&domain.Site{Name: "site", Status: domain.StatusEnabled})
	codexID, _ := db.Credential.Create(&domain.Credential{SiteID: siteID, Kind: "api_key", SecretEnc: []byte(codexSecret), Status: domain.StatusEnabled})
	geminiID, _ := db.Credential.Create(&domain.Credential{SiteID: siteID, Kind: "api_key", SecretEnc: []byte(geminiSecret), Status: domain.StatusEnabled})
	channel, err := db.Channel.Create(&domain.Channel{SiteID: &siteID, Name: "channel", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}

	fromDB, err := db.Channel.GetByID(channel)
	if err != nil {
		t.Fatal(err)
	}
	fromDB.CredentialID = nil // pool covers every key on the site

	// Record per-key visibility exactly like discovery would.
	if _, err := db.DiscoveredModel.Reconcile(context.Background(), store.ReconcileInput{
		ChannelID:        channel,
		Models:           []string{"codex-x", "shared", "gemini-y"},
		CredentialModels: map[int64][]string{codexID: {"codex-x", "shared"}, geminiID: {"gemini-y", "shared"}},
		CheckedAt:        time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	service, err := buildPoolService(db, enc)
	if err != nil {
		t.Fatal(err)
	}

	// codex model: only the codex key is usable.
	keys, err := service.resolveAPIKeyPool(*fromDB, "codex-x")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0] != "codex-secret" {
		t.Fatalf("codex pool = %v, want [codex-secret]", keys)
	}
	// gemini model: only the gemini key.
	keys, err = service.resolveAPIKeyPool(*fromDB, "gemini-y")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0] != "gemini-secret" {
		t.Fatalf("gemini pool = %v, want [gemini-secret]", keys)
	}
	// shared model: both keys qualify (rotation order: by id).
	keys, err = service.resolveAPIKeyPool(*fromDB, "shared")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 {
		t.Fatalf("shared pool = %v, want 2 keys", keys)
	}
	// unknown model: nobody's discovered set claims it — renamed/aliased or
	// custom names never appear in a key's recorded list. The pool fails open
	// with both keys (a wrong-group key draws a missable upstream 404) instead
	// of hard-failing with a bogus credential error.
	keys, err = service.resolveAPIKeyPool(*fromDB, "ghost")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 {
		t.Fatalf("ghost pool = %v, want both keys (fail-open)", keys)
	}
}

// A key with an explicit models_csv allowlist keeps manual filtering; a key
// without any discovered set remains usable for any model (backwards compat).
func TestKeyPoolManualAllowlistAndUnlearnedKey(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	enc, _ := crypto.New("pool-manual-test")
	manualSecret, _ := enc.Encrypt([]byte("manual-secret"))
	unlearnedSecret, _ := enc.Encrypt([]byte("unlearned-secret"))
	siteID, _ := db.Site.Create(&domain.Site{Name: "site", Status: domain.StatusEnabled})
	manualID, _ := db.Credential.Create(&domain.Credential{SiteID: siteID, Kind: "api_key", SecretEnc: []byte(manualSecret), Status: domain.StatusEnabled, ModelsCSV: "gpt-4*,claude-3"})
	unlearnedID, _ := db.Credential.Create(&domain.Credential{SiteID: siteID, Kind: "api_key", SecretEnc: []byte(unlearnedSecret), Status: domain.StatusEnabled})
	channel, err := db.Channel.Create(&domain.Channel{SiteID: &siteID, Name: "channel", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	// Record a discovered set for the manual key only.
	if _, err := db.DiscoveredModel.Reconcile(context.Background(), store.ReconcileInput{
		ChannelID:        channel,
		Models:           []string{"gpt-4o", "gpt-5"},
		CredentialModels: map[int64][]string{manualID: {"gpt-4o", "gpt-5"}},
		CheckedAt:        time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	service, err := buildPoolService(db, enc)
	if err != nil {
		t.Fatal(err)
	}
	fromDB, _ := db.Channel.GetByID(channel)
	fromDB.CredentialID = nil

	// gpt-4o: manual key matches its allowlist AND its discovered set.
	keys, err := service.resolveAPIKeyPool(*fromDB, "gpt-4o")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 {
		t.Fatalf("gpt-4o pool = %v, want both keys (manual + unlearned)", keys)
	}
	// claude-3: manual allowlist covers it, discovered set does not. Manual
	// allowlist wins, so the manual key must still serve it.
	keys, err = service.resolveAPIKeyPool(*fromDB, "claude-3")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, key := range keys {
		if key == "manual-secret" {
			found = true
		}
	}
	if !found {
		t.Fatalf("claude-3 pool lost manual key: %v", keys)
	}
	// gemini-new: nobody lists it; the unlearned key (no set) stays usable.
	keys, err = service.resolveAPIKeyPool(*fromDB, "gemini-new")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0] != "unlearned-secret" {
		t.Fatalf("gemini-new pool = %v, want [unlearned-secret]", keys)
	}
	_ = unlearnedID
}

// End-to-end: a request for a model only served by one group-scoped key must
// travel upstream with THAT key's Authorization header, not the other key.
func TestRelayUsesKeyThatServesModel(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	enc, _ := crypto.New("relay-key-test")
	codexSecret, _ := enc.Encrypt([]byte("sk-codex-secret"))
	geminiSecret, _ := enc.Encrypt([]byte("sk-gemini-secret"))
	siteID, _ := db.Site.Create(&domain.Site{Name: "site", Status: domain.StatusEnabled})
	codexID, _ := db.Credential.Create(&domain.Credential{SiteID: siteID, Kind: "api_key", SecretEnc: []byte(codexSecret), Status: domain.StatusEnabled})
	geminiID, _ := db.Credential.Create(&domain.Credential{SiteID: siteID, Kind: "api_key", SecretEnc: []byte(geminiSecret), Status: domain.StatusEnabled})
	channelID, err := db.Channel.Create(&domain.Channel{SiteID: &siteID, Name: "channel", BaseURL: "https://upstream.example", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	routeID, err := db.Route.Create(&domain.Route{ModelPattern: "codex-latest", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	memberID, err := db.RouteMember.Create(&domain.RouteMember{RouteID: routeID, ChannelID: channelID, Priority: 10, Weight: 100, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := db.DiscoveredModel.Reconcile(context.Background(), store.ReconcileInput{
		ChannelID:        channelID,
		Models:           []string{"codex-latest", "gemini-flash"},
		CredentialModels: map[int64][]string{codexID: {"codex-latest"}, geminiID: {"gemini-flash"}},
		CheckedAt:        time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	// A relay recording which API key each upstream call carried.
	recorder := &keyRecordingRelay{headers: make([]string, 0)}
	now := time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC)
	selector := routing.NewWithDependencies(db.RouteMember, fixedClock{now: now}, firstRandom{})
	service := New(selector, recorder, db, enc, 2, time.Minute)
	service.SetKeyPoolRotation(true)
	service.now = func() time.Time { return now }

	result := service.ChatCompletions(context.Background(), Request{
		RequestID: "model-key-routing",
		Model:     "codex-latest",
		Body:      []byte(`{"model":"codex-latest","messages":[{"role":"user","content":"hi"}]}`),
	})
	if result.Err != nil || result.StatusCode != 200 {
		t.Fatalf("relay failed: %+v", result)
	}
	defer result.Body.Close()
	if len(recorder.headers) != 1 || recorder.headers[0] != "Bearer sk-codex-secret" {
		t.Fatalf("upstream auth = %v, want one codex key", recorder.headers)
	}
	_ = memberID
}

// End-to-end regression: an alias (or unified) name rides a member whose
// mapping points at the real upstream model. Key-pool selection must resolve
// credentials against that effective name — filtering on the public alias
// starved the pool into a bogus "credential unavailable" 502 even though
// model listing worked fine.
func TestRelayResolvesKeysForAliasedModel(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	enc, _ := crypto.New("alias-key-test")
	codexSecret, _ := enc.Encrypt([]byte("sk-codex-secret"))
	geminiSecret, _ := enc.Encrypt([]byte("sk-gemini-secret"))
	siteID, _ := db.Site.Create(&domain.Site{Name: "site", Status: domain.StatusEnabled})
	codexID, _ := db.Credential.Create(&domain.Credential{SiteID: siteID, Kind: "api_key", SecretEnc: []byte(codexSecret), Status: domain.StatusEnabled})
	geminiID, _ := db.Credential.Create(&domain.Credential{SiteID: siteID, Kind: "api_key", SecretEnc: []byte(geminiSecret), Status: domain.StatusEnabled})
	channelID, err := db.Channel.Create(&domain.Channel{SiteID: &siteID, Name: "channel", BaseURL: "https://upstream.example", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	// Alias route: public "codex-alias" → real "codex-latest" on the member.
	routeID, err := db.Route.Create(&domain.Route{ModelPattern: "codex-alias", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.RouteMember.Create(&domain.RouteMember{
		RouteID: routeID, ChannelID: channelID, Priority: 10, Weight: 100,
		Enabled: true, MappingJSON: `{"real":"codex-latest"}`,
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := db.DiscoveredModel.Reconcile(context.Background(), store.ReconcileInput{
		ChannelID:        channelID,
		Models:           []string{"codex-latest", "gemini-flash"},
		CredentialModels: map[int64][]string{codexID: {"codex-latest"}, geminiID: {"gemini-flash"}},
		CheckedAt:        time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	// The gemini key cannot serve the request (upstream would 404); only the
	// codex key answers.
	relay := &keyAwareRelay{
		codes: map[string]int{
			"Bearer sk-codex-secret":  200,
			"Bearer sk-gemini-secret": 404,
		},
	}
	now := time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC)
	selector := routing.NewWithDependencies(db.RouteMember, fixedClock{now: now}, firstRandom{})
	service := New(selector, relay, db, enc, 2, time.Minute)
	service.SetKeyPoolRotation(true)
	service.now = func() time.Time { return now }

	result := service.ChatCompletions(context.Background(), Request{
		RequestID: "alias-key-routing",
		Model:     "codex-alias",
		Body:      []byte(`{"model":"codex-alias","messages":[{"role":"user","content":"hi"}]}`),
	})
	if result.Err != nil || result.StatusCode != 200 {
		t.Fatalf("aliased relay failed: %+v", result)
	}
	defer result.Body.Close()
	if len(relay.headers) == 0 || relay.headers[0] != "Bearer sk-codex-secret" {
		t.Fatalf("upstream auth = %v, want the codex key first", relay.headers)
	}
}

// keyAwareRelay answers per-key: 200 for keys in codes with 200, the mapped
// status otherwise. Records every Authorization header.
type keyAwareRelay struct {
	codes   map[string]int
	headers []string
}

func (r *keyAwareRelay) answer(header string) *relay.Result {
	code, ok := r.codes[header]
	if !ok {
		code = 404
	}
	if code == 200 {
		r.headers = append(r.headers, header)
	}
	return response(code, `{"ok":true}`)
}

func (r *keyAwareRelay) ChatCompletionsContext(_ context.Context, _, apiKey string, _ []byte, _ bool) *relay.Result {
	return r.answer("Bearer " + apiKey)
}

func (r *keyAwareRelay) ForwardContext(_ context.Context, _, _, _ string, _ []byte) *relay.Result {
	return response(200, `{"ok":true}`)
}

func (r *keyAwareRelay) ForwardWithHeaders(_ context.Context, _, _ string, h http.Header, _ []byte) *relay.Result {
	return r.answer(h.Get("Authorization"))
}

// keyRecordingRelay captures the Authorization header of every upstream call.
type keyRecordingRelay struct {
	headers []string
}

func (r *keyRecordingRelay) ChatCompletionsContext(_ context.Context, _, apiKey string, _ []byte, _ bool) *relay.Result {
	r.headers = append(r.headers, "Bearer "+apiKey)
	return response(200, `{"ok":true}`)
}

func (r *keyRecordingRelay) ForwardContext(_ context.Context, _, _, _ string, _ []byte) *relay.Result {
	return response(200, `{"ok":true}`)
}

func (r *keyRecordingRelay) ForwardWithHeaders(_ context.Context, _, _ string, h http.Header, _ []byte) *relay.Result {
	r.headers = append(r.headers, h.Get("Authorization"))
	return response(200, `{"ok":true}`)
}

// buildPoolService assembles a Service with the same wiring as setupProxy but
// without fixture members/routes (key-pool selection only).
func buildPoolService(db *store.DB, enc *crypto.Encrypter) (*Service, error) {
	now := time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC)
	selector := routing.NewWithDependencies(db.RouteMember, fixedClock{now: now}, firstRandom{})
	service := New(selector, &queuedRelay{}, db, enc, 2, time.Minute)
	service.SetKeyPoolRotation(true)
	return service, nil
}

// Per-model self-set prices take precedence over the downstream key's unit
// prices; models without a priced metadata row fall back to the key price.
func TestBillingCostModelPricePrecedence(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	enc, _ := crypto.New("billing-test")
	keyID, err := db.DownstreamKey.Create(&domain.DownstreamKey{
		Name:                 "client",
		Enabled:              true,
		PricePromptPer1k:     10,
		PriceCompletionPer1k: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := buildPoolService(db, enc)
	if err != nil {
		t.Fatal(err)
	}

	// No metadata row: the key price bills everything (legacy formula lumps
	// cache tokens into the prompt).
	req := Request{Model: "m1", DownstreamKeyID: keyID}
	if cost := service.billingCost(req, usage.Tokens{PromptTokens: 1000, CompletionTokens: 1000}); cost != 20 {
		t.Fatalf("key-price cost = %v, want 20", cost)
	}

	// Priced metadata: prompt 2, completion 4, cache-read 1 per 1k.
	if err := db.ModelMetadata.Upsert(&domain.ModelMetadata{
		ModelName: "m1", PricePromptPer1k: 2, PriceCompletionPer1k: 4, PriceCachePer1k: 1,
	}); err != nil {
		t.Fatal(err)
	}
	cost := service.billingCost(req, usage.Tokens{
		PromptTokens: 1000, CompletionTokens: 1000, CacheReadTokens: 1000,
	})
	if cost != 7 {
		t.Fatalf("model-price cost = %v, want 7", cost)
	}

	// A model without a priced row keeps the key price.
	other := Request{Model: "m2", DownstreamKeyID: keyID}
	if cost := service.billingCost(other, usage.Tokens{CompletionTokens: 1000}); cost != 10 {
		t.Fatalf("fallback cost = %v, want 10", cost)
	}
}

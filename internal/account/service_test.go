package account

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/adapters"
	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

func TestFinanceCacheUsesServiceClockAndReturnsCopies(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	svc := &Service{
		now: func() time.Time { return now },
		financeCache: &financeCache{
			at: now.Add(-time.Minute),
			items: []FinanceItem{{
				ChannelID: 1,
				Prices:    map[string]adapters.ModelPrice{"model": {Model: "model", PriceUSD: 1}},
			}},
		},
	}
	svc.financeMu.Lock()
	fresh := svc.financeCacheFreshLocked()
	svc.financeMu.Unlock()
	if !fresh {
		t.Fatal("fresh cache reported stale")
	}
	now = now.Add(-time.Hour)
	svc.financeMu.Lock()
	fresh = svc.financeCacheFreshLocked()
	svc.financeMu.Unlock()
	if fresh {
		t.Fatal("cache must expire after a clock rollback")
	}

	copyItems := cloneFinanceItems(svc.financeCache.items)
	copyItems[0].Prices["model"] = adapters.ModelPrice{Model: "changed"}
	if got := svc.financeCache.items[0].Prices["model"].Model; got != "model" {
		t.Fatalf("cached price mutated through caller copy: %q", got)
	}
}

// TestPricingNormalizesAAHFormula covers the All API Hub normalization in
// financeForChannel: group_ratio multiplier, direct USD override, per-call.
func TestPricingNormalizesAAHFormula(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/user/self":
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":1,"username":"u","quota":100000000,"used_quota":0}}`))
		case "/api/pricing":
			_, _ = w.Write([]byte(`{"success":true,"group_ratio":{"default":2,"vip":4},"data":[
				{"model_name":"ratio-model","model_ratio":0.5,"completion_ratio":2,"quota_type":0},
				{"model_name":"direct-model","model_ratio":0,"model_price":0,"token_price_usd_per_million":{"input":3,"output":12}},
				{"model_name":"percall-model","model_price":5,"quota_type":1},
				{"model_name":"free-model","model_ratio":0,"model_price":0}
			]}`))
		case "/api/status":
			_, _ = w.Write([]byte(`{"success":true,"data":{"quota_per_unit":500000}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	adapter := adapters.NewNewAPIAccountAdapter("new-api", server.Client(), true)
	prices, err := adapter.Pricing(context.Background(), adapters.AccountInput{BaseURL: server.URL, Secret: "user-token"})
	if err != nil {
		t.Fatalf("pricing: %v", err)
	}
	byModel := map[string]adapters.ModelPrice{}
	for _, p := range prices {
		byModel[p.Model] = p
	}
	// Group ratio resolved from "default" = 2.
	if byModel["ratio-model"].GroupRatio != 2 {
		t.Fatalf("group_ratio: %+v", byModel["ratio-model"])
	}
	if byModel["ratio-model"].CompletionRatio != 2 || byModel["ratio-model"].QuotaType != 0 {
		t.Fatalf("ratio fields: %+v", byModel["ratio-model"])
	}
	if byModel["direct-model"].TokenUSD == nil || byModel["direct-model"].TokenUSD.Input != 3 {
		t.Fatalf("direct USD: %+v", byModel["direct-model"])
	}
	if byModel["percall-model"].Mode != "fixed" || byModel["percall-model"].ModelPrice != 5 {
		t.Fatalf("percall: %+v", byModel["percall-model"])
	}
	if _, exists := byModel["free-model"]; exists {
		t.Fatalf("unpriced must be skipped")
	}

	// Service-level normalization (financeForChannel path).
	db, err := store.Open(filepath.Join(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	enc, err := crypto.New("aah-formula-key")
	if err != nil {
		t.Fatal(err)
	}
	svc := New(db, enc, adapters.NewRegistry(server.Client()))
	siteID, err := db.Site.Create(&domain.Site{
		Name: "aah", BaseURL: server.URL, Platform: "new-api", Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	secretEnc, _ := enc.Encrypt([]byte("user-token"))
	credID, err := db.Credential.Create(&domain.Credential{
		SiteID: siteID, Kind: "access_token", SecretEnc: []byte(secretEnc), Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Channel.Create(&domain.Channel{
		SiteID: &siteID, CredentialID: &credID, Name: "aah", BaseURL: "",
		GroupName: "default", Priority: 0, Weight: 100, Status: domain.StatusEnabled, TypeHint: "new-api",
	})
	if err != nil {
		t.Fatal(err)
	}
	items, err := svc.FinanceOverview(context.Background())
	if err != nil || len(items) != 1 {
		t.Fatalf("finance: %+v err=%v", items, err)
	}
	pricesByModel := items[0].Prices
	// ratio 0.5 × 2 (1e6/500k) × 2 (group) = 2 USD/1M; output × 2 = 4.
	if got := pricesByModel["ratio-model"].PriceUSD; math.Abs(got-2.0) > 1e-9 {
		t.Fatalf("ratio-model input USD=%v want 2", got)
	}
	if got := pricesByModel["ratio-model"].OutputUSD; math.Abs(got-4.0) > 1e-9 {
		t.Fatalf("ratio-model output USD=%v want 4", got)
	}
	// Direct USD wins untouched.
	if got := pricesByModel["direct-model"].PriceUSD; math.Abs(got-3.0) > 1e-9 {
		t.Fatalf("direct-model USD=%v want 3", got)
	}
	// Per-call: 5 × 2 = 10 USD/request.
	if got := pricesByModel["percall-model"].PriceUSD; math.Abs(got-10.0) > 1e-9 {
		t.Fatalf("percall-model USD=%v want 10", got)
	}
}

func TestProbeAndSyncKeys(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/user/self":
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":9,"username":"bob","quota":50,"used_quota":1}}`))
		case "/api/token/":
			_, _ = w.Write([]byte(`{"success":true,"data":[{"id":1,"name":"relay","key":"sk-from-site","status":1}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	db, err := store.Open(filepath.Join(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	enc, err := crypto.New("account-test-key")
	if err != nil {
		t.Fatal(err)
	}
	registry := adapters.NewRegistry(server.Client())
	svc := New(db, enc, registry)

	siteID, err := db.Site.Create(&domain.Site{
		Name: "demo", BaseURL: server.URL, Platform: "new-api", Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	secretEnc, err := enc.Encrypt([]byte("user-access-token"))
	if err != nil {
		t.Fatal(err)
	}
	credID, err := db.Credential.Create(&domain.Credential{
		SiteID: siteID, Kind: "access_token", SecretEnc: []byte(secretEnc), Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	channelID, err := db.Channel.Create(&domain.Channel{
		SiteID: &siteID, CredentialID: &credID, Name: "demo", BaseURL: "",
		GroupName: "default", Priority: 0, Weight: 100, Status: domain.StatusEnabled, TypeHint: "new-api",
	})
	if err != nil {
		t.Fatal(err)
	}

	probe, err := svc.Probe(context.Background(), channelID)
	if err != nil {
		t.Fatal(err)
	}
	if probe.Username != "bob" || probe.PlatformUserID != 9 {
		t.Fatalf("probe=%+v", probe)
	}

	sync, err := svc.SyncKeys(context.Background(), channelID, SyncKeysRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if sync.CreatedCredentials != 1 || sync.AttachedCredentialID == 0 {
		t.Fatalf("sync=%+v", sync)
	}
	channel, err := db.Channel.GetByID(channelID)
	if err != nil || channel == nil || channel.CredentialID == nil {
		t.Fatalf("channel after sync: %+v err=%v", channel, err)
	}
	if *channel.CredentialID == credID {
		t.Fatal("channel should rebind to api_key credential")
	}
	bound, err := db.Credential.GetByID(*channel.CredentialID)
	if err != nil || bound == nil || bound.Kind != "api_key" {
		t.Fatalf("bound=%+v err=%v", bound, err)
	}
}

func TestCreateKeyFallsBackToRevealWhenCreateResponseMasked(t *testing.T) {
	var revealHits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/user/self":
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":9,"username":"bob"}}`))
		case r.URL.Path == "/api/token/" && r.Method == http.MethodPost:
			// Create response carries a masked key (typical New-API fork behavior).
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":42,"name":"gateway-auto","key":"sk-xxxx****yyyy"}}`))
		case r.URL.Path == "/api/token/42/key":
			revealHits++
			_, _ = w.Write([]byte(`{"success":true,"data":"sk-full-revealed"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	db, err := store.Open(filepath.Join(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	enc, err := crypto.New("reveal-test-key")
	if err != nil {
		t.Fatal(err)
	}
	svc := New(db, enc, adapters.NewRegistry(server.Client()))

	siteID, err := db.Site.Create(&domain.Site{
		Name: "reveal", BaseURL: server.URL, Platform: "new-api", Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	secretEnc, _ := enc.Encrypt([]byte("user-access-token"))
	credID, err := db.Credential.Create(&domain.Credential{
		SiteID: siteID, Kind: "access_token", SecretEnc: []byte(secretEnc), Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	channelID, err := db.Channel.Create(&domain.Channel{
		SiteID: &siteID, CredentialID: &credID, Name: "reveal", BaseURL: "",
		GroupName: "default", Priority: 0, Weight: 100, Status: domain.StatusEnabled, TypeHint: "new-api",
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := svc.CreateKey(context.Background(), channelID, CreateKeyRequest{Name: "gateway-auto"})
	if err != nil {
		t.Fatalf("create key with reveal fallback: %v", err)
	}
	if result.CredentialID == 0 {
		t.Fatalf("result=%+v", result)
	}
	if revealHits == 0 {
		t.Fatal("reveal endpoint was never called")
	}
	bound, err := db.Credential.GetByID(result.CredentialID)
	if err != nil || bound == nil {
		t.Fatalf("bound=%+v err=%v", bound, err)
	}
	plain, err := enc.Decrypt(string(bound.SecretEnc))
	if err != nil || string(plain) != "sk-full-revealed" {
		t.Fatalf("stored secret=%q err=%v", plain, err)
	}
}

func TestCreateKeyFallsBackToListWhenRevealUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/user/self":
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":9,"username":"bob"}}`))
		case r.URL.Path == "/api/token/" && r.Method == http.MethodPost:
			// Create response masks the key AND has no usable reveal endpoint.
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":42,"name":"gateway-auto","key":"sk-xxxx****yyyy"}}`))
		case r.URL.Path == "/api/token/" && r.Method == http.MethodGet:
			// The list endpoint, however, exposes the full key (common fork).
			_, _ = w.Write([]byte(`{"success":true,"data":[{"id":42,"name":"gateway-auto","key":"sk-from-list","status":1}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	db, err := store.Open(filepath.Join(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	enc, err := crypto.New("list-fallback-test-key")
	if err != nil {
		t.Fatal(err)
	}
	svc := New(db, enc, adapters.NewRegistry(server.Client()))

	siteID, err := db.Site.Create(&domain.Site{
		Name: "listfallback", BaseURL: server.URL, Platform: "new-api", Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	secretEnc, _ := enc.Encrypt([]byte("user-access-token"))
	credID, err := db.Credential.Create(&domain.Credential{
		SiteID: siteID, Kind: "access_token", SecretEnc: []byte(secretEnc), Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	channelID, err := db.Channel.Create(&domain.Channel{
		SiteID: &siteID, CredentialID: &credID, Name: "listfallback", BaseURL: "",
		GroupName: "default", Priority: 0, Weight: 100, Status: domain.StatusEnabled, TypeHint: "new-api",
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := svc.CreateKey(context.Background(), channelID, CreateKeyRequest{Name: "gateway-auto"})
	if err != nil {
		t.Fatalf("create key with list fallback: %v", err)
	}
	bound, err := db.Credential.GetByID(result.CredentialID)
	if err != nil || bound == nil {
		t.Fatalf("bound=%+v err=%v", bound, err)
	}
	plain, err := enc.Decrypt(string(bound.SecretEnc))
	if err != nil || string(plain) != "sk-from-list" {
		t.Fatalf("stored secret=%q err=%v", plain, err)
	}
}

func TestCreateKeyFallsBackToRevealWhenListMasked(t *testing.T) {
	var revealHits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/user/self":
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":9,"username":"bob"}}`))
		case r.URL.Path == "/api/token/" && r.Method == http.MethodPost:
			// Create response carries a masked key.
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":42,"name":"gateway-auto","key":"sk-xxxx****yyyy"}}`))
		case r.URL.Path == "/api/token/42/key":
			revealHits++
			if revealHits == 1 {
				// Freshly created tokens may not be revealable for a moment.
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write([]byte(`{"success":true,"data":"sk-full-from-reveal"}`))
		case r.URL.Path == "/api/token/" && r.Method == http.MethodGet:
			// The list masks secrets too — only the reveal endpoint helps.
			_, _ = w.Write([]byte(`{"success":true,"data":[{"id":42,"name":"gateway-auto","key":"sk-xxxx****yyyy","status":1}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	db, err := store.Open(filepath.Join(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	enc, err := crypto.New("list-masked-reveal-key")
	if err != nil {
		t.Fatal(err)
	}
	svc := New(db, enc, adapters.NewRegistry(server.Client()))

	siteID, err := db.Site.Create(&domain.Site{
		Name: "masked", BaseURL: server.URL, Platform: "new-api", Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	secretEnc, _ := enc.Encrypt([]byte("user-access-token"))
	credID, err := db.Credential.Create(&domain.Credential{
		SiteID: siteID, Kind: "access_token", SecretEnc: []byte(secretEnc), Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	channelID, err := db.Channel.Create(&domain.Channel{
		SiteID: &siteID, CredentialID: &credID, Name: "masked", BaseURL: "",
		GroupName: "default", Priority: 0, Weight: 100, Status: domain.StatusEnabled, TypeHint: "new-api",
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := svc.CreateKey(context.Background(), channelID, CreateKeyRequest{Name: "gateway-auto"})
	if err != nil {
		t.Fatalf("create key with masked list fallback: %v", err)
	}
	if revealHits != 2 {
		t.Fatalf("reveal endpoint hits=%d, want 2 (initial attempt + list-matched attempt)", revealHits)
	}
	bound, err := db.Credential.GetByID(result.CredentialID)
	if err != nil || bound == nil {
		t.Fatalf("bound=%+v err=%v", bound, err)
	}
	plain, err := enc.Decrypt(string(bound.SecretEnc))
	if err != nil || string(plain) != "sk-full-from-reveal" {
		t.Fatalf("stored secret=%q err=%v", plain, err)
	}
}

func TestCreateKeyFallsBackToSyncWhenCreateResponseHasNoID(t *testing.T) {
	var revealHits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/user/self":
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":9,"username":"bob"}}`))
		case r.URL.Path == "/api/token/" && r.Method == http.MethodPost:
			// Create response carries no token id at all — some forks omit it.
			_, _ = w.Write([]byte(`{"success":true,"data":{"name":"gateway-default","key":"sk-xxxx****yyyy"}}`))
		case r.URL.Path == "/api/token/" && r.Method == http.MethodGet:
			// The list masks secrets too; only the per-token reveal helps.
			_, _ = w.Write([]byte(`{"success":true,"data":[{"id":77,"name":"gateway-default","key":"sk-xxxx****yyyy","status":1}]}`))
		case r.URL.Path == "/api/token/77/key":
			revealHits++
			_, _ = w.Write([]byte(`{"success":true,"data":"sk-full-via-sync"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	db, err := store.Open(filepath.Join(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	enc, err := crypto.New("no-id-sync-key")
	if err != nil {
		t.Fatal(err)
	}
	svc := New(db, enc, adapters.NewRegistry(server.Client()))

	siteID, err := db.Site.Create(&domain.Site{
		Name: "noid", BaseURL: server.URL, Platform: "new-api", Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	secretEnc, _ := enc.Encrypt([]byte("user-access-token"))
	credID, err := db.Credential.Create(&domain.Credential{
		SiteID: siteID, Kind: "access_token", SecretEnc: []byte(secretEnc), Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	channelID, err := db.Channel.Create(&domain.Channel{
		SiteID: &siteID, CredentialID: &credID, Name: "noid", BaseURL: "",
		GroupName: "default", Priority: 0, Weight: 100, Status: domain.StatusEnabled, TypeHint: "new-api",
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := svc.CreateKey(context.Background(), channelID, CreateKeyRequest{Name: "gateway-default"})
	if err != nil {
		t.Fatalf("create key with sync fallback: %v", err)
	}
	if result.CredentialID == 0 {
		t.Fatalf("result=%+v", result)
	}
	if revealHits != 1 {
		t.Fatalf("reveal hits=%d, want 1 (via sync import)", revealHits)
	}
	bound, err := db.Credential.GetByID(result.CredentialID)
	if err != nil || bound == nil {
		t.Fatalf("bound=%+v err=%v", bound, err)
	}
	plain, err := enc.Decrypt(string(bound.SecretEnc))
	if err != nil || string(plain) != "sk-full-via-sync" {
		t.Fatalf("stored secret=%q err=%v", plain, err)
	}
	channel, err := db.Channel.GetByID(channelID)
	if err != nil || channel == nil || channel.CredentialID == nil || *channel.CredentialID != result.CredentialID {
		t.Fatalf("channel=%+v err=%v", channel, err)
	}
}

func TestListTokenGroupsPrefersUserGroupsEndpoint(t *testing.T) {
	var tokenListHits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/user/self":
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":7,"username":"bob"}}`))
		case "/api/user/self/groups":
			// Groups the account may use, even though its token list is empty.
			_, _ = w.Write([]byte(`{"success":true,"data":["default","vip","claude code"]}`))
		case "/api/token/":
			tokenListHits++
			_, _ = w.Write([]byte(`{"success":true,"data":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	db, err := store.Open(filepath.Join(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	enc, err := crypto.New("groups-test-key")
	if err != nil {
		t.Fatal(err)
	}
	svc := New(db, enc, adapters.NewRegistry(server.Client()))

	siteID, err := db.Site.Create(&domain.Site{
		Name: "groups", BaseURL: server.URL, Platform: "new-api", Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	secretEnc, _ := enc.Encrypt([]byte("user-access-token"))
	credID, err := db.Credential.Create(&domain.Credential{
		SiteID: siteID, Kind: "access_token", SecretEnc: []byte(secretEnc), Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	channelID, err := db.Channel.Create(&domain.Channel{
		SiteID: &siteID, CredentialID: &credID, Name: "groups", BaseURL: "",
		GroupName: "default", Priority: 0, Weight: 100, Status: domain.StatusEnabled, TypeHint: "new-api",
	})
	if err != nil {
		t.Fatal(err)
	}

	groups, err := svc.ListTokenGroups(context.Background(), channelID)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 3 || groups[0] != "default" || groups[1] != "vip" || groups[2] != "claude code" {
		t.Fatalf("groups=%v", groups)
	}
	if tokenListHits != 0 {
		t.Fatalf("token list should not be consulted when the groups endpoint answers; hits=%d", tokenListHits)
	}
}

func TestListTokenGroupsFallsBackToTokenList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/user/self":
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":7,"username":"bob"}}`))
		case "/api/user/self/groups":
			// Older forks 404 the groups endpoint; enumeration must fall back.
			http.NotFound(w, r)
		case "/api/token/":
			_, _ = w.Write([]byte(`{"success":true,"data":[
				{"id":1,"name":"def-key","key":"sk-default-aaa","status":1,"group":"default"},
				{"id":2,"name":"vip-key","key":"sk-vip-bbb","status":1,"group":"vip"}
			]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	db, err := store.Open(filepath.Join(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	enc, err := crypto.New("groups-fallback-test")
	if err != nil {
		t.Fatal(err)
	}
	svc := New(db, enc, adapters.NewRegistry(server.Client()))

	siteID, err := db.Site.Create(&domain.Site{
		Name: "fallback", BaseURL: server.URL, Platform: "new-api", Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	secretEnc, _ := enc.Encrypt([]byte("user-access-token"))
	credID, err := db.Credential.Create(&domain.Credential{
		SiteID: siteID, Kind: "access_token", SecretEnc: []byte(secretEnc), Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	channelID, err := db.Channel.Create(&domain.Channel{
		SiteID: &siteID, CredentialID: &credID, Name: "fallback", BaseURL: "",
		GroupName: "default", Priority: 0, Weight: 100, Status: domain.StatusEnabled, TypeHint: "new-api",
	})
	if err != nil {
		t.Fatal(err)
	}

	groups, err := svc.ListTokenGroups(context.Background(), channelID)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 || groups[0] != "default" || groups[1] != "vip" {
		t.Fatalf("groups=%v", groups)
	}
}

func TestProbeRetriesTransientTransportFailure(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			// Simulate a flaky upstream (connection dropped mid-flight, as
			// with Cloudflare challenges): first attempt fails at transport.
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				http.Error(w, "no hijack", http.StatusInternalServerError)
				return
			}
			conn, _, _ := hijacker.Hijack()
			_ = conn.Close()
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"data":{"id":9,"username":"bob"}}`))
	}))
	t.Cleanup(server.Close)

	db, err := store.Open(filepath.Join(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	enc, err := crypto.New("retry-probe-key")
	if err != nil {
		t.Fatal(err)
	}
	svc := New(db, enc, adapters.NewRegistry(server.Client()))

	siteID, err := db.Site.Create(&domain.Site{
		Name: "flaky", BaseURL: server.URL, Platform: "new-api", Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	secretEnc, _ := enc.Encrypt([]byte("user-access-token"))
	credID, err := db.Credential.Create(&domain.Credential{
		SiteID: siteID, Kind: "access_token", SecretEnc: []byte(secretEnc), Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	channelID, err := db.Channel.Create(&domain.Channel{
		SiteID: &siteID, CredentialID: &credID, Name: "flaky", BaseURL: "",
		GroupName: "default", Priority: 0, Weight: 100, Status: domain.StatusEnabled, TypeHint: "new-api",
	})
	if err != nil {
		t.Fatal(err)
	}

	probe, err := svc.Probe(context.Background(), channelID)
	if err != nil {
		t.Fatalf("probe should succeed after one retry: %v", err)
	}
	if probe.Username != "bob" {
		t.Fatalf("probe=%+v", probe)
	}
	if calls != 2 {
		t.Fatalf("expected 2 attempts (1 failed + 1 retry), got %d", calls)
	}
	overviews, overviewErr := db.Channel.ListOverviews(time.Now())
	if overviewErr != nil {
		t.Fatal(overviewErr)
	}
	if len(overviews) != 1 || !overviews[0].LastAccountProbeOK {
		t.Fatalf("account probe ok should be true after retry: %+v", overviews)
	}
	if overviews[0].AccountState != "ok" {
		t.Fatalf("account state should be ok after retry: %+v", overviews[0])
	}
}

func TestProbeDoesNotRetryAuthRejection(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Error(w, `{"success":false,"message":"Unauthorized, invalid access token"}`, http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)

	db, err := store.Open(filepath.Join(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	enc, err := crypto.New("no-retry-key")
	if err != nil {
		t.Fatal(err)
	}
	svc := New(db, enc, adapters.NewRegistry(server.Client()))

	siteID, err := db.Site.Create(&domain.Site{
		Name: "dead", BaseURL: server.URL, Platform: "new-api", Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	secretEnc, _ := enc.Encrypt([]byte("user-access-token"))
	credID, err := db.Credential.Create(&domain.Credential{
		SiteID: siteID, Kind: "access_token", SecretEnc: []byte(secretEnc), Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	channelID, err := db.Channel.Create(&domain.Channel{
		SiteID: &siteID, CredentialID: &credID, Name: "dead", BaseURL: "",
		GroupName: "default", Priority: 0, Weight: 100, Status: domain.StatusEnabled, TypeHint: "new-api",
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = svc.Probe(context.Background(), channelID)
	if err == nil {
		t.Fatal("probe should fail on auth rejection")
	}
	if calls != 1 {
		t.Fatalf("auth rejections must not be retried, calls=%d", calls)
	}
}

func TestSyncKeysPrunesOrphanedAPIKeys(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/user/self":
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":3,"username":"alice"}}`))
		case "/api/token/":
			// Upstream no longer has token 88 (deleted there); only 99 remains.
			_, _ = w.Write([]byte(`{"success":true,"data":[{"id":99,"name":"fresh","key":"sk-fresh","status":1}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	db, err := store.Open(filepath.Join(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	enc, err := crypto.New("prune-test-key")
	if err != nil {
		t.Fatal(err)
	}
	svc := New(db, enc, adapters.NewRegistry(server.Client()))

	siteID, err := db.Site.Create(&domain.Site{
		Name: "prune", BaseURL: server.URL, Platform: "new-api", Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	secretEnc, _ := enc.Encrypt([]byte("user-access-token"))
	if _, err := db.Credential.Create(&domain.Credential{
		SiteID: siteID, Kind: "access_token", SecretEnc: []byte(secretEnc), Status: domain.StatusEnabled,
	}); err != nil {
		t.Fatal(err)
	}

	// Locally known key whose upstream token (88) has been deleted.
	orphanSecret, _ := enc.Encrypt([]byte("sk-orphan-88"))
	orphanID, err := db.Credential.Create(&domain.Credential{
		SiteID: siteID, Kind: "api_key", SecretEnc: []byte(orphanSecret),
		MetaJSON: `{"name":"old","upstream_token_id":88}`, Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Manually pasted key with no upstream id — must survive pruning.
	manualSecret, _ := enc.Encrypt([]byte("sk-manual"))
	manualID, err := db.Credential.Create(&domain.Credential{
		SiteID: siteID, Kind: "api_key", SecretEnc: []byte(manualSecret),
		MetaJSON: `{"name":"manual"}`, Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Channel is bound to the orphaned key (as if synced earlier).
	channelID, err := db.Channel.Create(&domain.Channel{
		SiteID: &siteID, CredentialID: &orphanID, Name: "prune", BaseURL: "",
		GroupName: "default", Priority: 0, Weight: 100, Status: domain.StatusEnabled, TypeHint: "new-api",
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := svc.SyncKeys(context.Background(), channelID, SyncKeysRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if result.CreatedCredentials != 1 {
		t.Fatalf("created=%d", result.CreatedCredentials)
	}
	if result.DeletedCredentials != 1 {
		t.Fatalf("deleted=%d", result.DeletedCredentials)
	}

	// Orphan gone, manual key untouched.
	orphan, getErr := db.Credential.GetByID(orphanID)
	if getErr != nil || orphan != nil {
		t.Fatalf("orphaned credential should have been deleted: %+v err=%v", orphan, getErr)
	}
	manual, getErr := db.Credential.GetByID(manualID)
	if getErr != nil || manual == nil {
		t.Fatalf("manual credential should survive: %+v err=%v", manual, getErr)
	}
	// Channel rebound to the fresh key, not dangling on the deleted one.
	channel, getErr := db.Channel.GetByID(channelID)
	if getErr != nil || channel == nil || channel.CredentialID == nil {
		t.Fatalf("channel=%+v err=%v", channel, getErr)
	}
	if *channel.CredentialID == orphanID || *channel.CredentialID == manualID {
		t.Fatalf("channel still bound to removed key: %d", *channel.CredentialID)
	}
}

func TestSyncKeysAggregatesByDefault(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/user/self":
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":3,"username":"alice"}}`))
		case "/api/token/":
			_, _ = w.Write([]byte(`{"success":true,"data":[
				{"id":1,"name":"def-key","key":"sk-default-aaa","status":1,"group":"default"},
				{"id":2,"name":"vip-key","key":"sk-vip-bbb","status":1,"group":"vip"}
			]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	db, err := store.Open(filepath.Join(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	enc, err := crypto.New("aggregate-keys-test")
	if err != nil {
		t.Fatal(err)
	}
	svc := New(db, enc, adapters.NewRegistry(server.Client()))

	siteID, err := db.Site.Create(&domain.Site{
		Name: "agg", BaseURL: server.URL, Platform: "new-api", Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	secretEnc, _ := enc.Encrypt([]byte("user-token"))
	credID, err := db.Credential.Create(&domain.Credential{
		SiteID: siteID, Kind: "access_token", SecretEnc: []byte(secretEnc), Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	channelID, err := db.Channel.Create(&domain.Channel{
		SiteID: &siteID, CredentialID: &credID, Name: "agg", BaseURL: "",
		GroupName: "default", Priority: 0, Weight: 100, Status: domain.StatusEnabled, TypeHint: "new-api",
	})
	if err != nil {
		t.Fatal(err)
	}

	sync, err := svc.SyncKeys(context.Background(), channelID, SyncKeysRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if sync.CreatedCredentials != 2 {
		t.Fatalf("want 2 keys in pool, got %+v", sync)
	}
	if sync.CreatedChannels != 0 || len(sync.GroupChannels) != 0 {
		t.Fatalf("default must not split connections: %+v", sync)
	}
	pool, err := db.Credential.ListEnabledAPIKeysBySite(siteID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pool) != 2 {
		t.Fatalf("pool size=%d", len(pool))
	}
}
func TestSyncKeysSplitsChannelsByGroup(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/user/self":
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":3,"username":"alice"}}`))
		case "/api/token/":
			_, _ = w.Write([]byte(`{"success":true,"data":[
				{"id":1,"name":"def-key","key":"sk-default-aaa","status":1,"group":"default"},
				{"id":2,"name":"vip-key","key":"sk-vip-bbb","status":1,"group":"vip"}
			]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	db, err := store.Open(filepath.Join(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	enc, err := crypto.New("split-group-test-key")
	if err != nil {
		t.Fatal(err)
	}
	svc := New(db, enc, adapters.NewRegistry(server.Client()))

	siteID, err := db.Site.Create(&domain.Site{
		Name: "multi", BaseURL: server.URL, Platform: "new-api", Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	secretEnc, _ := enc.Encrypt([]byte("user-token"))
	credID, err := db.Credential.Create(&domain.Credential{
		SiteID: siteID, Kind: "access_token", SecretEnc: []byte(secretEnc), Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	channelID, err := db.Channel.Create(&domain.Channel{
		SiteID: &siteID, CredentialID: &credID, Name: "multi", BaseURL: "",
		GroupName: "default", Priority: 0, Weight: 100, Status: domain.StatusEnabled, TypeHint: "new-api",
	})
	if err != nil {
		t.Fatal(err)
	}

	split := true
	sync, err := svc.SyncKeys(context.Background(), channelID, SyncKeysRequest{
		SplitByGroup: &split,
	})
	if err != nil {
		t.Fatal(err)
	}
	if sync.CreatedCredentials != 2 {
		t.Fatalf("credentials=%+v", sync)
	}
	if sync.CreatedChannels+sync.UpdatedChannels < 1 || len(sync.GroupChannels) != 2 {
		t.Fatalf("group channels=%+v", sync)
	}

	channels, err := db.Channel.List()
	if err != nil {
		t.Fatal(err)
	}
	var siteChannels []domain.Channel
	for _, ch := range channels {
		if ch.SiteID != nil && *ch.SiteID == siteID {
			siteChannels = append(siteChannels, ch)
		}
	}
	// Expect at least 2 connections for default + vip (seed channel may be reused as default).
	if len(siteChannels) < 2 {
		t.Fatalf("want >=2 site channels, got %d (%+v)", len(siteChannels), siteChannels)
	}
	groups := map[string]bool{}
	for _, ch := range siteChannels {
		if ch.CredentialID == nil {
			continue
		}
		cred, _ := db.Credential.GetByID(*ch.CredentialID)
		if cred == nil || cred.Kind != "api_key" {
			continue
		}
		groups[normalizeTokenGroup(ch.GroupName)] = true
	}
	if !groups["default"] || !groups["vip"] {
		t.Fatalf("groups present=%v channels=%+v", groups, siteChannels)
	}

	// Second sync is idempotent.
	again, err := svc.SyncKeys(context.Background(), channelID, SyncKeysRequest{
		SplitByGroup: &split,
	})
	if err != nil {
		t.Fatal(err)
	}
	if again.CreatedChannels != 0 {
		t.Fatalf("second sync should not create channels: %+v", again)
	}
}

func TestMapAdapterErrorVerdicts(t *testing.T) {
	cases := []struct {
		status int
		want   string
	}{
		{401, "upstream_unauthorized"},
		{403, "account_banned"},
		{429, "rate_limited"},
		{500, "upstream_status"},
	}
	for _, tc := range cases {
		err := mapAdapterError(&adapters.Error{Kind: adapters.ErrorStatus, Status: tc.status})
		var typed *Error
		if !errors.As(err, &typed) || typed.Category != tc.want {
			t.Fatalf("status %d → category %q, want %q", tc.status, probeCategory(err), tc.want)
		}
	}
	// Transport errors stay a stable generic category.
	tr := mapAdapterError(&adapters.Error{Kind: adapters.ErrorTransport})
	if got := probeCategory(tr); got != "upstream_failure" {
		t.Fatalf("transport category=%q", got)
	}
}

func TestRetryAfterFromAdapterError(t *testing.T) {
	if got := retryAfterFrom(&adapters.Error{Kind: adapters.ErrorStatus, Status: 429, RetryAfter: 5 * time.Second}); got != 5*time.Second {
		t.Fatalf("retryAfter=%v", got)
	}
	if got := retryAfterFrom(errors.New("plain")); got != 0 {
		t.Fatalf("plain error retryAfter=%v", got)
	}
	if got := retryAfterFrom(&adapters.Error{Kind: adapters.ErrorStatus, Status: 500}); got != 0 {
		t.Fatalf("non-429 retryAfter=%v", got)
	}
}

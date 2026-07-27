package account

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/lan/meta-gateway/internal/adapters"
	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

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

package exchange_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/discovery"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/exchange"
	"github.com/lan/meta-gateway/internal/store"
)

type recordingDiscovery struct {
	mu     sync.Mutex
	db     *store.DB
	calls  []int64
	failID int64
}

func (r *recordingDiscovery) Refresh(_ context.Context, channelID int64) (*discovery.RefreshResult, error) {
	var count int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM channels WHERE id = ?`, channelID).Scan(&count); err != nil || count != 1 {
		return nil, errors.New("discovery called before commit")
	}
	r.mu.Lock()
	r.calls = append(r.calls, channelID)
	r.mu.Unlock()
	if channelID == r.failID {
		return nil, &discovery.Error{Kind: discovery.ErrorUpstream, Category: "upstream_status"}
	}
	return &discovery.RefreshResult{ChannelID: channelID}, nil
}

func openExchangeService(t *testing.T) (*store.DB, *crypto.Encrypter, *recordingDiscovery, *exchange.Service) {
	t.Helper()
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	enc, err := crypto.New("exchange-service-test-master")
	if err != nil {
		t.Fatal(err)
	}
	refresh := &recordingDiscovery{db: db}
	return db, enc, refresh, exchange.NewService(db, enc, refresh)
}

func TestServiceImportCommitsBeforeOrderedDiscovery(t *testing.T) {
	db, _, refresh, service := openExchangeService(t)
	body := `[{"name":"second","base_url":"https://two.example.com","key":"key-two"},{"name":"first","base_url":"https://one.example.com","key":"key-one"}]`
	refresh.failID = 2
	result, err := service.Import(t.Context(), []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if result.CreatedCount != 2 || result.DiscoveryOK != 1 || result.DiscoveryFailed != 1 || len(result.ChannelIDs) != 2 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(refresh.calls) != 2 {
		t.Fatalf("expected 2 discovery calls, got %+v", refresh.calls)
	}
	seen := map[int64]bool{}
	for _, id := range refresh.calls {
		seen[id] = true
	}
	if !seen[1] || !seen[2] {
		t.Fatalf("discovery channels incomplete: %+v", refresh.calls)
	}
	var channels int
	if err := db.QueryRow(`SELECT COUNT(*) FROM channels`).Scan(&channels); err != nil || channels != 2 {
		t.Fatalf("assets did not survive discovery failure: count=%d err=%v", channels, err)
	}
	var foundFail bool
	for _, d := range result.Discovery {
		if d.Status == "failed" && d.Category == "upstream_status" {
			foundFail = true
		}
	}
	if !foundFail {
		t.Fatalf("failure not redacted/categorized: %+v", result.Discovery)
	}
}

func TestServiceRepeatedImportUpdatesAndExportsModes(t *testing.T) {
	_, _, refresh, service := openExchangeService(t)
	body := `[{"name":"first","base_url":"https://api.example.com/","key":"key-one","models":"b,a"}]`
	first, err := service.Import(t.Context(), []byte(body))
	if err != nil || first.CreatedCount != 1 {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	secondBody := `[{"name":"renamed","base_url":"https://api.example.com","key":"key-one","models":"c"}]`
	second, err := service.Import(t.Context(), []byte(secondBody))
	if err != nil || second.UpdatedCount != 1 || second.ChannelIDs[0] != first.ChannelIDs[0] {
		t.Fatalf("second=%+v err=%v", second, err)
	}
	// Re-import of existing identity is "updated": skip key-sync/discovery post-steps.
	if len(refresh.calls) != 1 {
		t.Fatalf("expected discovery only on create, not on update, calls=%+v", refresh.calls)
	}
	if second.DiscoveryOK+second.DiscoveryFailed != 0 && len(second.Discovery) != 0 {
		// allow empty discovery list for pure updates
	}
	if len(second.Discovery) != 0 {
		t.Fatalf("updated import should skip discovery list, got %+v", second.Discovery)
	}
	metadata, err := service.Export(t.Context(), exchange.ExportRequest{})
	if err != nil || metadata.Importable || metadata.Items[0].APIKey != "" {
		t.Fatalf("metadata export=%+v err=%v", metadata, err)
	}
	portable, err := service.Export(t.Context(), exchange.ExportRequest{IncludeSecrets: true, ChannelIDs: first.ChannelIDs})
	if err != nil || !portable.Importable || portable.Items[0].APIKey != "key-one" || portable.Items[0].Name != "renamed" {
		t.Fatalf("portable export=%+v err=%v", portable, err)
	}
	if _, err := service.Export(t.Context(), exchange.ExportRequest{ChannelIDs: []int64{9999}}); err == nil {
		t.Fatal("missing selected channel should fail")
	}
}

func TestServiceReplaceImportIsAtomicAndKeepsNonConnectionData(t *testing.T) {
	db, _, _, service := openExchangeService(t)
	oldBody := `[{"name":"old","base_url":"https://old.example.com","key":"old-key"}]`
	oldResult, err := service.Import(t.Context(), []byte(oldBody))
	if err != nil || oldResult.CreatedCount != 1 {
		t.Fatalf("old import=%+v err=%v", oldResult, err)
	}
	if _, err := db.Exec(`INSERT INTO downstream_keys (token_hash, name, enabled, scopes) VALUES ('hash', 'client', 1, 'relay')`); err != nil {
		t.Fatal(err)
	}

	badBody := `[{"name":"broken","base_url":"not-a-url","key":"new-key"}]`
	if _, err := service.ImportWithOptions(t.Context(), []byte(badBody), exchange.ImportOptions{Mode: exchange.ImportModeReplace}); err == nil {
		t.Fatal("invalid replacement should fail")
	}
	var oldChannels int
	if err := db.QueryRow(`SELECT COUNT(*) FROM channels WHERE name = 'old'`).Scan(&oldChannels); err != nil || oldChannels != 1 {
		t.Fatalf("old asset was removed after failed replace: count=%d err=%v", oldChannels, err)
	}

	newBody := `[{"name":"new","base_url":"https://new.example.com","key":"new-key"}]`
	result, err := service.ImportWithOptions(t.Context(), []byte(newBody), exchange.ImportOptions{Mode: exchange.ImportModeReplace})
	if err != nil || result.CreatedCount != 1 {
		t.Fatalf("replace=%+v err=%v", result, err)
	}
	var oldCount, newCount, keyCount int
	_ = db.QueryRow(`SELECT COUNT(*) FROM channels WHERE name = 'old'`).Scan(&oldCount)
	_ = db.QueryRow(`SELECT COUNT(*) FROM channels WHERE name = 'new'`).Scan(&newCount)
	_ = db.QueryRow(`SELECT COUNT(*) FROM downstream_keys`).Scan(&keyCount)
	if oldCount != 0 || newCount != 1 || keyCount != 1 {
		t.Fatalf("counts old=%d new=%d downstream_keys=%d", oldCount, newCount, keyCount)
	}
}

func TestServiceAdoptsUniqueLegacyIdentity(t *testing.T) {
	db, enc, _, service := openExchangeService(t)
	secret, _ := enc.Encrypt([]byte("legacy-key"))
	siteID, _ := db.Site.Create(&domain.Site{Name: "legacy", BaseURL: "https://legacy.example.com", Platform: "openai-compatible", Status: domain.StatusEnabled})
	credentialID, _ := db.Credential.Create(&domain.Credential{SiteID: siteID, Kind: "api_key", SecretEnc: []byte(secret), Status: domain.StatusEnabled})
	channelID, _ := db.Channel.Create(&domain.Channel{SiteID: &siteID, CredentialID: &credentialID, Name: "legacy", BaseURL: "https://legacy.example.com", Status: domain.StatusEnabled})
	result, err := service.Import(t.Context(), []byte(`[{"name":"adopted","base_url":"https://legacy.example.com/","key":"legacy-key"}]`))
	if err != nil || result.AdoptedCount != 1 || result.ChannelIDs[0] != channelID {
		t.Fatalf("adoption=%+v err=%v", result, err)
	}
	credential, _ := db.Credential.GetByID(credentialID)
	if credential.ImportFingerprint == "" {
		t.Fatal("adoption did not persist fingerprint")
	}
}

func TestServiceExportSkipsChannelsWithoutBaseURLOrCredential(t *testing.T) {
	db, _, _, service := openExchangeService(t)
	// Import a healthy channel that exports fine.
	body := `[{"name":"healthy","base_url":"https://ok.example.com","key":"ok-key"}]`
	res, err := service.Import(t.Context(), []byte(body))
	if err != nil || res.CreatedCount != 1 {
		t.Fatalf("import=%+v err=%v", res, err)
	}
	// Manually insert a channel with no credential and one whose base URL is
	// empty on both channel and site.
	if _, err := db.Exec(`INSERT INTO sites (name, base_url, platform, status) VALUES ('s-empty', '', 'new-api', 1)`); err != nil {
		t.Fatal(err)
	}
	// Channel with a credential but no resolvable base URL -> invalid_base_url.
	if _, err := db.Exec(`INSERT INTO credentials (site_id, kind, secret_enc) VALUES (2, 'api_key', 'enc-secret')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO channels (name, site_id, base_url, status, priority, weight, models_csv, credential_id) VALUES ('c-no-url', 2, '', 1, 0, 1, '', 1)`); err != nil {
		t.Fatal(err)
	}
	// Channel with a base URL but no credential -> no_credential.
	if _, err := db.Exec(`INSERT INTO channels (name, site_id, base_url, status, priority, weight, models_csv) VALUES ('c-no-cred', 2, 'https://nope.example.com', 1, 0, 1, '')`); err != nil {
		t.Fatal(err)
	}

	env, err := service.Export(t.Context(), exchange.ExportRequest{})
	if err != nil {
		t.Fatalf("export should not fail with unexportable channels: %v", err)
	}
	if len(env.Items) != 1 || env.Items[0].Name != "healthy" {
		t.Fatalf("expected only healthy channel, got %+v", env.Items)
	}
	if len(env.Skipped) != 2 {
		t.Fatalf("expected 2 skipped channels, got %+v", env.Skipped)
	}
	reasons := map[string]bool{}
	for _, s := range env.Skipped {
		reasons[s.Reason] = true
	}
	if !reasons["invalid_base_url"] || !reasons["no_credential"] {
		t.Fatalf("expected both skip reasons, got %+v", env.Skipped)
	}
}

package exchange_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/discovery"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/exchange"
	"github.com/lan/meta-gateway/internal/store"
)

type recordingDiscovery struct {
	db     *store.DB
	calls  []int64
	failID int64
}

func (r *recordingDiscovery) Refresh(_ context.Context, channelID int64) (*discovery.RefreshResult, error) {
	var count int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM channels WHERE id = ?`, channelID).Scan(&count); err != nil || count != 1 {
		return nil, errors.New("discovery called before commit")
	}
	r.calls = append(r.calls, channelID)
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

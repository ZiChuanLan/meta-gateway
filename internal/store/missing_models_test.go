package store_test

import (
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

func TestMissingModels(t *testing.T) {
	db := openTestDB(t)

	siteID, err := db.Site.Create(&domain.Site{Name: "s", BaseURL: "https://api.example.com", Platform: "openai-compatible", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	credID, err := db.Credential.Create(&domain.Credential{SiteID: siteID, Kind: "api_key", SecretEnc: []byte("enc"), Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	channelID, err := db.Channel.Create(&domain.Channel{
		SiteID: &siteID, CredentialID: credIDPtr(credID), Name: "ch",
		BaseURL: "https://api.example.com", TypeHint: "openai-compatible",
		Status: domain.StatusEnabled, ModelsCSV: "gpt-4o,gpt-5-missing,deepseek-v4-flash",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Routes: exact gpt-4o + wildcard deepseek-*.
	if _, err := db.Route.Create(&domain.Route{ModelPattern: "gpt-4o", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Route.Create(&domain.Route{ModelPattern: "deepseek-*", Enabled: true}); err != nil {
		t.Fatal(err)
	}

	missing, err := db.MissingModels()
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 1 {
		t.Fatalf("missing = %+v, want exactly gpt-5-missing", missing)
	}
	if missing[0].Model != "gpt-5-missing" || missing[0].ChannelID != channelID {
		t.Fatalf("missing[0] = %+v", missing[0])
	}
	if missing[0].Source != "models_csv" {
		t.Fatalf("source = %q", missing[0].Source)
	}
}

func TestMissingModelsIncludesDiscovered(t *testing.T) {
	db := openTestDB(t)

	siteID, _ := db.Site.Create(&domain.Site{Name: "s2", BaseURL: "https://api.example.com", Platform: "openai-compatible", Status: domain.StatusEnabled})
	credID, _ := db.Credential.Create(&domain.Credential{SiteID: siteID, Kind: "api_key", SecretEnc: []byte("enc"), Status: domain.StatusEnabled})
	channelID, _ := db.Channel.Create(&domain.Channel{
		SiteID: &siteID, CredentialID: credIDPtr(credID), Name: "ch2",
		BaseURL: "https://api.example.com", TypeHint: "openai-compatible",
		Status: domain.StatusEnabled,
	})

	// Discovery snapshot exposes a model with no route (inserted directly to
	// avoid Reconcile's auto-routing, which would create a covering route).
	if _, err := db.Exec(`INSERT INTO discovered_models (channel_id, model_name, available, source, latency_ms, checked_at) VALUES (?, 'only-discovered', 1, 'test', 0, datetime('now'))`, channelID); err != nil {
		t.Fatal(err)
	}

	missing, err := db.MissingModels()
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 1 || missing[0].Model != "only-discovered" {
		t.Fatalf("missing = %+v, want only-discovered", missing)
	}
	if missing[0].Source != "discovered" {
		t.Fatalf("source = %q", missing[0].Source)
	}
}

func TestMissingModelsEmptyWhenCovered(t *testing.T) {
	db := openTestDB(t)

	siteID, _ := db.Site.Create(&domain.Site{Name: "s3", BaseURL: "https://api.example.com", Platform: "openai-compatible", Status: domain.StatusEnabled})
	credID, _ := db.Credential.Create(&domain.Credential{SiteID: siteID, Kind: "api_key", SecretEnc: []byte("enc"), Status: domain.StatusEnabled})
	_, _ = db.Channel.Create(&domain.Channel{
		SiteID: &siteID, CredentialID: credIDPtr(credID), Name: "ch3",
		BaseURL: "https://api.example.com", TypeHint: "openai-compatible",
		Status: domain.StatusEnabled, ModelsCSV: "gpt-4o, gpt-4o-mini",
	})
	if _, err := db.Route.Create(&domain.Route{ModelPattern: "gpt-4o*", Enabled: true}); err != nil {
		t.Fatal(err)
	}

	missing, err := db.MissingModels()
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 0 {
		t.Fatalf("missing = %+v, want empty", missing)
	}
}

func credIDPtr(id int64) *int64 { return &id }

var _ = store.MissingModel{}

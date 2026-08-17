package store

import (
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
)

func TestFactoryResetClearsProcessCaches(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	siteID, err := db.Site.Create(&domain.Site{Name: "cached-site", BaseURL: "https://example.com", Platform: "openai-compatible", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Credential.Create(&domain.Credential{SiteID: siteID, Kind: "api_key", SecretEnc: []byte("enc"), Status: domain.StatusEnabled}); err != nil {
		t.Fatal(err)
	}
	keyID, err := db.DownstreamKey.Create(&domain.DownstreamKey{Name: "cached-key", TokenHash: "hash", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	_ = keyID
	if _, err := db.Group.Get("tenant"); err != nil {
		t.Fatal(err)
	}
	if err := db.ModelRatio.SetRatio("cached-model", 2); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Site.GetByID(siteID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Credential.GetByID(1); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DownstreamKey.GetByHash("hash"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.FactoryReset(); err != nil {
		t.Fatal(err)
	}
	if len(db.Site.byID) != 0 || len(db.Credential.byID) != 0 || len(db.Credential.bySiteKeys) != 0 || len(db.DownstreamKey.byID) != 0 || len(db.DownstreamKey.byHash) != 0 || len(db.Group.cache) != 0 || len(db.ModelRatio.cache) != 0 {
		t.Fatalf("stale caches after reset: sites=%d credentials=%d pools=%d keys=%d hashes=%d groups=%d ratios=%d", len(db.Site.byID), len(db.Credential.byID), len(db.Credential.bySiteKeys), len(db.DownstreamKey.byID), len(db.DownstreamKey.byHash), len(db.Group.cache), len(db.ModelRatio.cache))
	}
}

func TestSiteDeleteClearsCascadedCredentialCache(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	siteID, err := db.Site.Create(&domain.Site{Name: "delete-site", BaseURL: "https://example.com", Platform: "openai-compatible", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	credentialID, err := db.Credential.Create(&domain.Credential{SiteID: siteID, Kind: "api_key", SecretEnc: []byte("enc"), Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Credential.GetByID(credentialID); err != nil {
		t.Fatal(err)
	}
	if err := db.Site.Delete(siteID); err != nil {
		t.Fatal(err)
	}
	got, err := db.Credential.GetByID(credentialID)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("credential survived site cascade: %+v", got)
	}
}

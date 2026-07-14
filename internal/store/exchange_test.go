package store_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

func exchangeItem(name, fingerprint string) store.ExchangeImportItem {
	return store.ExchangeImportItem{Name: name, BaseURL: "https://api.example.com",
		ModelsCSV: "model-a", GroupName: "default", Priority: 1, Weight: 100,
		Status: domain.StatusEnabled, TypeHint: "openai-compatible",
		SecretEnc: "v1:cipher", Fingerprint: fingerprint}
}

func TestExchangeImportIsIdempotentAndSeparatesKeys(t *testing.T) {
	db := openTestDB(t)
	first, err := db.Exchange.Import(t.Context(), []store.ExchangeImportItem{exchangeItem("first", "fingerprint-one")})
	if err != nil || len(first.CreatedChannelIDs) != 1 {
		t.Fatalf("first import=%+v err=%v", first, err)
	}
	updatedItem := exchangeItem("renamed", "fingerprint-one")
	updatedItem.ModelsCSV = "model-b"
	second, err := db.Exchange.Import(t.Context(), []store.ExchangeImportItem{updatedItem})
	if err != nil || len(second.UpdatedChannelIDs) != 1 || second.UpdatedChannelIDs[0] != first.CreatedChannelIDs[0] {
		t.Fatalf("second import=%+v err=%v", second, err)
	}
	third, err := db.Exchange.Import(t.Context(), []store.ExchangeImportItem{exchangeItem("other-key", "fingerprint-two")})
	if err != nil || len(third.CreatedChannelIDs) != 1 {
		t.Fatalf("different key import=%+v err=%v", third, err)
	}
	var sites, credentials, channels int
	_ = db.QueryRow(`SELECT COUNT(*) FROM sites`).Scan(&sites)
	_ = db.QueryRow(`SELECT COUNT(*) FROM credentials`).Scan(&credentials)
	_ = db.QueryRow(`SELECT COUNT(*) FROM channels`).Scan(&channels)
	if sites != 1 || credentials != 2 || channels != 2 {
		t.Fatalf("counts sites=%d credentials=%d channels=%d", sites, credentials, channels)
	}
	channel, _ := db.Channel.GetByID(first.CreatedChannelIDs[0])
	if channel.Name != "renamed" || channel.ModelsCSV != "model-b" {
		t.Fatalf("mutable fields not updated: %+v", channel)
	}
}

func TestExchangeImportAdoptsOnlyDedicatedLegacyAsset(t *testing.T) {
	db := openTestDB(t)
	siteID, _ := db.Site.Create(&domain.Site{Name: "legacy", BaseURL: "https://api.example.com", Platform: "openai-compatible", Status: domain.StatusEnabled})
	credentialID, _ := db.Credential.Create(&domain.Credential{SiteID: siteID, Kind: "api_key", SecretEnc: []byte("v1:legacy"), Status: domain.StatusEnabled})
	channelID, _ := db.Channel.Create(&domain.Channel{SiteID: &siteID, CredentialID: &credentialID, Name: "legacy", BaseURL: "https://api.example.com", Status: domain.StatusEnabled})
	item := exchangeItem("adopted", "legacy-fingerprint")
	item.AdoptChannelID, item.AdoptCredentialID = channelID, credentialID
	result, err := db.Exchange.Import(t.Context(), []store.ExchangeImportItem{item})
	if err != nil || len(result.AdoptedChannelIDs) != 1 || result.AdoptedChannelIDs[0] != channelID {
		t.Fatalf("adopt result=%+v err=%v", result, err)
	}
	credential, _ := db.Credential.GetByID(credentialID)
	if credential.ImportFingerprint != "legacy-fingerprint" {
		t.Fatalf("fingerprint not persisted: %+v", credential)
	}
	serialized, _ := json.Marshal(credential)
	if string(serialized) == "" || contains(string(serialized), "legacy-fingerprint") {
		t.Fatalf("credential JSON leaked fingerprint: %s", serialized)
	}

	sharedChannel, _ := db.Channel.Create(&domain.Channel{SiteID: &siteID, CredentialID: &credentialID, Name: "shared", BaseURL: "https://api.example.com", Status: domain.StatusEnabled})
	_ = sharedChannel
	_, err = db.Exchange.Import(t.Context(), []store.ExchangeImportItem{exchangeItem("conflict", "legacy-fingerprint")})
	if !errors.Is(err, store.ErrExchangeConflict) {
		t.Fatalf("shared fingerprint identity should conflict, got %v", err)
	}
}

func TestExchangeImportRollsBackAllAssets(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec(`CREATE TRIGGER fail_exchange_channel BEFORE INSERT ON channels WHEN NEW.name = 'fail' BEGIN SELECT RAISE(ABORT, 'forced'); END`); err != nil {
		t.Fatal(err)
	}
	_, err := db.Exchange.Import(t.Context(), []store.ExchangeImportItem{
		exchangeItem("ok", "rollback-one"), exchangeItem("fail", "rollback-two"),
	})
	if err == nil {
		t.Fatal("expected forced import failure")
	}
	for _, table := range []string{"sites", "credentials", "channels"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s rollback count=%d err=%v", table, count, err)
		}
	}
}

func TestExchangeExportOrderingAndSelection(t *testing.T) {
	db := openTestDB(t)
	first, _ := db.Exchange.Import(t.Context(), []store.ExchangeImportItem{exchangeItem("first", "export-one")})
	secondItem := exchangeItem("second", "export-two")
	secondItem.BaseURL = "https://other.example.com"
	second, _ := db.Exchange.Import(t.Context(), []store.ExchangeImportItem{secondItem})
	rows, err := db.Exchange.Export(t.Context(), []int64{second.CreatedChannelIDs[0], first.CreatedChannelIDs[0]})
	if err != nil || len(rows) != 2 || rows[0].ChannelID != first.CreatedChannelIDs[0] || rows[1].ChannelID != second.CreatedChannelIDs[0] {
		t.Fatalf("ordered export=%+v err=%v", rows, err)
	}
	rows, err = db.Exchange.Export(t.Context(), []int64{999999})
	if err != nil || len(rows) != 0 {
		t.Fatalf("missing selection=%+v err=%v", rows, err)
	}
}

func contains(value, substring string) bool {
	for i := 0; i+len(substring) <= len(value); i++ {
		if value[i:i+len(substring)] == substring {
			return true
		}
	}
	return false
}

package store_test

import (
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

func TestFactoryResetWipesBusinessKeepsConfig(t *testing.T) {
	db := openTestDB(t)
	site, err := db.Site.Create(&domain.Site{Name: "s", BaseURL: "http://x", Platform: "openai-compatible", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Channel.Create(&domain.Channel{SiteID: &site, Name: "ch", BaseURL: "http://x", TypeHint: "openai-compatible", Status: domain.StatusEnabled}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DownstreamKey.Create(&domain.DownstreamKey{Name: "k", Scopes: "relay", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ProxyLog.Insert(&domain.ProxyLog{RequestID: "r1", Model: "m", ChannelID: 1, Status: 200, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	// Runtime settings override must survive.
	if err := db.RuntimeSettings.Save(&store.RuntimeSettingsRow{HasOverride: true, CheckinCron: "0 8 * * *", CheckinEnabled: true}); err != nil {
		t.Fatal(err)
	}

	deleted, err := db.FactoryReset()
	if err != nil {
		t.Fatal(err)
	}
	if deleted["channels"] != 1 || deleted["downstream_keys"] != 1 || deleted["proxy_logs"] != 1 {
		t.Fatalf("unexpected deleted counts: %v", deleted)
	}

	channels, _ := db.Channel.List()
	if len(channels) != 0 {
		t.Fatalf("channels not wiped: %d", len(channels))
	}
	keys, _ := db.DownstreamKey.List()
	if len(keys) != 0 {
		t.Fatalf("keys not wiped: %d", len(keys))
	}
	// Sites survive.
	sites, _ := db.Site.List()
	if len(sites) != 1 {
		t.Fatalf("sites must survive reset: %d", len(sites))
	}
	// Settings survive.
	settings, _ := db.RuntimeSettings.Get()
	if settings.CheckinCron != "0 8 * * *" {
		t.Fatalf("settings not preserved: %+v", settings)
	}
}

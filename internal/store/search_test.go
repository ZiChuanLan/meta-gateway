package store_test

import (
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
)

func ptr(v int64) *int64 { return &v }

func TestSearchGroupsAndLimits(t *testing.T) {
	db := openTestDB(t)
	// A site is required by the channel FK.
	site, err := db.Site.Create(&domain.Site{Name: "search-site", BaseURL: "http://example.com", Platform: "openai-compatible", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	// Seed one channel + one route + one key + one proxy log.
	if _, err := db.Channel.Create(&domain.Channel{
		SiteID: ptr(site), Name: "needle-channel", BaseURL: "http://needle.example.com",
		TypeHint: "openai-compatible", Status: domain.StatusEnabled,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Route.Create(&domain.Route{ModelPattern: "needle-model-v3", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DownstreamKey.Create(&domain.DownstreamKey{
		Name: "needle-key", Scopes: "relay", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ProxyLog.Insert(&domain.ProxyLog{
		RequestID: "needle-request-1", Model: "needle-model-v3",
		ChannelID: 1, Status: 200, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	hits, err := db.Search("needle", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits.Channels) != 1 || hits.Channels[0].Name != "needle-channel" {
		t.Fatalf("channels = %+v", hits.Channels)
	}
	if len(hits.Routes) != 1 || hits.Routes[0].Model != "needle-model-v3" || hits.Routes[0].Status != "enabled" {
		t.Fatalf("routes = %+v", hits.Routes)
	}
	if len(hits.Credentials) != 1 || hits.Credentials[0].Name != "needle-key" {
		t.Fatalf("credentials = %+v", hits.Credentials)
	}
	if len(hits.Logs) != 1 || hits.Logs[0].RequestID != "needle-request-1" {
		t.Fatalf("logs = %+v", hits.Logs)
	}

	// Case-insensitive substring.
	hits, err = db.Search("NEEDLE", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits.Channels) != 1 {
		t.Fatalf("case-insensitive channels = %d", len(hits.Channels))
	}

	// Empty term → empty groups.
	hits, err = db.Search("", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits.Channels)+len(hits.Routes)+len(hits.Credentials)+len(hits.Logs) != 0 {
		t.Fatalf("empty term returned hits: %+v", hits)
	}

	// Limit caps each group.
	for i := 0; i < 25; i++ {
		if _, err := db.Channel.Create(&domain.Channel{
			SiteID: ptr(site), Name: "mass-channel-" + string(rune('a'+i%26)) + string(rune('0'+i%10)),
			BaseURL: "http://mass.example.com", TypeHint: "openai-compatible", Status: domain.StatusEnabled,
		}); err != nil {
			t.Fatal(err)
		}
	}
	hits, err = db.Search("mass-channel-", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits.Channels) > 10 {
		t.Fatalf("channels not capped: %d", len(hits.Channels))
	}
}

package store_test

import (
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

func newsFixture(t *testing.T, db *store.DB, name string) int64 {
	t.Helper()
	siteID, err := db.Site.Create(&domain.Site{
		Name: name, BaseURL: "https://" + name + ".example",
		Platform: "new-api", Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	return siteID
}

// A site republishes its whole board on every call, so the second fetch must
// change nothing: no duplicate rows, and above all no new first_seen_at, which is
// the only "is this new to me" signal the console has.
func TestSiteAnnouncementsUpsertKeepsFirstSeen(t *testing.T) {
	db := openTestDB(t)
	siteID := newsFixture(t, db, "news-site")

	first := time.Date(2026, 10, 10, 6, 0, 0, 0, time.UTC)
	items := []store.SiteAnnouncement{
		{UpstreamID: "7", Content: "channel A is down", Kind: "default", PublishedAt: "2026-10-09T10:00:00Z"},
		{UpstreamID: "8", Content: "group renamed", Kind: "ongoing", PublishedAt: "2026-10-10T09:00:00Z"},
	}
	added, err := db.UpsertSiteAnnouncements(siteID, items, first)
	if err != nil {
		t.Fatal(err)
	}
	if added != 2 {
		t.Fatalf("added = %d, want 2", added)
	}

	// Second read: same ids, one edited text, one new date for the same id.
	second := first.Add(5 * time.Minute)
	items[0].Content = "channel A is back"
	items[0].PublishedAt = "2026-10-10T11:00:00Z"
	items = append(items, store.SiteAnnouncement{
		UpstreamID: "9", Content: "new notice", Kind: "default", PublishedAt: "2026-10-10T10:30:00Z",
	})
	added, err = db.UpsertSiteAnnouncements(siteID, items, second)
	if err != nil {
		t.Fatal(err)
	}
	if added != 1 {
		t.Fatalf("added = %d, want 1 (only id 9 is new)", added)
	}

	feed, err := db.SiteAnnouncements(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(feed) != 3 {
		t.Fatalf("feed = %d rows, want 3 (no duplicates)", len(feed))
	}
	// Newest published first: id 7 was edited to 11:00, then 9 at 10:30, then 8.
	if feed[0].UpstreamID != "7" || feed[1].UpstreamID != "9" || feed[2].UpstreamID != "8" {
		t.Fatalf("order = %s/%s/%s, want 7/9/8", feed[0].UpstreamID, feed[1].UpstreamID, feed[2].UpstreamID)
	}
	if feed[0].Content != "channel A is back" {
		t.Errorf("content = %q, want the updated text", feed[0].Content)
	}
	for _, item := range feed {
		// Rows 7 and 8 were already known; row 9 arrived with the second fetch,
		// so only it carries the later stamp.
		want := first.Format(time.RFC3339Nano)
		if item.UpstreamID == "9" {
			want = second.Format(time.RFC3339Nano)
		}
		if item.FirstSeenAt != want {
			t.Errorf("row %s first_seen = %s, want %s", item.UpstreamID, item.FirstSeenAt, want)
		}
		if item.SiteName != "news-site" {
			t.Errorf("site name = %q, want the joined name", item.SiteName)
		}
	}
}

// A reading with no readable date must not move an old announcement to the top
// of the feed, and it must not fail the insert either.
func TestSiteAnnouncementsKeepStoredDateWhenReadingHasNone(t *testing.T) {
	db := openTestDB(t)
	siteID := newsFixture(t, db, "undated-site")
	when := time.Date(2026, 10, 10, 6, 0, 0, 0, time.UTC)

	if _, err := db.UpsertSiteAnnouncements(siteID, []store.SiteAnnouncement{
		{UpstreamID: "1", Content: "hello", PublishedAt: "2026-09-01T00:00:00Z"},
	}, when); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpsertSiteAnnouncements(siteID, []store.SiteAnnouncement{
		{UpstreamID: "1", Content: "hello again", PublishedAt: ""},
	}, when.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	feed, err := db.SiteAnnouncements(5)
	if err != nil {
		t.Fatal(err)
	}
	if len(feed) != 1 {
		t.Fatalf("feed = %d rows, want 1", len(feed))
	}
	if feed[0].PublishedAt != "2026-09-01T00:00:00Z" {
		t.Errorf("published = %q, want the stored date kept", feed[0].PublishedAt)
	}
	if feed[0].Content != "hello again" {
		t.Errorf("content = %q, want the newer text", feed[0].Content)
	}
}

// Rows with no id cannot be deduped and rows with no text have nothing to show.
func TestSiteAnnouncementsSkipUnusableRows(t *testing.T) {
	db := openTestDB(t)
	siteID := newsFixture(t, db, "skip-site")
	added, err := db.UpsertSiteAnnouncements(siteID, []store.SiteAnnouncement{
		{Content: "no id", PublishedAt: "2026-10-01T00:00:00Z"},
		{UpstreamID: "4", Content: "   "},
		{UpstreamID: "5", Content: "kept", PublishedAt: "2026-10-01T00:00:00Z"},
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if added != 1 {
		t.Fatalf("added = %d, want 1", added)
	}
	reported, last, err := db.SiteNewsStatus()
	if err != nil {
		t.Fatal(err)
	}
	if reported != 1 || last == "" {
		t.Fatalf("status = (%d, %q), want one reported site and a fetch stamp", reported, last)
	}
}

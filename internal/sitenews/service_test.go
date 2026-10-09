package sitenews_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/sitenews"
	"github.com/lan/meta-gateway/internal/store"
)

// boardServer answers /api/status the way a New-API site does, including the
// nesting the announcement list actually arrives in.
func boardServer(t *testing.T, announcements []map[string]any) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/status" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"data":    map[string]any{"announcements": announcements},
		})
	}))
	t.Cleanup(server.Close)
	return server
}

func newAPISite(t *testing.T, db *store.DB, name, baseURL, platform string) int64 {
	t.Helper()
	id, err := db.Site.Create(&domain.Site{
		Name: name, BaseURL: baseURL, Platform: platform, Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// One round reads the New-API sites, stores what they published, and does not
// touch a platform whose board it cannot read.
func TestRefreshReadsNewAPISites(t *testing.T) {
	db := openNewsDB(t)
	server := boardServer(t, []map[string]any{
		{"id": 4, "content": "老黄渠道模型名取消重定向", "extra": "", "publishDate": "2026-08-25T02:37:07.959Z", "type": "ongoing"},
		{"id": 3, "content": "三星分组速率改了", "extra": "https://x666.me/notice/3", "publishDate": "2026-08-18T03:09:25.421Z", "type": "ongoing"},
	})
	newAPISite(t, db, "x666", server.URL, "new-api")
	newAPISite(t, db, "transit", "https://free.hiyo.top", "sub2api")

	service := sitenews.NewService(db, nil, nil)
	result, err := service.Refresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Sites != 1 {
		t.Fatalf("sites = %d, want 1 (the sub2api site publishes no board)", result.Sites)
	}
	if result.Fetched != 1 || result.Failed != 0 || result.Added != 2 {
		t.Fatalf("round = %+v, want 1 fetched / 0 failed / 2 added", result)
	}

	feed, err := service.Feed(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(feed.Items) != 2 {
		t.Fatalf("feed = %d items, want 2", len(feed.Items))
	}
	if feed.Items[0].UpstreamID != "4" || !strings.Contains(feed.Items[0].Content, "老黄渠道") {
		t.Errorf("first item = %+v, want the newest announcement", feed.Items[0])
	}
	if feed.Items[0].SiteName != "x666" {
		t.Errorf("site name = %q, want the joined name", feed.Items[0].SiteName)
	}
	if feed.Items[0].PublishedAt != "2026-08-25T02:37:07.959Z" {
		t.Errorf("published = %q, want the site's own timestamp", feed.Items[0].PublishedAt)
	}
	if feed.Sites.Readable != 1 || feed.Sites.Reported != 1 {
		t.Errorf("coverage = %+v, want one readable and one reporting site", feed.Sites)
	}
	if feed.LastFetched == "" {
		t.Error("last fetched is empty, want the round's stamp")
	}

	// The same board again: nothing is new, and nothing is duplicated.
	again, err := service.Refresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if again.Added != 0 {
		t.Fatalf("second round added %d, want 0", again.Added)
	}
	feed, err = service.Feed(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(feed.Items) != 2 {
		t.Fatalf("feed after re-read = %d items, want 2", len(feed.Items))
	}
}

// A site that fails is counted and named, and the other sites' notices still
// land: the round is exactly when an operator needs them most.
func TestRefreshToleratesAFailingSite(t *testing.T) {
	db := openNewsDB(t)
	server := boardServer(t, []map[string]any{{"id": 1, "content": "hello", "publishDate": "2026-10-01T00:00:00Z"}})
	newAPISite(t, db, "good-site", server.URL, "new-api")
	newAPISite(t, db, "dead-site", "http://127.0.0.1:1", "new-api")

	service := sitenews.NewService(db, nil, nil)
	result, err := service.Refresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Fetched != 1 || result.Failed != 1 {
		t.Fatalf("round = %+v, want one fetched and one failed", result)
	}
	if len(result.Errors) != 1 || !strings.Contains(result.Errors[0], "dead-site") {
		t.Fatalf("errors = %v, want the failing site named", result.Errors)
	}
	feed, err := service.Feed(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(feed.Items) != 1 || feed.Items[0].SiteName != "good-site" {
		t.Fatalf("feed = %+v, want the reachable site's notice", feed.Items)
	}
}

// A site whose /api/status carries no announcement list is not an error: most
// forks ship the endpoint without the feature enabled.
func TestRefreshTreatsAnEmptyBoardAsSuccess(t *testing.T) {
	db := openNewsDB(t)
	server := boardServer(t, nil)
	newAPISite(t, db, "quiet-site", server.URL, "new-api")

	service := sitenews.NewService(db, nil, nil)
	result, err := service.Refresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Fetched != 1 || result.Failed != 0 || result.Added != 0 {
		t.Fatalf("round = %+v, want one fetched, nothing added, nothing failed", result)
	}
}

// A reading with no readable date must not stamp "now": that would push an old
// announcement to the top of the feed on every fetch.
func TestRefreshKeepsTheStoredDateWhenTheReadingHasNone(t *testing.T) {
	db := openNewsDB(t)
	server := boardServer(t, []map[string]any{
		{"id": 1, "content": "undated notice", "publishDate": "whenever"},
	})
	newAPISite(t, db, "dated-site", server.URL, "new-api")

	service := sitenews.NewService(db, nil, nil)
	if _, err := service.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	first, err := service.Feed(5)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 1 || first.Items[0].PublishedAt == "" {
		t.Fatalf("feed = %+v, want one dated row", first.Items)
	}

	// A second read must leave that date alone.
	if _, err := service.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	second, err := service.Feed(5)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 {
		t.Fatalf("feed = %d rows, want 1", len(second.Items))
	}
	if second.Items[0].PublishedAt != first.Items[0].PublishedAt {
		t.Errorf("published = %q, want the stored %q kept", second.Items[0].PublishedAt, first.Items[0].PublishedAt)
	}
}

func openNewsDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.OpenTest(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

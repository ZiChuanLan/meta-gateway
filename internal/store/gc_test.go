package store_test

import (
	"strings"
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
)

// TestGCDeletesOrphansAndKeepsValidRows verifies orphan cleanup across the
// FK-bearing tables and that valid rows survive.
func TestGCDeletesOrphansAndKeepsValidRows(t *testing.T) {
	db := openTestDB(t)
	// FK off so orphan rows can exist (they arise from backups/restores or
	// older versions that did not enforce FKs); GC must sweep them.
	if _, err := db.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatal(err)
	}
	site, err := db.Site.Create(&domain.Site{Name: "gc-site", BaseURL: "http://example.com", Platform: "openai-compatible", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := db.Channel.Create(&domain.Channel{
		SiteID: ptr(site), Name: "gc-ch", BaseURL: "http://gc.example.com",
		TypeHint: "openai-compatible", Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	routeID, err := db.Route.Create(&domain.Route{ModelPattern: "gc-model", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.RouteMember.Create(&domain.RouteMember{RouteID: routeID, ChannelID: ch, Priority: 1, Weight: 100, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	// Orphans pointing at deleted parents (direct SQL: FK is off).
	if _, err := db.Exec(`INSERT INTO route_members (route_id, channel_id, priority, weight, enabled, cooldown_until, created_at, updated_at) VALUES (9999, 9999, 1, 100, 1, '', '', '')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO proxy_logs (request_id, channel_id, route_id, model, status, created_at) VALUES ('orphan-log', 9999, 9999, 'x', 200, datetime('now'))`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO channel_health_history (channel_id, ok, latency_ms, verdict, probed_at) VALUES (9999, 0, 5, 'timeout', datetime('now'))`); err != nil {
		t.Fatal(err)
	}

	res, err := db.GC()
	if err != nil {
		t.Fatal(err)
	}
	if res.RouteMembers < 1 {
		t.Fatalf("route_members deleted = %d, want >= 1", res.RouteMembers)
	}
	if res.ProxyLogs != 1 {
		t.Fatalf("proxy_logs deleted = %d, want 1", res.ProxyLogs)
	}
	if res.HealthHistory != 1 {
		t.Fatalf("health_history deleted = %d, want 1", res.HealthHistory)
	}
	// Valid rows survive.
	members, err := db.RouteMember.ListByRoute(routeID)
	if err != nil || len(members) != 1 {
		t.Fatalf("valid member lost: %v %v", members, err)
	}
}

// TestGCVacuumThreshold verifies VACUUM only fires above the freelist
// threshold and reports freed bytes.
func TestGCVacuumThreshold(t *testing.T) {
	db := openTestDB(t)
	// Churn: create + delete rows to build a freelist. proxy_logs rows carry
	// a 2KB error payload so deletion frees many pages.
	payload := strings.Repeat("x", 2048)
	for i := 0; i < 600; i++ {
		if _, err := db.Exec(`INSERT INTO proxy_logs (request_id, channel_id, route_id, model, status, error_brief, created_at) VALUES (?, 1, 1, 'churn', 500, ?, datetime('now'))`, "churn-"+itoa(i), payload); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 600; i++ {
		if _, err := db.Exec(`DELETE FROM proxy_logs WHERE request_id = ?`, "churn-"+itoa(i)); err != nil {
			t.Fatal(err)
		}
	}
	res, err := db.GC()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("freelist=%d page_size=%d vacuumed=%v freed=%d", res.FreelistPages, res.PageSize, res.Vacuumed, res.VacuumFreedBytes)
	// 600 deleted rows with 2KB payloads free well over the 256-page
	// threshold; VACUUM must have run and compacted the file.
	if !res.Vacuumed {
		t.Fatalf("expected vacuum, freelist=%d", res.FreelistPages)
	}
	if res.FreelistPages > 64 {
		t.Fatalf("freelist after vacuum = %d, want small", res.FreelistPages)
	}
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	digits := []byte{}
	for v > 0 {
		digits = append([]byte{byte('0' + v%10)}, digits...)
		v /= 10
	}
	return string(digits)
}

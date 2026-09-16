package store_test

import (
	"strings"
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

func seedLogs(t *testing.T, db *store.DB) {
	t.Helper()
	rows := []domain.ProxyLog{
		{RequestID: "req-alpha-001", Model: "gemini-2.5-flash", Status: 200, ErrorBrief: "", Path: "/v1/chat/completions"},
		{RequestID: "req-beta-002", Model: "grok-imagine-image-edit", Status: 400, ErrorBrief: "upstream_error: 图片编辑模型请使用 /v1/images/edits", Path: "/v1/images/edits"},
		{RequestID: "req-gamma-003", Model: "gpt-image-2", Status: 200, ErrorBrief: "", ErrorDetail: "quota exhausted for the day", Path: "/v1/images/generations"},
	}
	for i := range rows {
		if _, err := db.ProxyLog.Insert(&rows[i]); err != nil {
			t.Fatalf("insert log: %v", err)
		}
	}
}

func TestBuildLogFTSMatch(t *testing.T) {
	cases := map[string]string{
		"gemini":        `"gemini"*`,
		"gemini flash":  `"gemini"* AND "flash"*`,
		`bad"*()input:`: `"bad""*()input:"*`,
		"   ":           "",
		"":              "",
		"req-alpha-001": `"req-alpha-001"*`,
	}
	for in, want := range cases {
		if got := store.BuildLogFTSMatch(in); got != want {
			t.Errorf("BuildLogFTSMatch(%q) = %q, want %q", in, got, want)
		}
	}
}

// The point of the index: find a log by a fragment of its error text or model
// name without a full table scan.
func TestProxyLogFullTextSearch(t *testing.T) {
	db := openTestDB(t)
	seedLogs(t, db)

	find := func(q string) []string {
		t.Helper()
		rows, err := db.ProxyLog.ListFilter(store.ProxyLogFilter{Query: q, Limit: 50})
		if err != nil {
			t.Fatalf("search %q: %v", q, err)
		}
		out := []string{}
		for _, row := range rows {
			out = append(out, row.RequestID)
		}
		return out
	}

	if got := find("gemini"); len(got) != 1 || got[0] != "req-alpha-001" {
		t.Errorf("model prefix search = %v", got)
	}
	if got := find("quota"); len(got) != 1 || got[0] != "req-gamma-003" {
		t.Errorf("error detail search = %v", got)
	}
	if got := find("images/edits"); len(got) != 1 || got[0] != "req-beta-002" {
		t.Errorf("path search = %v", got)
	}
	if got := find("nothing-matches-this"); len(got) != 0 {
		t.Errorf("expected no hits, got %v", got)
	}
	// Empty query is not a filter.
	if got := find(""); len(got) != 3 {
		t.Errorf("empty query should return everything, got %v", got)
	}
}

// Search must compose with the existing filters rather than replace them.
func TestProxyLogSearchComposesWithFilters(t *testing.T) {
	db := openTestDB(t)
	seedLogs(t, db)

	failed := true
	rows, err := db.ProxyLog.ListFilter(store.ProxyLogFilter{Query: "req", FailedOnly: failed, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].RequestID != "req-beta-002" {
		t.Errorf("search + failedOnly = %+v", rows)
	}

	rows, err = db.ProxyLog.ListFilter(store.ProxyLogFilter{Query: "req", Model: "gpt-image-2", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].RequestID != "req-gamma-003" {
		t.Errorf("search + model = %+v", rows)
	}
}

func TestProxyLogSearchComposesWithSiteFilter(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		name := "fts"
		if fallback {
			name = "like"
		}
		t.Run(name, func(t *testing.T) {
			db := openTestDB(t)
			if fallback {
				if _, err := db.Exec(`CREATE TABLE proxy_logs_fts (a TEXT)`); err != nil {
					t.Fatal(err)
				}
			}
			siteID, err := db.Site.Create(&domain.Site{Name: "search-site", Status: domain.StatusEnabled})
			if err != nil {
				t.Fatal(err)
			}
			channelID, err := db.Channel.Create(&domain.Channel{Name: "search-channel", SiteID: &siteID, Status: domain.StatusEnabled})
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range []domain.ProxyLog{
				{RequestID: "matching", ChannelID: channelID, Model: "gemini-2.5-flash", Status: 200},
				{RequestID: "wrong-model", ChannelID: channelID, Model: "gpt-image-2", Status: 200},
				{RequestID: "other-site", Model: "gemini-2.5-flash", Status: 200},
			} {
				if _, err := db.ProxyLog.Insert(&row); err != nil {
					t.Fatal(err)
				}
			}
			rows, err := db.ProxyLog.ListFilter(store.ProxyLogFilter{Query: "gemini", SiteID: &siteID, Limit: 50})
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 1 || rows[0].RequestID != "matching" {
				t.Fatalf("search + site returned %+v", rows)
			}
			if !fallback {
				var indexed int
				if err := db.QueryRow(`SELECT COUNT(*) FROM proxy_logs_fts WHERE proxy_logs_fts MATCH 'gemini'`).Scan(&indexed); err != nil {
					t.Fatalf("FTS path must be exercised: %v", err)
				}
				if indexed != 2 {
					t.Fatalf("FTS indexed %d rows, want 2", indexed)
				}
			}
		})
	}
}

func TestProxyLogSearchFindsLiteralIdentifiers(t *testing.T) {
	db := openTestDB(t)
	seedLogs(t, db)
	for _, tc := range []struct{ query, requestID string }{
		{"req-alpha-001", "req-alpha-001"},
		{"gemini-2.5-flash", "req-alpha-001"},
		{"grok-imagine-image-edit", "req-beta-002"},
	} {
		rows, err := db.ProxyLog.ListFilter(store.ProxyLogFilter{Query: tc.query})
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 || rows[0].RequestID != tc.requestID {
			t.Errorf("search %q returned %+v", tc.query, rows)
		}
	}
}

func TestProxyLogLikeSearchTreatsWildcardsLiterally(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec(`CREATE TABLE proxy_logs_fts (a TEXT)`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{`req\_literal%`, "req-other"} {
		if _, err := db.ProxyLog.Insert(&domain.ProxyLog{RequestID: id, Status: 200}); err != nil {
			t.Fatal(err)
		}
	}
	for _, query := range []string{`\_literal%`, `\`, "%", "_"} {
		rows, err := db.ProxyLog.ListFilter(store.ProxyLogFilter{Query: query})
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 || rows[0].RequestID != `req\_literal%` {
			t.Errorf("literal %q returned %+v", query, rows)
		}
	}
}

// Syntax-hostile input must not surface as a 500: tokens are quoted before
// reaching MATCH, so operators lose their meaning.
func TestProxyLogSearchSurvivesHostileInput(t *testing.T) {
	db := openTestDB(t)
	seedLogs(t, db)
	for _, q := range []string{
		`"`, `*`, `:*`, `AND OR NOT`, `(`,
		`gemini AND (`, `%`, `_`, `'`, "gemini\x00flash", "req\"alpha",
		strings.Repeat("a", 300),
	} {
		if _, err := db.ProxyLog.ListFilter(store.ProxyLogFilter{Query: q, Limit: 10}); err != nil {
			t.Errorf("query %q returned error: %v", q, err)
		}
	}
}

// Like fallback: when FTS5 is unavailable the same query still works, just via
// substring scan. We force the branch by keeping the cached availability false.
func TestProxyLogSearchLikeFallback(t *testing.T) {
	db := openTestDB(t)
	seedLogs(t, db)
	// Simulate a build without FTS5 by occupying the index name with an
	// ordinary table: the virtual-table creation then fails and the availability
	// check downgrades to the LIKE branch for this database only.
	if _, err := db.DB.Exec(`CREATE TABLE proxy_logs_fts (a TEXT)`); err != nil {
		t.Skipf("cannot force fallback path: %v", err)
	}
	rows, err := db.ProxyLog.ListFilter(store.ProxyLogFilter{Query: "quota", Limit: 50})
	if err != nil {
		t.Fatalf("like fallback: %v", err)
	}
	if len(rows) != 1 || rows[0].RequestID != "req-gamma-003" {
		t.Errorf("like fallback = %+v", rows)
	}
}

// Availability is cached per database handle: a second database must build its
// own index instead of inheriting the first one's answer.
func TestLogFTSAvailabilityIsPerDatabase(t *testing.T) {
	first := openTestDB(t)
	if _, err := first.ProxyLog.ListFilter(store.ProxyLogFilter{Query: "gemini"}); err != nil {
		t.Fatalf("first db: %v", err)
	}
	second := openTestDB(t)
	seedLogs(t, second)
	rows, err := second.ProxyLog.ListFilter(store.ProxyLogFilter{Query: "gemini", Limit: 10})
	if err != nil {
		t.Fatalf("second db: %v", err)
	}
	if len(rows) != 1 || rows[0].RequestID != "req-alpha-001" {
		t.Errorf("second db search = %+v", rows)
	}
}

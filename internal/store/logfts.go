package store

import (
	"database/sql"
	"log"
	"strings"
	"sync"
	"unicode"
)

// Full-text search over proxy_logs.
//
// The index is an external-content FTS5 table: it stores no copy of the text,
// it points at proxy_logs by rowid and is kept in step by triggers. That keeps
// the database small and means a rebuild is just `INSERT INTO
// proxy_logs_fts(proxy_logs_fts) VALUES('rebuild')`.
//
// The whole thing is best-effort on purpose. FTS5 is a compile-time option —
// if this SQLite build lacks it, creating the virtual table fails, and a failed
// migration would put the container in a crash loop. So the DDL lives here
// rather than in a numbered migration, and a failure downgrades search to LIKE
// instead of taking the process down.
const (
	logFTSCreate = `CREATE VIRTUAL TABLE IF NOT EXISTS proxy_logs_fts USING fts5(
		model, error_brief, error_detail, path, request_id, upstream_request_id, upstream_url,
		content='proxy_logs', content_rowid='id'
	)`

	logFTSInsertTrigger = `CREATE TRIGGER IF NOT EXISTS proxy_logs_fts_ai AFTER INSERT ON proxy_logs BEGIN
		INSERT INTO proxy_logs_fts(rowid, model, error_brief, error_detail, path, request_id, upstream_request_id, upstream_url)
		VALUES (new.id, new.model, new.error_brief, new.error_detail, new.path, new.request_id, new.upstream_request_id, new.upstream_url);
	END`

	logFTSDeleteTrigger = `CREATE TRIGGER IF NOT EXISTS proxy_logs_fts_ad AFTER DELETE ON proxy_logs BEGIN
		INSERT INTO proxy_logs_fts(proxy_logs_fts, rowid, model, error_brief, error_detail, path, request_id, upstream_request_id, upstream_url)
		VALUES ('delete', old.id, old.model, old.error_brief, old.error_detail, old.path, old.request_id, old.upstream_request_id, old.upstream_url);
	END`

	logFTSUpdateTrigger = `CREATE TRIGGER IF NOT EXISTS proxy_logs_fts_au AFTER UPDATE ON proxy_logs BEGIN
		INSERT INTO proxy_logs_fts(proxy_logs_fts, rowid, model, error_brief, error_detail, path, request_id, upstream_request_id, upstream_url)
		VALUES ('delete', old.id, old.model, old.error_brief, old.error_detail, old.path, old.request_id, old.upstream_request_id, old.upstream_url);
		INSERT INTO proxy_logs_fts(rowid, model, error_brief, error_detail, path, request_id, upstream_request_id, upstream_url)
		VALUES (new.id, new.model, new.error_brief, new.error_detail, new.path, new.request_id, new.upstream_request_id, new.upstream_url);
	END`
)

// LogFTSState caches the answer per database handle. It lives on the log store
// rather than in a package global because a process may open more than one
// database (tests, migrations, side-by-side instances), and a single global
// would report "available" for a handle whose index was never created.
type LogFTSState struct {
	mu    sync.Mutex
	ready *bool
}

// Available reports whether the FTS5 index is usable, creating it on first
// call. Safe to call concurrently; the answer is cached per state.
func (s *LogFTSState) Available(db *sql.DB) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ready != nil {
		return *s.ready
	}
	ok := ensureLogFTS(db)
	s.ready = &ok
	return ok
}

func ensureLogFTS(db *sql.DB) bool {
	if !logFTSHasColumn(db, "upstream_url") {
		// The indexed column list changed (proxy_logs.upstream_url). An FTS5
		// table cannot gain a column in place, and the insert triggers now
		// reference one: leaving the old table would make every log write fail.
		// Drop table + triggers once and let the DDL and the rebuild below
		// recreate and repopulate them. Both statements are no-ops on a fresh
		// database (nothing to drop).
		for _, ddl := range []string{
			`DROP TRIGGER IF EXISTS proxy_logs_fts_ai`,
			`DROP TRIGGER IF EXISTS proxy_logs_fts_ad`,
			`DROP TRIGGER IF EXISTS proxy_logs_fts_au`,
			`DROP TABLE IF EXISTS proxy_logs_fts`,
		} {
			if _, err := db.Exec(ddl); err != nil {
				log.Printf("store: log full-text search disabled: %v", err)
				return false
			}
		}
	}
	for _, ddl := range []string{logFTSCreate, logFTSInsertTrigger, logFTSDeleteTrigger, logFTSUpdateTrigger} {
		if _, err := db.Exec(ddl); err != nil {
			log.Printf("store: log full-text search disabled: %v", err)
			return false
		}
	}
	// Tables created before the index existed are invisible to it until a
	// rebuild; do it once on first successful setup so history is searchable.
	if _, err := db.Exec(`INSERT INTO proxy_logs_fts(proxy_logs_fts) VALUES('rebuild')`); err != nil {
		log.Printf("store: log full-text index rebuild failed: %v", err)
		return false
	}
	return true
}

// logFTSHasColumn reports whether the existing full-text table carries a column.
// A missing table (fresh database) answers false, which makes the caller drop
// nothing before creating it.
func logFTSHasColumn(db *sql.DB, column string) bool {
	rows, err := db.Query(`PRAGMA table_info(proxy_logs_fts)`)
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid     int
			name    string
			type_   string
			notNull int
			dflt    sql.NullString
			pk      int
		)
		if err := rows.Scan(&cid, &name, &type_, &notNull, &dflt, &pk); err != nil {
			return false
		}
		if name == column {
			return true
		}
	}
	return false
}

// BuildLogFTSMatch turns free text into a safe FTS5 MATCH expression.
//
// Raw user input cannot be handed to MATCH: characters like `*`, `:`, `^` and
// unbalanced quotes are query syntax, and a syntax error surfaces as a 500 on
// the logs page. So every token is quoted (killing operator meaning) and given
// a prefix wildcard, which is what makes "gem" find "gemini-2.5-flash".
func BuildLogFTSMatch(query string) string {
	tokens := []string{}
	for _, field := range strings.FieldsFunc(query, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) {
		if !strings.ContainsFunc(field, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) }) {
			continue
		}
		// Let the same FTS tokenizer handle punctuation in both the indexed
		// text and the query. Removing '-' would turn req-alpha into reqalpha,
		// which can never match the indexed identifier. Doubled quotes escape
		// the only delimiter inside an FTS string literal.
		tokens = append(tokens, `"`+strings.ReplaceAll(field, `"`, `""`)+`"*`)
	}
	if len(tokens) == 0 {
		return ""
	}
	return strings.Join(tokens, " AND ")
}

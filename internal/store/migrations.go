package store

import (
	"database/sql"
	"embed"
	"fmt"
	"sort"
	"strings"
)

//go:embed *.sql
var migrationFS embed.FS

// Migrate applies all embedded SQL migrations in order.
// Files are sorted by name; naming convention: 001_name.sql, 002_name.sql, etc.
func Migrate(db *sql.DB) error {
	entries, err := migrationFS.ReadDir(".")
	if err != nil {
		return fmt.Errorf("store: read migrations: %w", err)
	}

	// Collect .sql files and sort by name.
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	for _, name := range files {
		content, err := migrationFS.ReadFile(name)
		if err != nil {
			return fmt.Errorf("store: read %s: %w", name, err)
		}
		// Split on semicolons for multi-statement files.
		statements := splitStatements(string(content))
		for _, stmt := range statements {
			stmt = strings.TrimSpace(stmt)
			if stmt == "" {
				continue
			}
			if _, err := db.Exec(stmt); err != nil {
				return fmt.Errorf("store: migrate %s: %w\nSQL: %s", name, err, stmt)
			}
		}
	}
	return nil
}

func splitStatements(s string) []string {
	var result []string
	for _, part := range strings.Split(s, ";") {
		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}

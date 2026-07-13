package store

import (
	"database/sql"
	"strings"
	"time"
)

// parseSQLiteTime converts SQLite datetime/text values into time.Time.
func parseSQLiteTime(v any) (time.Time, error) {
	switch t := v.(type) {
	case time.Time:
		return t, nil
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return time.Time{}, nil
		}
		layouts := []string{
			time.RFC3339Nano,
			time.RFC3339,
			"2006-01-02 15:04:05.999999999-07:00",
			"2006-01-02 15:04:05.999999999",
			"2006-01-02 15:04:05",
			"2006-01-02T15:04:05",
		}
		var err error
		for _, layout := range layouts {
			var parsed time.Time
			parsed, err = time.ParseInLocation(layout, s, time.UTC)
			if err == nil {
				return parsed, nil
			}
		}
		return time.Time{}, err
	case []byte:
		return parseSQLiteTime(string(t))
	case nil:
		return time.Time{}, nil
	default:
		return time.Time{}, sql.ErrNoRows
	}
}

func scanTime(dest *time.Time) interface{} {
	return (*sqliteTime)(dest)
}

type sqliteTime time.Time

func (t *sqliteTime) Scan(src any) error {
	parsed, err := parseSQLiteTime(src)
	if err != nil {
		// tolerate unparsable empty-ish values
		if src == nil {
			*t = sqliteTime(time.Time{})
			return nil
		}
		if s, ok := src.(string); ok && strings.TrimSpace(s) == "" {
			*t = sqliteTime(time.Time{})
			return nil
		}
		return err
	}
	*t = sqliteTime(parsed)
	return nil
}

func scanNullTime(dest **time.Time) interface{} {
	return &nullSQLiteTime{dest: dest}
}

type nullSQLiteTime struct {
	dest **time.Time
}

func (n *nullSQLiteTime) Scan(src any) error {
	if src == nil {
		*n.dest = nil
		return nil
	}
	if s, ok := src.(string); ok && strings.TrimSpace(s) == "" {
		*n.dest = nil
		return nil
	}
	parsed, err := parseSQLiteTime(src)
	if err != nil {
		return err
	}
	if parsed.IsZero() {
		*n.dest = nil
		return nil
	}
	*n.dest = &parsed
	return nil
}

package store

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// DB wraps the *sql.DB connection and all CRUD repositories.
type DB struct {
	*sql.DB
	Site          *SiteStore
	Credential    *CredentialStore
	Channel       *ChannelStore
	Route         *RouteStore
	RouteMember   *RouteMemberStore
	DownstreamKey *DownstreamKeyStore
	ProxyLog      *ProxyLogStore
}

// Open opens (or creates) the SQLite database at the given path and runs migrations.
func Open(dataDir string) (*DB, error) {
	dsn := fmt.Sprintf("file:%s/meta-gateway.db?cache=shared&_journal_mode=WAL&_busy_timeout=5000", dataDir)
	sqldb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}
	sqldb.SetMaxOpenConns(1)

	if err := sqldb.Ping(); err != nil {
		return nil, fmt.Errorf("store: ping: %w", err)
	}
	if err := Migrate(sqldb); err != nil {
		return nil, fmt.Errorf("store: migrate: %w", err)
	}

	db := &DB{
		DB:            sqldb,
		Site:          &SiteStore{db: sqldb},
		Credential:    &CredentialStore{db: sqldb},
		Channel:       &ChannelStore{db: sqldb},
		Route:         &RouteStore{db: sqldb},
		RouteMember:   &RouteMemberStore{db: sqldb},
		DownstreamKey: &DownstreamKeyStore{db: sqldb},
		ProxyLog:      &ProxyLogStore{db: sqldb},
	}
	return db, nil
}

// Close closes the underlying SQLite connection.
func (db *DB) Close() error {
	return db.DB.Close()
}

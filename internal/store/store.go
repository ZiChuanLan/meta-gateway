package store

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// DB wraps the *sql.DB connection and all CRUD repositories.
type DB struct {
	*sql.DB
	Site            *SiteStore
	Credential      *CredentialStore
	Channel         *ChannelStore
	DiscoveredModel *DiscoveredModelStore
	Route           *RouteStore
	RouteMember     *RouteMemberStore
	DownstreamKey   *DownstreamKeyStore
	ProxyLog        *ProxyLogStore
	CheckinLog      *CheckinLogStore
	Exchange        *ExchangeStore
	AuditEvent      *AuditEventStore
	BackupRecord    *BackupRecordStore
	Plugin          *PluginStore
	WebDAVSettings  *WebDAVSettingsStore
	AdminTOTP       *AdminTOTPStore
	ModelMetadata   *ModelMetadataStore
	ErrorRule       *ErrorPassRuleStore
	HealthHistory   *HealthHistoryStore
	AlertRule       *AlertRuleStore
	PromptGuard     *PromptGuardStore
	RuntimeSettings *RuntimeSettingsStore
	Usage           *UsageStore
	ModelRatio      *ModelRatioStore
	Group           *GroupStore
}

// DefaultMaxOpenConns is the SQLite connection-pool ceiling used when no
// explicit value is provided. WAL mode allows concurrent readers alongside a
// single writer, so a small pool (rather than 1) removes the serialization
// bottleneck on hot paths while keeping write contention bounded by
// busy_timeout.
const DefaultMaxOpenConns = 4

// Open opens (or creates) the SQLite database at the given path and runs
// migrations, using DefaultMaxOpenConns.
func Open(dataDir string) (*DB, error) {
	return OpenWithMaxConns(dataDir, DefaultMaxOpenConns)
}

// OpenWithMaxConns opens (or creates) the SQLite database at the given path
// and runs migrations with an explicit connection-pool ceiling. The pool must
// be at least 1; values outside 1..16 are clamped defensively.
func OpenWithMaxConns(dataDir string, maxOpenConns int) (*DB, error) {
	if maxOpenConns < 1 {
		maxOpenConns = 1
	}
	if maxOpenConns > 16 {
		maxOpenConns = 16
	}
	dsn := fmt.Sprintf("file:%s/meta-gateway.db?cache=shared&_journal_mode=WAL&_busy_timeout=5000&_pragma=foreign_keys(1)", dataDir)
	sqldb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}
	sqldb.SetMaxOpenConns(maxOpenConns)

	if err := sqldb.Ping(); err != nil {
		return nil, fmt.Errorf("store: ping: %w", err)
	}
	if err := Migrate(sqldb); err != nil {
		return nil, fmt.Errorf("store: migrate: %w", err)
	}

	db := &DB{
		DB:              sqldb,
		Site:            newSiteStore(sqldb),
		Credential:      newCredentialStore(sqldb),
		Channel:         &ChannelStore{db: sqldb},
		DiscoveredModel: &DiscoveredModelStore{db: sqldb},
		Route:           &RouteStore{db: sqldb},
		RouteMember:     &RouteMemberStore{db: sqldb},
		DownstreamKey:   newDownstreamKeyStore(sqldb),
		ProxyLog:        &ProxyLogStore{db: sqldb},
		CheckinLog:      &CheckinLogStore{db: sqldb},
		AuditEvent:      &AuditEventStore{db: sqldb},
		BackupRecord:    &BackupRecordStore{db: sqldb},
		Plugin:          &PluginStore{db: sqldb},
		WebDAVSettings:  &WebDAVSettingsStore{db: sqldb},
		AdminTOTP:       &AdminTOTPStore{db: sqldb},
		ModelMetadata:   &ModelMetadataStore{db: sqldb},
		ErrorRule:       &ErrorPassRuleStore{db: sqldb},
		HealthHistory:   &HealthHistoryStore{db: sqldb},
		AlertRule:       &AlertRuleStore{db: sqldb},
		PromptGuard:     &PromptGuardStore{db: sqldb},
		RuntimeSettings: &RuntimeSettingsStore{db: sqldb},
		Usage:           &UsageStore{db: sqldb},
		ModelRatio:      newModelRatioStore(sqldb),
		Group:           newGroupStore(sqldb),
	}
	// Exchange needs the full DB handle to clear the site/credential caches
	// after direct-SQL imports.
	db.Exchange = &ExchangeStore{db: db}
	return db, nil
}

// Close closes the underlying SQLite connection.
func (db *DB) Close() error {
	return db.DB.Close()
}

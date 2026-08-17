package store

import (
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

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
	dsn, err := sqliteDSN(dataDir)
	if err != nil {
		return nil, fmt.Errorf("store: database path: %w", err)
	}
	sqldb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}
	sqldb.SetMaxOpenConns(maxOpenConns)

	// sql.Open is lazy. Keep the handle closed on every initialization failure;
	// otherwise a failed migration can leak a pool and its file descriptors.
	initialized := false
	defer func() {
		if !initialized {
			_ = sqldb.Close()
		}
	}()
	if err := sqldb.Ping(); err != nil {
		return nil, fmt.Errorf("store: ping: %w", err)
	}
	if err := Migrate(sqldb); err != nil {
		return nil, fmt.Errorf("store: migrate: %w", err)
	}

	siteStore := newSiteStore(sqldb)
	credentialStore := newCredentialStore(sqldb)
	siteStore.onDelete = credentialStore.ClearCache
	db := &DB{
		DB:              sqldb,
		Site:            siteStore,
		Credential:      credentialStore,
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
	initialized = true
	return db, nil
}

// sqliteDSN builds a URI understood by modernc.org/sqlite. The driver uses
// repeated _pragma query parameters; the legacy _journal_mode/_busy_timeout
// names are silently ignored. URI construction also matters on Windows (and
// for paths containing spaces, '#', or '?').
func sqliteDSN(dataDir string) (string, error) {
	path, err := filepath.Abs(filepath.Join(dataDir, "meta-gateway.db"))
	if err != nil {
		return "", err
	}
	path = filepath.ToSlash(path)
	// A Windows volume path needs a leading slash for a canonical file URI:
	// file:///C:/... . On Unix filepath.Abs already supplies one.
	if filepath.VolumeName(path) != "" && !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	u := &url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Set("cache", "shared")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(1)")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// Close closes the underlying SQLite connection.
func (db *DB) Close() error {
	return db.DB.Close()
}

CREATE TABLE audit_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    request_id TEXT NOT NULL DEFAULT '',
    actor_kind TEXT NOT NULL,
    actor_id INTEGER,
    action TEXT NOT NULL,
    resource_kind TEXT NOT NULL DEFAULT '',
    resource_id INTEGER,
    outcome TEXT NOT NULL,
    status_code INTEGER NOT NULL,
    category TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_audit_events_created_at ON audit_events(created_at DESC, id DESC);
CREATE INDEX idx_audit_events_action ON audit_events(action, id DESC);

CREATE TABLE backup_records (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    status TEXT NOT NULL,
    size_bytes INTEGER NOT NULL DEFAULT 0,
    checksum TEXT NOT NULL DEFAULT '',
    duration_ms INTEGER NOT NULL DEFAULT 0,
    category TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_backup_records_created_at ON backup_records(created_at DESC, id DESC);

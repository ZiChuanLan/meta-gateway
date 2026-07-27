CREATE TABLE IF NOT EXISTS plugins (
    id TEXT PRIMARY KEY,
    version TEXT NOT NULL,
    status TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 0,
    source TEXT NOT NULL DEFAULT '',
    checksum TEXT NOT NULL DEFAULT '',
    installed_at TEXT,
    enabled_at TEXT,
    meta_json TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_plugins_enabled ON plugins(enabled);

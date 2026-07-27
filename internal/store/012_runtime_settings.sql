-- Admin-editable runtime parameters (single row). NULL fields mean "use env bootstrap".
CREATE TABLE IF NOT EXISTS runtime_settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    has_override INTEGER NOT NULL DEFAULT 0,
    retry_times INTEGER,
    cooldown_seconds INTEGER,
    checkin_enabled INTEGER,
    checkin_cron TEXT,
    relay_rate_per_minute INTEGER,
    relay_rate_burst INTEGER,
    admin_rate_per_minute INTEGER,
    admin_rate_burst INTEGER,
    audit_retention_days INTEGER,
    audit_retention_rows INTEGER,
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

INSERT OR IGNORE INTO runtime_settings (id, has_override)
VALUES (1, 0);

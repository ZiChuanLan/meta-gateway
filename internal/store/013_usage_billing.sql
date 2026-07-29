-- Simple usage metering and per-key token quotas.
ALTER TABLE downstream_keys ADD COLUMN quota_total_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE downstream_keys ADD COLUMN quota_used_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE downstream_keys ADD COLUMN price_prompt_per_1k REAL NOT NULL DEFAULT 0;
ALTER TABLE downstream_keys ADD COLUMN price_completion_per_1k REAL NOT NULL DEFAULT 0;

ALTER TABLE proxy_logs ADD COLUMN downstream_key_id INTEGER NOT NULL DEFAULT 0;
ALTER TABLE proxy_logs ADD COLUMN prompt_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE proxy_logs ADD COLUMN completion_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE proxy_logs ADD COLUMN total_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE proxy_logs ADD COLUMN stream INTEGER NOT NULL DEFAULT 0;
ALTER TABLE proxy_logs ADD COLUMN path TEXT NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS usage_records (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    request_id TEXT NOT NULL DEFAULT '',
    downstream_key_id INTEGER NOT NULL DEFAULT 0,
    channel_id INTEGER NOT NULL DEFAULT 0,
    model TEXT NOT NULL DEFAULT '',
    path TEXT NOT NULL DEFAULT '',
    stream INTEGER NOT NULL DEFAULT 0,
    prompt_tokens INTEGER NOT NULL DEFAULT 0,
    completion_tokens INTEGER NOT NULL DEFAULT 0,
    total_tokens INTEGER NOT NULL DEFAULT 0,
    status INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_usage_records_key_created ON usage_records(downstream_key_id, created_at);
CREATE INDEX IF NOT EXISTS idx_usage_records_created ON usage_records(created_at);
CREATE INDEX IF NOT EXISTS idx_proxy_logs_downstream_key ON proxy_logs(downstream_key_id);

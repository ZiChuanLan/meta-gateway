CREATE TABLE IF NOT EXISTS discovered_models (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    channel_id INTEGER NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    model_name TEXT NOT NULL,
    available INTEGER NOT NULL DEFAULT 1,
    source TEXT NOT NULL DEFAULT '',
    latency_ms INTEGER NOT NULL DEFAULT 0,
    checked_at TEXT NOT NULL,
    UNIQUE(channel_id, model_name)
);

CREATE INDEX IF NOT EXISTS idx_discovered_models_channel_available
    ON discovered_models(channel_id, available, model_name);
CREATE INDEX IF NOT EXISTS idx_discovered_models_checked_at
    ON discovered_models(checked_at);

-- 137_site_probe_evidence_history.sql
--
-- Two reads the console's availability views were missing.
--
-- 1. site_probe_samples had no index on observed_at, so "what did this site
--    publish over the last day" was a full scan of the samples table (measured:
--    261k rows, ~0.8s) — the panel that draws one tick per collection needs the
--    window, not the table.
--
-- 2. The third-party directory's readings were kept as a snapshot only: one row
--    per (site, model) replaced on every sync. A snapshot can say 97.1% today,
--    but it cannot say whether it said 60% last night, so the console had one
--    number and no history next to sources that do have one.

CREATE INDEX IF NOT EXISTS idx_site_probe_samples_time ON site_probe_samples(observed_at);

CREATE TABLE IF NOT EXISTS site_probe_external_history (
    id INTEGER PRIMARY KEY,
    site_id INTEGER NOT NULL DEFAULT 0,
    source TEXT NOT NULL DEFAULT '',
    raw_model TEXT NOT NULL,
    group_name TEXT NOT NULL DEFAULT '',
    ratio REAL NOT NULL DEFAULT 0,
    request_count INTEGER NOT NULL DEFAULT 0,
    success_count INTEGER NOT NULL DEFAULT 0,
    failure_count INTEGER NOT NULL DEFAULT 0,
    observed_at TEXT NOT NULL DEFAULT '',
    -- collected_at is when this gateway pulled the reading, and bucket_at is that
    -- moment truncated to the hour: one row per pair per hour, so a sync that
    -- repeats itself does not stack the directory's rolling-window counts.
    collected_at TEXT NOT NULL DEFAULT (datetime('now')),
    bucket_at TEXT NOT NULL DEFAULT '',
    UNIQUE(site_id, raw_model, bucket_at)
);
CREATE INDEX IF NOT EXISTS idx_site_probe_external_history_model
    ON site_probe_external_history(site_id, raw_model, bucket_at DESC);
-- The console reads the history of a window, in order; without this the read is a
-- scan of everything the retention window holds.
CREATE INDEX IF NOT EXISTS idx_site_probe_external_history_time
    ON site_probe_external_history(collected_at);

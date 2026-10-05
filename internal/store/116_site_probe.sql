-- External site probe source: read a public-benefit site's own probe data
-- (Uptime Kuma status page) and/or its public price table (New-API
-- /api/pricing) instead of spending upstream tokens on real completions.
--
-- Design notes (see docs/site-probe-source.md):
--   * The configuration lives on sites, not channels: one site = one probe
--     page, and several channels (keys) of that site share the same data.
--     It also keeps channels' three hand-written projections untouched.
--   * Samples are stored per collection round so the "N consecutive rounds
--     below threshold" verdict can be recomputed from history instead of
--     being kept in a third piece of mutable state.
--   * Nothing here is written to model_health/probe_results: those tables mean
--     "a real completion was sent", which is exactly what this feature avoids.

ALTER TABLE sites ADD COLUMN probe_source_kind TEXT NOT NULL DEFAULT '';
ALTER TABLE sites ADD COLUMN probe_source_url TEXT NOT NULL DEFAULT '';
ALTER TABLE sites ADD COLUMN probe_source_config TEXT NOT NULL DEFAULT '';
ALTER TABLE sites ADD COLUMN probe_source_enabled INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sites ADD COLUMN probe_last_run_at TEXT;
ALTER TABLE sites ADD COLUMN probe_last_error TEXT NOT NULL DEFAULT '';

-- One collection round per site.
CREATE TABLE IF NOT EXISTS site_probe_runs (
    id INTEGER PRIMARY KEY,
    site_id INTEGER NOT NULL,
    source_kind TEXT NOT NULL DEFAULT '',
    started_at TEXT NOT NULL,
    finished_at TEXT,
    status TEXT NOT NULL,             -- running | ok | failed
    monitor_count INTEGER NOT NULL DEFAULT 0,
    error TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_site_probe_runs_site ON site_probe_runs(site_id, id DESC);

-- One row per monitor (model) per round: the raw ratio the site reports,
-- plus the raw price quote when the source publishes one.
CREATE TABLE IF NOT EXISTS site_probe_samples (
    id INTEGER PRIMARY KEY,
    run_id INTEGER NOT NULL,
    site_id INTEGER NOT NULL,
    monitor_id TEXT NOT NULL DEFAULT '',
    monitor_name TEXT NOT NULL,
    monitor_type TEXT NOT NULL DEFAULT '',   -- http | keyword | ''
    group_name TEXT NOT NULL DEFAULT '',
    raw_model TEXT NOT NULL,
    observed_at TEXT NOT NULL,
    samples INTEGER NOT NULL DEFAULT 0,      -- denominator, pending/maintenance excluded
    up_count INTEGER NOT NULL DEFAULT 0,
    ratio REAL NOT NULL DEFAULT 0,
    avg_ping_ms INTEGER,
    weak_evidence INTEGER NOT NULL DEFAULT 0,
    -- Price observation (public price table), stored raw + normalized.
    price_mode TEXT NOT NULL DEFAULT '',     -- '' | token | fixed
    price_currency TEXT NOT NULL DEFAULT '',
    price_input_usd_per_million REAL NOT NULL DEFAULT 0,
    price_output_usd_per_million REAL NOT NULL DEFAULT 0,
    price_cache_read_usd_per_million REAL NOT NULL DEFAULT 0,
    price_per_request_usd REAL NOT NULL DEFAULT 0,
    price_group_ratio REAL NOT NULL DEFAULT 0,
    price_raw TEXT NOT NULL DEFAULT '',
    price_unparsed INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_site_probe_samples_run ON site_probe_samples(run_id);
CREATE INDEX IF NOT EXISTS idx_site_probe_samples_model ON site_probe_samples(site_id, raw_model, id DESC);

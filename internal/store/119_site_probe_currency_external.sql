-- Two follow-ups from the first week of the site probe: the published amounts
-- are NOT always USD, and most sites publish no health at all.
--
-- 1. Currency. A site's price table is denominated in the currency the site
--    declares (New-API's quota_display_type): measured, happycoding declares USD
--    while 南梁 declares CNY, and the same formula produces the amount in that
--    currency. The columns claimed "usd" and the console printed "$" for a
--    ¥ amount, which is the kind of quiet lie that ends up in someone's cost
--    model, so the names now say what they mean and the symbol travels along.
--
-- 2. Third-party health. The gateway cannot read a per-model availability from
--    any public site endpoint (verified: /api/models/status answers 401,
--    /api/ratio_config 403, /api/uptime 404, /healthz says only "I am up").
--    A monitoring directory publishes an aggregate per (site, model) instead;
--    those readings are stored here, labelled third-party everywhere, and are
--    only consulted when neither the site nor our own relay has anything to say.

ALTER TABLE site_probe_samples RENAME COLUMN price_input_usd_per_million TO price_input_per_million;
ALTER TABLE site_probe_samples RENAME COLUMN price_output_usd_per_million TO price_output_per_million;
ALTER TABLE site_probe_samples RENAME COLUMN price_cache_read_usd_per_million TO price_cache_read_per_million;
ALTER TABLE site_probe_samples RENAME COLUMN price_per_request_usd TO price_per_request;
ALTER TABLE site_probe_samples ADD COLUMN price_currency_symbol TEXT NOT NULL DEFAULT '';

-- One row per (source, site, model, group) of the latest external snapshot. The
-- table is replaced wholesale on every sync: it is a snapshot, not a history,
-- and the history that matters (our own readings) lives in the probe tables.
CREATE TABLE IF NOT EXISTS site_probe_external (
    id INTEGER PRIMARY KEY,
    source TEXT NOT NULL,
    site_id INTEGER NOT NULL,
    site_name TEXT NOT NULL DEFAULT '',
    site_host TEXT NOT NULL DEFAULT '',
    raw_model TEXT NOT NULL,
    group_name TEXT NOT NULL DEFAULT '',
    ratio REAL NOT NULL DEFAULT 0,
    avg_latency_ms INTEGER NOT NULL DEFAULT 0,
    first_token_ms INTEGER NOT NULL DEFAULT 0,
    tokens_per_second REAL NOT NULL DEFAULT 0,
    service_state TEXT NOT NULL DEFAULT '',
    acquisition_state TEXT NOT NULL DEFAULT '',
    observed_at TEXT NOT NULL DEFAULT '',
    collected_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_site_probe_external_site ON site_probe_external(site_id, raw_model);

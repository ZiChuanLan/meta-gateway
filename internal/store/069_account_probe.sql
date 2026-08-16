-- Account-layer probe results, kept separate from business probes so an
-- access_token/session failure can never overwrite last_probe_* (api_key chain).
-- health_state stays driven by the business probe; account_state is derived
-- from these columns and rendered independently in the UI.
ALTER TABLE channels ADD COLUMN last_account_probe_at TEXT;
ALTER TABLE channels ADD COLUMN last_account_probe_ok INTEGER NOT NULL DEFAULT 0;
ALTER TABLE channels ADD COLUMN last_account_probe_error TEXT NOT NULL DEFAULT '';

-- Probe-history kind dimension: 'ping' (network layer) / 'probe' (business
-- model probe) / 'account' (access_token/session probe). Availability stats
-- aggregate all kinds by default so the badge and the curve share one source.
ALTER TABLE channel_health_history ADD COLUMN kind TEXT NOT NULL DEFAULT 'probe';
CREATE INDEX IF NOT EXISTS idx_health_history_kind ON channel_health_history (kind);
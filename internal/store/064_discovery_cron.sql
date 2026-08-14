-- Scheduled model-list refresh: a five-field cron expression (same format
-- as checkin_cron; empty = disabled) that re-runs discovery.RefreshAll on
-- schedule, keeping channel model lists fresh without manual intervention.
ALTER TABLE runtime_settings ADD COLUMN discovery_cron TEXT NOT NULL DEFAULT '';

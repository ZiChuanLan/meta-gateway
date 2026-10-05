-- Site-probe collection cadence.
--
-- The 15-minute default shipped with the feature is the cadence of the *slowest*
-- public status page we read (Uptime Kuma keeps ~100 heartbeats per monitor, so a
-- site polling every 15 minutes publishes one new beat per round). Sites that poll
-- their upstreams every 60 seconds need a much faster round to be useful at all,
-- so the cadence is a setting rather than a constant.
--
-- Columns are nullable on purpose: NULL means "not overridden" and resolves to the
-- environment bootstrap, exactly like the health-sweep columns in 040. A NOT NULL
-- default would make an existing override row silently zero the new values.

ALTER TABLE runtime_settings ADD COLUMN site_probe_interval_seconds INTEGER;
ALTER TABLE runtime_settings ADD COLUMN site_probe_jitter_seconds INTEGER;

-- Per-route retry overrides. NULL = follow the global runtime setting.
ALTER TABLE routes ADD COLUMN retry_times INTEGER;
ALTER TABLE routes ADD COLUMN channel_retry_times INTEGER;

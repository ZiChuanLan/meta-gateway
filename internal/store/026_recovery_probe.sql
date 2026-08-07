-- Passive-recovery probe settings for the runtime settings document.
-- NULL = not overridden yet (falls back to env bootstrap).
ALTER TABLE runtime_settings ADD COLUMN recovery_probe_enabled INTEGER;
ALTER TABLE runtime_settings ADD COLUMN recovery_probe_interval_seconds INTEGER;

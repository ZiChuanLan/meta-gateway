-- Independent failure thresholds + frontend-configurable sticky sessions.
-- NULL = not overridden (env bootstrap).
ALTER TABLE runtime_settings ADD COLUMN model_breaker_fail_count INTEGER;
ALTER TABLE runtime_settings ADD COLUMN key_fail_threshold INTEGER;
ALTER TABLE runtime_settings ADD COLUMN sticky_enabled INTEGER;
ALTER TABLE runtime_settings ADD COLUMN sticky_ttl_minutes INTEGER;

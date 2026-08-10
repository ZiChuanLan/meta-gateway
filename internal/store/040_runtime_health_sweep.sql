-- Frontend-configurable channel health sweep (hot reload).
-- NULL = not overridden (env bootstrap).
ALTER TABLE runtime_settings ADD COLUMN health_sweep_enabled INTEGER;
ALTER TABLE runtime_settings ADD COLUMN health_sweep_interval_seconds INTEGER;
ALTER TABLE runtime_settings ADD COLUMN health_sweep_jitter_seconds INTEGER;
ALTER TABLE runtime_settings ADD COLUMN health_sweep_degraded_ms INTEGER;
ALTER TABLE runtime_settings ADD COLUMN health_sweep_concurrency INTEGER;
ALTER TABLE runtime_settings ADD COLUMN health_sweep_timeout_seconds INTEGER;

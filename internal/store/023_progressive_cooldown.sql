-- Progressive (tiered) cooldown settings for the runtime settings document.
-- NULL = not overridden yet (falls back to env bootstrap at apply time).
ALTER TABLE runtime_settings ADD COLUMN progressive_cooldown_enabled INTEGER;
ALTER TABLE runtime_settings ADD COLUMN cooldown_level2_seconds INTEGER;
ALTER TABLE runtime_settings ADD COLUMN cooldown_level3_seconds INTEGER;
ALTER TABLE runtime_settings ADD COLUMN cooldown_level4_seconds INTEGER;
ALTER TABLE runtime_settings ADD COLUMN breaker_fail_count INTEGER;

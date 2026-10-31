-- Runtime switch for the image-write failover escape hatch. NULL preserves the
-- env bootstrap (IMAGES_FAILOVER_ENABLED, default off) for override rows created
-- before this setting existed.
ALTER TABLE runtime_settings ADD COLUMN images_failover_enabled INTEGER;

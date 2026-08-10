-- Runtime switch for cross-channel failover. NULL preserves env bootstrap for
-- existing Admin override rows created before this setting existed.
ALTER TABLE runtime_settings ADD COLUMN cross_channel_failover_enabled INTEGER;

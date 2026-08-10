-- Frontend-configurable same-key retry count (hot reload).
-- NULL = not overridden (env bootstrap).
ALTER TABLE runtime_settings ADD COLUMN channel_retry_times INTEGER;

-- Admin-configured default sync mode for newly created channels.
-- Existing channels keep their own model_sync_mode; only channel creation
-- consults this value when the request omits model_sync_mode.
ALTER TABLE runtime_settings ADD COLUMN default_model_sync_mode TEXT NOT NULL DEFAULT 'manual';

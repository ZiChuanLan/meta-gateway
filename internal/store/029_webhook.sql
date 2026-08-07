-- Operational webhook notifications: endpoint URL and throttle window.
-- NULL = not overridden yet (falls back to env bootstrap).
ALTER TABLE runtime_settings ADD COLUMN webhook_url TEXT;
ALTER TABLE runtime_settings ADD COLUMN webhook_throttle_seconds INTEGER;

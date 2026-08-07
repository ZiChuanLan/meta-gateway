-- Error-propensity-aware routing switch for the runtime settings document.
-- NULL = not overridden yet (falls back to env bootstrap).
ALTER TABLE runtime_settings ADD COLUMN routing_error_aware INTEGER;

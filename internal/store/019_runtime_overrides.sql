-- Runtime-editable channel auto-disable threshold and latency-aware routing.
ALTER TABLE runtime_settings ADD COLUMN channel_auto_disable_threshold INTEGER;
ALTER TABLE runtime_settings ADD COLUMN routing_latency_aware INTEGER;

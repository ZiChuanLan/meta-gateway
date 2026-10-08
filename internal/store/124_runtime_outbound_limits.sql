-- Outbound client limits become runtime settings.
--
-- These were environment-only, which meant the one knob an operator reaches for
-- during an incident — the response-header ceiling that decides whether a slow
-- image edit dies at 60s — needed a .env edit and a container recreate. The
-- console could only report the value in "deployment parameters".
--
-- 0 keeps the meaning it has everywhere else in this table: no override, use the
-- deployment default (OUTBOUND_* in the environment).
ALTER TABLE runtime_settings ADD COLUMN outbound_connect_timeout_seconds INTEGER NOT NULL DEFAULT 0;
ALTER TABLE runtime_settings ADD COLUMN outbound_header_timeout_seconds INTEGER NOT NULL DEFAULT 0;
ALTER TABLE runtime_settings ADD COLUMN outbound_image_header_timeout_seconds INTEGER NOT NULL DEFAULT 0;
ALTER TABLE runtime_settings ADD COLUMN outbound_tls_timeout_seconds INTEGER NOT NULL DEFAULT 0;
ALTER TABLE runtime_settings ADD COLUMN outbound_max_idle_conns INTEGER NOT NULL DEFAULT 0;
ALTER TABLE runtime_settings ADD COLUMN outbound_max_idle_conns_per_host INTEGER NOT NULL DEFAULT 0;

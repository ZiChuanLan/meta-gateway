CREATE UNIQUE INDEX IF NOT EXISTS idx_routes_model_pattern_unique ON routes(model_pattern);
CREATE UNIQUE INDEX IF NOT EXISTS idx_route_members_route_channel_unique ON route_members(route_id, channel_id);
CREATE INDEX IF NOT EXISTS idx_route_members_route_enabled_priority ON route_members(route_id, enabled, priority DESC);
CREATE INDEX IF NOT EXISTS idx_route_members_cooldown ON route_members(cooldown_until);
CREATE INDEX IF NOT EXISTS idx_channels_credential ON channels(credential_id);
CREATE INDEX IF NOT EXISTS idx_credentials_site ON credentials(site_id);
ALTER TABLE proxy_logs ADD COLUMN attempt INTEGER NOT NULL DEFAULT 1;

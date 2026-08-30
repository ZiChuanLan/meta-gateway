-- Route groups: a route may carry several member groups, each with its own
-- priority ordering (the same channel can appear in multiple groups at
-- different priorities). Existing members land in the built-in 'default'
-- group; API keys with an empty route_group_name use each route's default.
ALTER TABLE route_members ADD COLUMN group_name TEXT NOT NULL DEFAULT 'default';

-- The uniqueness of a plain (mapping-less) member binding is now per group:
-- (route_id, channel_id) may repeat across groups, but not within one.
DROP INDEX IF EXISTS idx_route_members_route_channel_unique;
CREATE UNIQUE INDEX IF NOT EXISTS idx_route_members_route_channel_unique
  ON route_members(route_id, channel_id, group_name) WHERE mapping_json = '';

-- Keys pick one route group name; empty means "use every route's default".
ALTER TABLE downstream_keys ADD COLUMN route_group_name TEXT NOT NULL DEFAULT '';

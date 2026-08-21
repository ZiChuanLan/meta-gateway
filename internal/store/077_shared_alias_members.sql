-- Shared aliases need several members for the same (route_id, channel_id):
-- each real model renamed to the same alias name gets its own member row that
-- carries a mapping_json of {"real":"…"}, so the proxy rewrites per channel.
-- The original global UNIQUE(route_id, channel_id) index rejected the second
-- such member. Plain (mapping-less) members must stay unique per route+channel
-- to avoid duplicate bindings, so the constraint becomes a partial unique
-- index that only applies to members without an alias mapping.
DROP INDEX IF EXISTS idx_route_members_route_channel_unique;
CREATE UNIQUE INDEX IF NOT EXISTS idx_route_members_route_channel_unique
  ON route_members(route_id, channel_id) WHERE mapping_json = '';

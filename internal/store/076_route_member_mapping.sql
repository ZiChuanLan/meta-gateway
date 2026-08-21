-- Per-member model alias mapping. Historically the alias rewrite target lived
-- on the route (routes.mapping_json, single {"real":"…"}), which forced every
-- channel on a route to rewrite to the same upstream model. Moving it to the
-- member lets multiple channels expose different real models under one shared
-- alias name, and lets several models in one channel share an alias via
-- separate (route_id, channel_id) member rows.
ALTER TABLE route_members ADD COLUMN mapping_json TEXT NOT NULL DEFAULT '';
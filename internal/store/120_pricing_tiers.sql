-- Context-length price ladders and time-of-day schedules, per price layer.
--
-- Both live as JSON on the layer that already owns the prices rather than in a
-- table of their own: the billing path reads a layer's prices from one row on
-- every relay, and a side table would put a join on that path for a value most
-- gateways leave empty.
--
-- '' means "no ladder / no schedule", which is exactly the behaviour before
-- this migration: an unconfigured gateway bills identically.
ALTER TABLE model_metadata ADD COLUMN price_tiers TEXT NOT NULL DEFAULT '';
ALTER TABLE model_metadata ADD COLUMN price_schedule TEXT NOT NULL DEFAULT '';
ALTER TABLE route_members ADD COLUMN price_tiers TEXT NOT NULL DEFAULT '';
ALTER TABLE route_members ADD COLUMN price_schedule TEXT NOT NULL DEFAULT '';

-- Personal routing is arranged per MODEL: a plan holds one ordered upstream
-- list for each model its owner touched
-- ({"routes":{"<model>":[{id,weight,disabled}]}}) instead of a single flat
-- member slice.
--
-- The old slice cannot be mapped onto the new meaning. It said "these members
-- and nothing else, on every model", while the new form says "on this model,
-- in this order" and leaves every untouched model on the site's own order.
-- Reinterpreting it would silently narrow models the user never arranged, so
-- the rows are cleared and their keys unbound instead of being guessed at.
DELETE FROM team_route_plans;
UPDATE downstream_keys SET team_plan_id = 0 WHERE team_plan_id <> 0;

-- A user's arrangement without a goal name: keys that never picked a plan ride
-- it, so "my order" applies without binding every key by hand.
ALTER TABLE team_route_plans ADD COLUMN is_default INTEGER NOT NULL DEFAULT 0;
CREATE UNIQUE INDEX team_route_plans_default ON team_route_plans(user_id) WHERE is_default = 1;

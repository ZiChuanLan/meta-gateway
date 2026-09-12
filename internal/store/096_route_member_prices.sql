-- Per-member pricing: the most specific billing layer. Each channel serving a
-- model (a route member) can carry its own unit prices per 1k tokens — the
-- same model is often priced differently per upstream. 0 = fall through to
-- the model's metadata prices, then the downstream key's prices.
ALTER TABLE route_members ADD COLUMN price_prompt_per_1k REAL NOT NULL DEFAULT 0;
ALTER TABLE route_members ADD COLUMN price_completion_per_1k REAL NOT NULL DEFAULT 0;
ALTER TABLE route_members ADD COLUMN price_cache_per_1k REAL NOT NULL DEFAULT 0;

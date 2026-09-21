-- Per-key pool priority: which key of a site the relay should try first.
--
-- Higher wins, matching the priority convention used by channels and route
-- members. Keys are grouped into tiers and the pool exhausts the top tier
-- before falling through; inside one tier the relay rotates (round-robin), so
-- keys sharing a priority spread the traffic evenly instead of pinning it on
-- the first one. A tier is therefore "preferred / balanced / backup":
--
--    10 = preferred   try these first
--     0 = balanced    default; same-priority keys rotate
--   -10 = backup      only after every higher tier failed
--
-- Zero is deliberately the balanced default: credentials created by imports,
-- seeds and account sync never set the field, and a zero value must mean
-- "ordinary pool member" rather than "last resort".
ALTER TABLE credentials ADD COLUMN priority INTEGER NOT NULL DEFAULT 0;

-- Preserve the historical ordering for existing deployments. Before priority
-- existed the channel's bound credential was always tried before every other
-- key on the site, so mark those as preferred to keep the behaviour identical
-- until an operator changes it.
UPDATE credentials
   SET priority = 10
 WHERE id IN (
   SELECT credential_id FROM channels WHERE credential_id IS NOT NULL
 );

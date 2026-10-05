-- Automatic probe sources.
--
-- Most public-benefit sites are new-api or Sub2API deployments whose probe data
-- lives at a fixed, well-known path on the site root (/api/pricing, or
-- /.well-known/ai-transit.json on the public-transit fork). Typing that URL per
-- site is busywork the platform column can do away with: probe_auto = 1 means
-- "derive the source from the site's platform and base_url", with
-- probe_source_url still winning when an operator typed a custom one.
--
-- Default on: the collector only ever reads public pages, so the flag costs
-- nothing for sites it cannot resolve — the round just skips them.

ALTER TABLE sites ADD COLUMN probe_auto INTEGER NOT NULL DEFAULT 1;

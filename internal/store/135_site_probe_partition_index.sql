-- The console's site-observation panel resolves one round per (site, model,
-- group, monitor, source) partition, and the auto-apply pass resolves the same
-- shape. Without a covering index that read has to visit the wide sample row for
-- every candidate just to answer the partition lookup, which is what made the
-- panel cost seconds of CPU per page load on a real fleet: measured on a
-- production database of 214k samples, 9.9s of CPU for 1.4k result rows, with
-- the correlated partition probe showing up as a table search in EXPLAIN QUERY
-- PLAN. With this index the same read is index-only: 0.30s for a single site
-- (was 5.15s) and 2.0s for the whole fleet.
CREATE INDEX IF NOT EXISTS idx_site_probe_samples_partition ON site_probe_samples
	(site_id, raw_model, group_name, monitor_id, source_key, run_id DESC);

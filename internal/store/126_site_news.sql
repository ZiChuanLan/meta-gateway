-- Site news: what an upstream site publishes on its own notice board.
--
-- Read by the gateway, never written by the operator — the deployment's own
-- voice lives in 122_announcements.sql. The two are deliberately separate
-- tables: one is authored here and shown to members, the other is quoted from
-- elsewhere and shown to the operator.
--
-- Rows are keyed by the site's own announcement id, so a site that republishes
-- its whole board (New-API returns every announcement on every call) updates
-- rather than duplicates. first_seen_at is written once and never updated: it is
-- what makes "new since I last looked" answerable without a second table.
CREATE TABLE IF NOT EXISTS site_announcements (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  site_id INTEGER NOT NULL,
  upstream_id TEXT NOT NULL,
  content TEXT NOT NULL,
  extra TEXT NOT NULL DEFAULT '',
  kind TEXT NOT NULL DEFAULT '',
  published_at TEXT NOT NULL,
  first_seen_at TEXT NOT NULL,
  fetched_at TEXT NOT NULL,
  UNIQUE(site_id, upstream_id)
);

-- The feed is read newest-first across every site.
CREATE INDEX IF NOT EXISTS idx_site_announcements_order
  ON site_announcements(published_at DESC, id DESC);

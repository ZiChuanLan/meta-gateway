-- Upstream notice-board cadence.
--
-- The reader ships at five minutes (the SITE_NEWS_INTERVAL_SECONDS bootstrap in
-- internal/config): a board changes when a site has something to say, and the
-- dashboard's refresh button covers "now".
--
-- Unlike the site-probe columns in 117 — where 0 is rejected because an unset
-- row must not read as "stop probing" — this column keeps 0 as a real value:
-- the console renders it as "off", and only NULL means "not overridden". A
-- notice board is background reading, so turning the scheduled round off is a
-- choice an operator may legitimately make.

ALTER TABLE runtime_settings ADD COLUMN site_news_interval_seconds INTEGER;

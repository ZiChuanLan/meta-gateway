-- One request = one visible log row.
--
-- Every upstream attempt still gets its own proxy_logs row: that is the audit,
-- health and billing record, and it is what survives a crash mid-request. What
-- changes is which row the console lists. When an attempt is written, every
-- earlier row of the same request_id is marked as superseded: merged_into
-- points at the surviving row, and list queries return only rows with
-- merged_into IS NULL.
--
-- The failover chain itself is derived at read time from those superseded rows
-- (attempt order, channel, status, error text). Storing a second copy as JSON
-- would duplicate up to 600 bytes of upstream error per hop and grow without
-- bound — the failure mode octopus hit when one request accumulated millions of
-- attempts in a single row. Here the chain is bounded by the retry settings and
-- the read path caps how many hops it returns.
ALTER TABLE proxy_logs ADD COLUMN merged_into INTEGER;

-- Partial index: the list query is "newest open rows", so this is the index
-- whose scan order is already right (id DESC) and which skips merged attempts.
CREATE INDEX IF NOT EXISTS idx_proxy_logs_open ON proxy_logs(id) WHERE merged_into IS NULL;

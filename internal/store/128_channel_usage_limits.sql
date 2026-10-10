-- Per-channel usage budget: stop paying for an account once it has cost (or
-- consumed) more than the operator said it may.
--
-- The two limits are independent (either one trips) and 0 means "no limit",
-- matching the key / group / team quota columns. usage_used_* are counters
-- written in the same transaction as the usage row, so the relay never has to
-- SUM the whole usage table on the request path to answer "have we spent it
-- yet"; they are re-derived from usage_records whenever the operator saves a
-- limit, which keeps them from drifting away from the ledger.
--
-- usage_limit_hit records WHICH limit came due ("cost", "tokens", "cost,tokens")
-- and usage_limit_hit_at when — the console phrases it in the reader's language,
-- and hit_at non-empty is also the marker for "this channel is parked because of
-- its budget" (as opposed to a failed probe or a manual disable), which is what
-- lets saving a higher limit release it again.
ALTER TABLE channels ADD COLUMN usage_limit_cost REAL NOT NULL DEFAULT 0;
ALTER TABLE channels ADD COLUMN usage_limit_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE channels ADD COLUMN usage_used_cost REAL NOT NULL DEFAULT 0;
ALTER TABLE channels ADD COLUMN usage_used_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE channels ADD COLUMN usage_limit_hit TEXT NOT NULL DEFAULT '';
ALTER TABLE channels ADD COLUMN usage_limit_hit_at TEXT NOT NULL DEFAULT '';

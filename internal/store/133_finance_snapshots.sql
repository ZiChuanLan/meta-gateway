-- Account finance observations per channel: balance (site credit) plus the
-- normalized model quotes. The payload is the allowlisted FinanceItem JSON
-- only — no credentials, cookies, account input, or raw upstream bodies ever
-- reach this table. Rows are append-only so a later failed refresh cannot
-- erase the last good reading.
CREATE TABLE finance_snapshots (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 channel_id INTEGER NOT NULL,
 observed_at TEXT NOT NULL,
 payload TEXT NOT NULL
);
CREATE INDEX finance_snapshots_channel ON finance_snapshots(channel_id, id DESC);

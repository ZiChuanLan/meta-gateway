-- Quotes are observations. Billing rules are explicit local policy. Existing
-- numeric columns and historical ledger amounts stay unchanged (legacy rules).
CREATE TABLE price_quotes (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 scope_key TEXT NOT NULL,
 source TEXT NOT NULL,
 purpose TEXT NOT NULL,
 model TEXT NOT NULL,
 site_id INTEGER NOT NULL DEFAULT 0,
 account_id INTEGER NOT NULL DEFAULT 0,
 upstream_group TEXT NOT NULL DEFAULT '',
 observed_at TEXT NOT NULL,
 expires_at TEXT NOT NULL DEFAULT '',
 payload TEXT NOT NULL
);
CREATE INDEX price_quotes_lookup ON price_quotes(scope_key, id DESC);
CREATE INDEX price_quotes_model ON price_quotes(model, purpose, id DESC);
CREATE TABLE billing_rules (
 scope TEXT NOT NULL CHECK(scope IN ('model','member')),
 scope_key TEXT NOT NULL,
 revision INTEGER NOT NULL,
 payload TEXT NOT NULL,
 updated_at TEXT NOT NULL,
 PRIMARY KEY(scope, scope_key)
);
ALTER TABLE usage_records ADD COLUMN billing_status TEXT NOT NULL DEFAULT 'legacy';
ALTER TABLE usage_records ADD COLUMN billing_evidence TEXT NOT NULL DEFAULT '';

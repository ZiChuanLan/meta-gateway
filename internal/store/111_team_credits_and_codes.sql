-- Team accounts get a credit pool of their own: tokens granted by the owner
-- (or bought with a credit code) and charged by the relay alongside the
-- per-key quota. 0 means unlimited, exactly like downstream_keys.
-- The two limits are independent and both enforced: a key quota says "this
-- credential may spend X", the account pool says "this person may spend Y".
ALTER TABLE team_users ADD COLUMN quota_total_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE team_users ADD COLUMN quota_used_tokens INTEGER NOT NULL DEFAULT 0;

-- team_invites becomes the team's code table: an invitation that may be used
-- more than once, a credit voucher, or a recovery link. The kind CHECK has to
-- grow a third value, and SQLite cannot alter a CHECK — so the table is rebuilt
-- (same shape of migration as 009_site_channel_cascade.sql).
CREATE TABLE team_invites_new (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  token_hash TEXT NOT NULL UNIQUE,
  label TEXT NOT NULL DEFAULT '',
  policy_id INTEGER NOT NULL REFERENCES team_policies(id),
  role TEXT NOT NULL DEFAULT 'member' CHECK(role IN ('admin','member')),
  kind TEXT NOT NULL DEFAULT 'invite' CHECK(kind IN ('invite','recovery','credit')),
  user_id INTEGER REFERENCES team_users(id),
  expires_at INTEGER NOT NULL,
  consumed_at INTEGER,
  revoked INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  -- max_uses covers batch invites (one code, N registrations) and shared
  -- vouchers. used_count is the count actually consumed; consumed_at stays the
  -- FIRST use, because that is what the existing list view shows.
  max_uses INTEGER NOT NULL DEFAULT 1 CHECK(max_uses BETWEEN 1 AND 100000),
  used_count INTEGER NOT NULL DEFAULT 0,
  -- Credit face value in tokens. For kind='invite' it is granted at signup, for
  -- kind='credit' it is added to the redeeming account.
  quota_tokens INTEGER NOT NULL DEFAULT 0,
  note TEXT NOT NULL DEFAULT ''
);
INSERT INTO team_invites_new
  (id, token_hash, label, policy_id, role, kind, user_id, expires_at, consumed_at, revoked, created_at, max_uses, used_count, quota_tokens, note)
  SELECT id, token_hash, label, policy_id, role, kind, user_id, expires_at, consumed_at, revoked, created_at,
         1, CASE WHEN consumed_at IS NULL THEN 0 ELSE 1 END, 0, ''
    FROM team_invites;
DROP TABLE team_invites;
ALTER TABLE team_invites_new RENAME TO team_invites;
CREATE INDEX team_invites_kind_idx ON team_invites(kind, revoked);

-- One redemption per (code, account). A shared voucher with max_uses > 1 must
-- not let the same person redeem it twice; the total is capped by used_count.
CREATE TABLE team_code_redemptions (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  code_id INTEGER NOT NULL REFERENCES team_invites(id),
  user_id INTEGER NOT NULL REFERENCES team_users(id),
  quota_tokens INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE UNIQUE INDEX team_code_redemptions_once ON team_code_redemptions(code_id, user_id);

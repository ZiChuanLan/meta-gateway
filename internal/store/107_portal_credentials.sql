-- Self-serve portal credentials: how a key holder proves they own a key
-- without transcribing a 76-character secret.
--
-- The credential is bound to the KEY, not to a new "user" row: the key stays
-- the single unit of identity, policy and revocation, so deleting or disabling
-- it takes every credential with it (foreign_keys is ON, see store.go) and no
-- orphaned account can ever exist. That is what keeps this out of L2.
--
-- kind:
--   password  — subject is '' and secret_hash holds a bcrypt hash of
--               base64(sha256(password)); see HashPortalPassword for why the
--               digest is pre-hashed. The login name is the key's own name; the
--               caller refuses a name that another password-bearing key already
--               uses, and login fails closed if one slips through.
--   github    — subject is the provider's numeric user id, secret_hash empty.
--   linuxdo   — same as github; the community's trust_level is checked against
--               a deployment-wide minimum at bind time and at login.
--
-- The LINUX DO trust minimum is NOT a column here on purpose. It is a property
-- of the deployment ("this gateway is for regulars"), not of one person's
-- binding, so it lives in config — one source of truth, and raising it applies
-- to everyone rather than only to bindings made afterwards. Recording it per
-- row would mean a stale bar silently outliving the operator's decision, which
-- is exactly the class of setting this project refuses to ship.
CREATE TABLE IF NOT EXISTS portal_credentials (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  key_id INTEGER NOT NULL REFERENCES downstream_keys(id) ON DELETE CASCADE,
  kind TEXT NOT NULL,
  subject TEXT NOT NULL DEFAULT '',
  secret_hash TEXT NOT NULL DEFAULT '',
  label TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  last_used_at TEXT
);

-- At most one credential of each kind per key: one password, one GitHub, one
-- LINUX DO. Adding a second binding of the same kind replaces the first, which
-- is why this is unique rather than a plain index.
CREATE UNIQUE INDEX IF NOT EXISTS idx_portal_credentials_key_kind
  ON portal_credentials(key_id, kind);

-- One external identity maps to exactly one key.
CREATE UNIQUE INDEX IF NOT EXISTS idx_portal_credentials_subject
  ON portal_credentials(kind, subject) WHERE subject <> '';

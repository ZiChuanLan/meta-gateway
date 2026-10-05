-- Third-party sign-in for team accounts.
--
-- One row per external identity, keyed by the provider's own stable subject id
-- (never the email: people change addresses, providers reuse them). A member
-- may hold several identities; a given identity belongs to exactly one member,
-- which the unique index enforces so a race between two logins cannot create
-- two accounts for one person.
CREATE TABLE team_identities (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id INTEGER NOT NULL REFERENCES team_users(id),
  provider TEXT NOT NULL,
  subject TEXT NOT NULL,
  email TEXT NOT NULL DEFAULT '',
  name TEXT NOT NULL DEFAULT '',
  avatar TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  last_login_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE UNIQUE INDEX team_identities_subject ON team_identities(provider, subject);
CREATE INDEX team_identities_user ON team_identities(user_id);

-- Provider credentials live beside the rest of the team settings. The secret is
-- stored as an encrypted envelope (crypto.Encrypter), never in clear: the
-- console only ever reports whether a secret is set.
ALTER TABLE team_settings ADD COLUMN oauth_json TEXT NOT NULL DEFAULT '';

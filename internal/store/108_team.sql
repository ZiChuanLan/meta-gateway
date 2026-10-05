-- Team mode is opt-in. Legacy keys and the existing admin credential remain valid.
CREATE TABLE team_settings (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  mode TEXT NOT NULL DEFAULT 'personal' CHECK (mode IN ('personal','team')),
  branding_json TEXT NOT NULL DEFAULT '{}'
);
INSERT INTO team_settings(id) VALUES(1);
CREATE TABLE team_policies (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  models_json TEXT NOT NULL DEFAULT '[]',
  members_json TEXT NOT NULL DEFAULT '[]',
  all_models INTEGER NOT NULL DEFAULT 0,
  max_keys INTEGER NOT NULL DEFAULT 5 CHECK(max_keys BETWEEN 1 AND 100),
  rpm INTEGER NOT NULL DEFAULT 60 CHECK(rpm BETWEEN 1 AND 100000),
  allow_routing INTEGER NOT NULL DEFAULT 0
);
INSERT INTO team_policies(name) VALUES('Default');
CREATE TABLE team_users (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  username TEXT NOT NULL COLLATE NOCASE UNIQUE,
  name TEXT NOT NULL,
  password_hash TEXT NOT NULL,
  role TEXT NOT NULL DEFAULT 'member' CHECK(role IN ('owner','admin','member')),
  status TEXT NOT NULL DEFAULT 'active' CHECK(status IN ('active','paused')),
  policy_id INTEGER NOT NULL REFERENCES team_policies(id),
  session_version INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE team_sessions (
  token_hash TEXT PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES team_users(id) ON DELETE CASCADE,
  version INTEGER NOT NULL,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL
);
CREATE INDEX team_sessions_user ON team_sessions(user_id);
CREATE TABLE team_invites (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  token_hash TEXT NOT NULL UNIQUE,
  label TEXT NOT NULL DEFAULT '',
  policy_id INTEGER NOT NULL REFERENCES team_policies(id),
  role TEXT NOT NULL DEFAULT 'member' CHECK(role IN ('admin','member')),
  kind TEXT NOT NULL DEFAULT 'invite' CHECK(kind IN ('invite','recovery')),
  user_id INTEGER REFERENCES team_users(id),
  expires_at INTEGER NOT NULL,
  consumed_at INTEGER,
  revoked INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE team_route_plans (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id INTEGER NOT NULL REFERENCES team_users(id),
  name TEXT NOT NULL,
  members_json TEXT NOT NULL DEFAULT '[]'
);
CREATE INDEX team_route_plans_user ON team_route_plans(user_id);
ALTER TABLE downstream_keys ADD COLUMN user_id INTEGER REFERENCES team_users(id);
ALTER TABLE downstream_keys ADD COLUMN team_plan_id INTEGER NOT NULL DEFAULT 0;
ALTER TABLE downstream_keys ADD COLUMN team_hint TEXT NOT NULL DEFAULT '';
ALTER TABLE downstream_keys ADD COLUMN team_deleted_at TEXT NOT NULL DEFAULT '';
CREATE INDEX downstream_keys_user ON downstream_keys(user_id);
ALTER TABLE usage_records ADD COLUMN user_id INTEGER NOT NULL DEFAULT 0;
CREATE INDEX usage_records_user_time ON usage_records(user_id, created_at);
CREATE TABLE team_requests (
  request_id TEXT PRIMARY KEY,
  user_id INTEGER NOT NULL,
  key_id INTEGER NOT NULL,
  path TEXT NOT NULL,
  status INTEGER NOT NULL,
  latency_ms INTEGER NOT NULL,
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX team_requests_user_time ON team_requests(user_id, created_at);

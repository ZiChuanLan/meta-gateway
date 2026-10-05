-- Optional user-owned request preferences; never change the site's policy.
ALTER TABLE team_policies ADD COLUMN allow_request_preferences INTEGER NOT NULL DEFAULT 0;
ALTER TABLE team_users ADD COLUMN preferences_json TEXT NOT NULL DEFAULT '{}';

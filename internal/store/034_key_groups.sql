-- Multi-tenant token groups: downstream_keys belong to a group; groups carry
-- their own quota (0 = unlimited) and optional rate limits. Relay quota checks
-- enforce the key quota and the group quota; usage accrues to both.
ALTER TABLE downstream_keys ADD COLUMN group_name TEXT NOT NULL DEFAULT 'default';

CREATE TABLE IF NOT EXISTS key_groups (
    name TEXT PRIMARY KEY,
    quota_total_tokens INTEGER NOT NULL DEFAULT 0,
    quota_used_tokens INTEGER NOT NULL DEFAULT 0,
    rate_per_minute INTEGER NOT NULL DEFAULT 0,
    rate_burst INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

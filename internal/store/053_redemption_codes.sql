-- One-time redemption codes: minted by the admin, redeemed by a downstream
-- key holder to top up quota_total_tokens. A code is single-use.
CREATE TABLE IF NOT EXISTS redemption_codes (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  code TEXT NOT NULL UNIQUE,
  quota_tokens INTEGER NOT NULL,
  created_by INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  expires_at TEXT,
  redeemed_by_key_id INTEGER NOT NULL DEFAULT 0,
  redeemed_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_redemption_codes_redeemed ON redemption_codes (redeemed_by_key_id);

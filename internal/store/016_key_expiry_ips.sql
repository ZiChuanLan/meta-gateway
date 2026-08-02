-- Per-key expiry and source-IP allowlist.
-- expires_at: RFC3339 timestamp; empty means never expires.
-- allowed_ips: newline-separated IPs or CIDRs; empty means any source.
ALTER TABLE downstream_keys ADD COLUMN expires_at TEXT NOT NULL DEFAULT '';
ALTER TABLE downstream_keys ADD COLUMN allowed_ips TEXT NOT NULL DEFAULT '';

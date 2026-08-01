-- Per-key model allowlist / denylist. Comma-separated model names; empty means "no restriction".
-- Allowlist: when non-empty, only these models may be relayed.
-- Denylist: when non-empty, these models are blocked even if they match the allowlist.
ALTER TABLE downstream_keys ADD COLUMN model_allowlist TEXT NOT NULL DEFAULT '';
ALTER TABLE downstream_keys ADD COLUMN model_denylist TEXT NOT NULL DEFAULT '';

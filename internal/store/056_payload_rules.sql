-- Channel-level payload rewrite rules: a JSON array of {match, actions}
-- chains (see internal/proxy/payload_rules.go for the shape). Empty string
-- (default) = no rewriting; the request body passes through untouched.
ALTER TABLE channels ADD COLUMN payload_rules TEXT NOT NULL DEFAULT '';

-- Per-channel outbound HTTP(S) proxy. Empty (default) = inherit the global
-- proxy (runtime setting proxy_url); a value overrides it for this channel's
-- upstream requests. The URL is policy-validated (http/https, allowed
-- network) and the proxy host is dialed through the SSRF policy.
ALTER TABLE channels ADD COLUMN proxy_url TEXT NOT NULL DEFAULT '';

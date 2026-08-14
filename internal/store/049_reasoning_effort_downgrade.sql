-- Channel capability: highest reasoning_effort this channel's upstream accepts.
-- Empty = passthrough (unknown / unrestricted). Values: none, minimal, low,
-- medium, high, xhigh, max. Requests asking for more are downgraded at
-- forward time instead of failing over (e.g. gateways that reject max).
ALTER TABLE channels ADD COLUMN max_reasoning_effort TEXT NOT NULL DEFAULT '';

-- Proxy log: records a capability downgrade like "max→high" when the request's
-- reasoning_effort was rewritten for the serving channel. Empty = unchanged.
ALTER TABLE proxy_logs ADD COLUMN mapped_reasoning_effort TEXT NOT NULL DEFAULT '';

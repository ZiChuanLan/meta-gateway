-- Money accounting alongside the token quotas.
--
-- Every quota in the product used to count tokens, which is the wrong unit for
-- a customer who buys "100 dollars of usage": the price per token differs by
-- model and by channel, so a token budget cannot express a spend budget. These
-- columns add the second unit. They are enforced TOGETHER with the token
-- quotas, never instead of them: whichever runs out first refuses the request.
--
-- Costs are stored in the pricing unit the ledger already uses
-- (usage_records.cost, i.e. USD as published by the model catalogue). The
-- display currency is a presentation concern handled by currency_symbol /
-- currency_rate below, so changing the display never rewrites history.
ALTER TABLE downstream_keys ADD COLUMN quota_total_cost REAL NOT NULL DEFAULT 0;
ALTER TABLE downstream_keys ADD COLUMN quota_used_cost REAL NOT NULL DEFAULT 0;
ALTER TABLE key_groups ADD COLUMN quota_total_cost REAL NOT NULL DEFAULT 0;
ALTER TABLE key_groups ADD COLUMN quota_used_cost REAL NOT NULL DEFAULT 0;
ALTER TABLE team_users ADD COLUMN quota_total_cost REAL NOT NULL DEFAULT 0;
ALTER TABLE team_users ADD COLUMN quota_used_cost REAL NOT NULL DEFAULT 0;

-- Per-request pricing: some upstreams sell calls, not tokens. It is a THIRD
-- price layer beside prompt/completion/cache, resolved by the same two-step
-- fallback (route member first, then model metadata) and multiplied by the
-- same billing ratio, so a call priced per request needs no separate rule.
ALTER TABLE route_members ADD COLUMN price_per_request REAL NOT NULL DEFAULT 0;
ALTER TABLE model_metadata ADD COLUMN price_per_request REAL NOT NULL DEFAULT 0;

-- Credit vouchers may carry money as well as tokens.
ALTER TABLE team_invites ADD COLUMN quota_cost REAL NOT NULL DEFAULT 0;

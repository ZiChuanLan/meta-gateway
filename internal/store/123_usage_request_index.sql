-- Point lookup for "which model was this request", the join both the member log
-- page and the per-model usage stats need.
--
-- usage_records is keyed for reads by (downstream_key_id, created_at) and
-- (user_id, created_at), so any question about ONE request had to scan the whole
-- table: the member log page resolves a model per row with a correlated
-- subquery, which is a full scan per returned row. The pair is what makes the
-- lookup selective — request_id alone would return every attempt's row.
CREATE INDEX IF NOT EXISTS idx_usage_records_request ON usage_records(request_id, user_id);

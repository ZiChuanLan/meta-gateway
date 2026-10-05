-- Record the money a credit voucher granted, beside the token count it already
-- stores. The redemption log is what answers "who topped this account up, and
-- with how much" without re-deriving it from the code (which may since have
-- been revoked).
ALTER TABLE team_code_redemptions ADD COLUMN quota_cost REAL NOT NULL DEFAULT 0;

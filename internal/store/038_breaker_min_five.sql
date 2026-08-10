-- Progressive cooldown + breaker threshold consistency: with tiered cooldown
-- enabled (fail 2/3/4 → level 2/3/4), a breaker_fail_count below 5 disables
-- the member before the 1h/24h tiers ever apply. Normalize existing overrides
-- to the new floor (the env default also moved from 3 to 5).
UPDATE runtime_settings
SET breaker_fail_count = 5
WHERE breaker_fail_count IS NOT NULL
  AND breaker_fail_count > 0
  AND breaker_fail_count < 5;

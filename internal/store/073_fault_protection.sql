-- 073: master switch for retry failure protection. NULL follows the
-- environment/default value; 0 disables cooldown/auto-disable enforcement.
ALTER TABLE runtime_settings ADD COLUMN fault_protection_enabled INTEGER;

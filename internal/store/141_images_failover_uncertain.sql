-- Third, blunt tier of the image-failover opt-in: replay 504, transport
-- timeouts, streams that died mid-flight and empty 2xx — every failure where
-- the upstream may already hold a finished picture and a charge. The operator
-- trades a possible duplicate bill for a second attempt.
-- NULL preserves the env bootstrap (IMAGES_FAILOVER_INCLUDE_UNCERTAIN, default
-- off) for rows created before this setting existed.
ALTER TABLE runtime_settings ADD COLUMN images_failover_include_uncertain INTEGER;

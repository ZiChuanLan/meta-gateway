-- Second, warned tier of the image-failover opt-in: also replay 5xx answers
-- other than 504, which may arrive after the picture was generated and billed.
-- NULL preserves the env bootstrap (IMAGES_FAILOVER_INCLUDE_AMBIGUOUS, default
-- off) for rows created before this setting existed.
ALTER TABLE runtime_settings ADD COLUMN images_failover_include_ambiguous INTEGER;

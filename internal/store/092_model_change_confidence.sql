-- Model-change confidence tracking:
--   miss_count    how many consecutive successful syncs have lacked the model
--                 since the removal was recorded (0 = detection round); a row
--                 reaching the confirmation threshold stops being "suspected".
--   flap_count    how many times this channel×model has re-disappeared within
--                 the flap window; repeated churn collapses into one counter
--                 instead of stacking history rows.
--   partial_keys  the snapshot was taken while only some of the channel's API
--                 keys returned a model list, so the removal may be a false
--                 positive (a silent key hides its models from the merge).
ALTER TABLE model_changes ADD COLUMN miss_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE model_changes ADD COLUMN flap_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE model_changes ADD COLUMN partial_keys INTEGER NOT NULL DEFAULT 0;

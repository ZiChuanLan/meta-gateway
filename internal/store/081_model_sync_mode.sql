-- Per-channel model sync mode: 'auto' keeps the legacy behaviour (discovery
-- auto-adopts every probed model as route+member), 'manual' only refreshes the
-- discovery snapshot — adoption happens per model from the channel models
-- panel. Column default is manual so newly created channels opt in; existing
-- rows are pinned back to auto so the upgrade is behaviour-neutral.
ALTER TABLE channels ADD COLUMN model_sync_mode TEXT NOT NULL DEFAULT 'manual';
UPDATE channels SET model_sync_mode = 'auto';

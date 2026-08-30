-- Name unification records every mutation it makes, grouped into a batch, so a
-- group can be reverted precisely later: routes and members the batch created
-- are deleted again, and routes it archived are restored to their previous
-- enabled state.
--
-- Before this log existed, the only way to recognise a unify-created member was
-- the implicit (auto=1 AND manual_override=1) fingerprint, which no other code
-- documents or enforces and which silently collides with manually pinned
-- members. Undo was therefore guesswork, and archived routes could not be told
-- apart from ones an operator disabled on purpose.

CREATE TABLE IF NOT EXISTS model_unify_batches (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    canonical TEXT NOT NULL,
    route_id INTEGER NOT NULL,
    routes_created INTEGER NOT NULL DEFAULT 0,
    members_created INTEGER NOT NULL DEFAULT 0,
    routes_archived INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    undone_at TEXT
);

CREATE INDEX IF NOT EXISTS idx_model_unify_batches_open
    ON model_unify_batches(undone_at, id DESC);

-- One row per mutation, replayed in reverse seq order on undo.
--   route_created   -> the alias route did not exist and was created
--   member_created  -> a channel binding was added to the alias route
--   route_archived  -> an original route was hidden (prev_enabled keeps state)
CREATE TABLE IF NOT EXISTS model_unify_ops (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    batch_id INTEGER NOT NULL,
    seq INTEGER NOT NULL,
    op TEXT NOT NULL,
    route_id INTEGER NOT NULL,
    member_id INTEGER,
    prev_enabled INTEGER,
    undone INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_model_unify_ops_batch
    ON model_unify_ops(batch_id, seq);

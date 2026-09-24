-- Unify removes the original routes a group supersedes instead of parking them
-- disabled. A deleted row cannot be listed, restored or explained later, so the
-- op that removed it now carries the model name and a verbatim snapshot of the
-- rows that went away (route + its members). The snapshot is what makes
-- "rebuild this original" possible without a hand-maintained column list here
-- going stale on every future ALTER TABLE.
ALTER TABLE model_unify_ops ADD COLUMN model_name TEXT NOT NULL DEFAULT '';

ALTER TABLE model_unify_ops ADD COLUMN snapshot TEXT NOT NULL DEFAULT '';

-- Same counter, honest name: the batch's route operations are deletions now.
ALTER TABLE model_unify_batches RENAME COLUMN routes_archived TO routes_deleted;

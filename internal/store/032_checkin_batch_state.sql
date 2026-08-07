-- Durable "scheduled batch completed" marker for the check-in scheduler.
-- A batch that was interrupted (restart/stop) never records today here, so the
-- next start can catch up; only fully completed batches suppress a re-run.
CREATE TABLE IF NOT EXISTS checkin_batch_state (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    completed_at TEXT NOT NULL
);

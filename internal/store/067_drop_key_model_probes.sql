-- Key × model probes were removed (consumed upstream quota for little value).
-- Drops the table for databases that applied 060; no-op on fresh installs.
DROP TABLE IF EXISTS key_model_probes;

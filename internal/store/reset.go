package store

import (
	"fmt"
)

// businessTables are wiped by FactoryReset. Configuration (sites, runtime
// settings, TOTP state, webdav/plugin state) and backup history survive; the
// reset returns the gateway to a clean slate: no channels, keys, routes,
// models, logs, quotas, histories or rules.
var businessTables = []string{
	"credentials",
	"channels",
	"routes",
	"route_members",
	"downstream_keys",
	"proxy_logs",
	"discovered_models",
	"checkin_logs",
	"checkin_batch_state",
	"usage_records",
	"model_ratios",
	"key_groups",
	"balance_history",
	"decision_snapshots",
	"channel_model_blocks",
	"redemption_codes",
	"model_metadata",
	"error_passthrough_rules",
	"channel_health_history",
	"alert_rules",
	"prompt_guard_rules",
	"audit_events",
}

// FactoryReset wipes every business table in one transaction (configuration
// tables are intentionally left untouched). It returns the per-table deleted
// counts for confirmation UI.
func (db *DB) FactoryReset() (map[string]int64, error) {
	tx, err := db.Begin()
	if err != nil {
		return nil, fmt.Errorf("factory reset begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	deleted := make(map[string]int64, len(businessTables))
	for _, table := range businessTables {
		res, err := tx.Exec(`DELETE FROM ` + table)
		if err != nil {
			return nil, fmt.Errorf("factory reset %s: %w", table, err)
		}
		n, _ := res.RowsAffected()
		deleted[table] = n
	}
	// Reset AUTOINCREMENT counters so fresh ids start at 1.
	if _, err := tx.Exec(`DELETE FROM sqlite_sequence WHERE name IN (` + sequenceList() + `)`); err != nil {
		return nil, fmt.Errorf("factory reset sequence: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("factory reset commit: %w", err)
	}
	return deleted, nil
}

func sequenceList() string {
	out := ""
	for i, table := range businessTables {
		if i > 0 {
			out += ","
		}
		out += "'" + table + "'"
	}
	return out
}

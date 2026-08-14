package store

import (
	"fmt"
)

// GCResult reports what a maintenance pass deleted and whether VACUUM ran.
type GCResult struct {
	RouteMembers    int64 `json:"route_members"`
	ProxyLogs       int64 `json:"proxy_logs"`
	Discovered      int64 `json:"discovered_models"`
	CheckinLogs     int64 `json:"checkin_logs"`
	UsageRecords    int64 `json:"usage_records"`
	BalanceHistory  int64 `json:"balance_history"`
	DecisionSnaps   int64 `json:"decision_snapshots"`
	HealthHistory  int64 `json:"channel_health_history"`
	ModelBlocks    int64 `json:"channel_model_blocks"`
	Redemptions     int64 `json:"redemption_codes"`
	ErrorRules      int64 `json:"error_passthrough_rules"`
	FreelistPages   int64 `json:"freelist_pages"`
	PageSize        int64 `json:"page_size"`
	VacuumFreedBytes int64 `json:"vacuum_freed_bytes"`
	Vacuumed        bool   `json:"vacuumed"`
}

// vacuumFreeListPages is the freelist threshold (in pages) above which a
// maintenance pass runs VACUUM. Smaller freelists are left alone — rebuilding
// the file for a few wasted pages costs more than it saves.
const vacuumFreeListPages = 256

// GC deletes orphaned rows (children whose parent channel/route/credential/
// key was deleted) and, when the database has accumulated enough free pages,
// runs VACUUM to reclaim the space. It returns per-table deletion counts.
func (db *DB) GC() (*GCResult, error) {
	res := &GCResult{}
	tx, err := db.Begin()
	if err != nil {
		return nil, fmt.Errorf("gc begin: %w", err)
	}
	//nolint:errcheck
	defer tx.Rollback()

	type job struct {
		name  string
		sql   string
		field *int64
	}
	jobs := []job{
		{"route_members", `DELETE FROM route_members WHERE route_id NOT IN (SELECT id FROM routes) OR channel_id NOT IN (SELECT id FROM channels)`, &res.RouteMembers},
		{"proxy_logs", `DELETE FROM proxy_logs WHERE channel_id NOT IN (SELECT id FROM channels)`, &res.ProxyLogs},
		{"discovered_models", `DELETE FROM discovered_models WHERE channel_id NOT IN (SELECT id FROM channels)`, &res.Discovered},
		{"checkin_logs", `DELETE FROM checkin_logs WHERE credential_id NOT IN (SELECT id FROM credentials)`, &res.CheckinLogs},
		{"usage_records", `DELETE FROM usage_records WHERE channel_id NOT IN (SELECT id FROM channels) OR downstream_key_id NOT IN (SELECT id FROM downstream_keys)`, &res.UsageRecords},
		{"balance_history", `DELETE FROM balance_history WHERE channel_id NOT IN (SELECT id FROM channels)`, &res.BalanceHistory},
		{"decision_snapshots", `DELETE FROM decision_snapshots WHERE route_id NOT IN (SELECT id FROM routes)`, &res.DecisionSnaps},
		{"channel_health_history", `DELETE FROM channel_health_history WHERE channel_id NOT IN (SELECT id FROM channels)`, &res.HealthHistory},
		{"channel_model_blocks", `DELETE FROM channel_model_blocks WHERE channel_id NOT IN (SELECT id FROM channels)`, &res.ModelBlocks},
		{"redemption_codes", `DELETE FROM redemption_codes WHERE redeemed_by_key_id != 0 AND redeemed_by_key_id NOT IN (SELECT id FROM downstream_keys)`, &res.Redemptions},
		{"error_passthrough_rules", `DELETE FROM error_passthrough_rules WHERE channel_id != 0 AND channel_id NOT IN (SELECT id FROM channels)`, &res.ErrorRules},
	}
	for _, j := range jobs {
		result, err := tx.Exec(j.sql)
		if err != nil {
			return nil, fmt.Errorf("gc %s: %w", j.name, err)
		}
		n, err := result.RowsAffected()
		if err != nil {
			return nil, fmt.Errorf("gc %s rows: %w", j.name, err)
		}
		*j.field = n
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("gc commit: %w", err)
	}

	// VACUUM cannot run inside a transaction; the cleanup above is already
	// committed, so check the freelist and compact when worthwhile.
	res.PageSize = db.pageSize()
	res.FreelistPages = db.freelistPages()
	if res.FreelistPages >= vacuumFreeListPages {
		before := res.FreelistPages * res.PageSize
		if _, err := db.Exec(`VACUUM`); err != nil {
			return nil, fmt.Errorf("gc vacuum: %w", err)
		}
		after := db.freelistPages()
		res.Vacuumed = true
		res.VacuumFreedBytes = (res.FreelistPages - after) * res.PageSize
		if res.VacuumFreedBytes < 0 {
			res.VacuumFreedBytes = 0
		}
		res.FreelistPages = after
		_ = before
	}
	return res, nil
}

func (db *DB) pageSize() int64 {
	var size int64
	_ = db.QueryRow(`PRAGMA page_size`).Scan(&size)
	if size <= 0 {
		size = 4096
	}
	return size
}

func (db *DB) freelistPages() int64 {
	var pages int64
	_ = db.QueryRow(`PRAGMA freelist_count`).Scan(&pages)
	return pages
}

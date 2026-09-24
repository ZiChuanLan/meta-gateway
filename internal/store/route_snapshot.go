package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// Verbatim row snapshots for route removal.
//
// Unified originals are deleted outright, which means nothing in the database
// remembers what they were. The columns are captured generically — read from
// PRAGMA table_info at snapshot time, written back from the same list — so a
// future migration that adds a column to routes/route_members needs no change
// here, and a rebuild cannot silently drop whatever the operator had set
// (member prices, group, priority, weights, route-level overrides).
//
// Values are stored as text with NULL preserved: SQLite applies the column's
// declared affinity on insert, so an INTEGER/REAL column reads its number back
// out of the stored text.

type tableSnapshot struct {
	Columns []string `json:"columns"`
	Values  []any    `json:"values"`
}

type routeSnapshot struct {
	Route   tableSnapshot   `json:"route"`
	Members []tableSnapshot `json:"members"`
}

// snapshotRouteRows captures one route plus every member it carries.
func snapshotRouteRows(tx *sql.Tx, routeID int64) (routeSnapshot, error) {
	var snap routeSnapshot
	rows, err := snapshotRows(tx, "routes", "id = ?", routeID)
	if err != nil {
		return snap, err
	}
	if len(rows) == 0 {
		return snap, &UnifyValidationError{Message: "unify: route not found while snapshotting it"}
	}
	snap.Route = rows[0]
	members, err := snapshotRows(tx, "route_members", "route_id = ? ORDER BY id", routeID)
	if err != nil {
		return snap, err
	}
	snap.Members = members
	return snap, nil
}

// snapshotRows reads every column of the rows matching where (which may carry
// its own trailing ORDER BY) into column/value pairs.
func snapshotRows(ex sqlExecutor, table, where string, args ...any) ([]tableSnapshot, error) {
	columns, err := tableColumns(ex, table)
	if err != nil {
		return nil, err
	}
	quoted := make([]string, len(columns))
	for i, column := range columns {
		quoted[i] = `"` + column + `"`
	}
	rows, err := ex.Query(`SELECT `+strings.Join(quoted, ", ")+` FROM `+table+` WHERE `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("snapshot %s: %w", table, err)
	}
	defer rows.Close()

	var out []tableSnapshot
	for rows.Next() {
		cells := make([]sql.NullString, len(columns))
		scan := make([]any, len(columns))
		for i := range cells {
			scan[i] = &cells[i]
		}
		if err := rows.Scan(scan...); err != nil {
			return nil, fmt.Errorf("snapshot %s scan: %w", table, err)
		}
		values := make([]any, len(columns))
		for i, cell := range cells {
			if cell.Valid {
				values[i] = cell.String
			}
		}
		out = append(out, tableSnapshot{Columns: columns, Values: values})
	}
	return out, rows.Err()
}

// tableColumns lists the column names of one table in declaration order.
func tableColumns(ex sqlExecutor, table string) ([]string, error) {
	rows, err := ex.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return nil, fmt.Errorf("snapshot %s columns: %w", table, err)
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var (
			cid       int
			name      string
			ctype     string
			notNull   int
			dfltValue sql.NullString
			pk        int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dfltValue, &pk); err != nil {
			return nil, fmt.Errorf("snapshot %s columns scan: %w", table, err)
		}
		columns = append(columns, name)
	}
	return columns, rows.Err()
}

// restoreRouteRows rebuilds the route (and its members) a snapshot captured and
// returns the route id. Rows keep their original ids: route_members.route_id
// and routes.single_member_id therefore point at the right rows again without
// any remapping, and SQLite's AUTOINCREMENT guarantees the freed ids were not
// handed out to anything else in the meantime.
func restoreRouteRows(tx *sql.Tx, payload string) (int64, error) {
	unreadable := &UnifyValidationError{Message: "unify: this batch has no usable snapshot of the deleted route — recreate the model route by hand"}
	var snap routeSnapshot
	if err := json.Unmarshal([]byte(strings.TrimSpace(payload)), &snap); err != nil {
		return 0, unreadable
	}
	if len(snap.Route.Columns) == 0 || len(snap.Route.Values) == 0 {
		return 0, unreadable
	}
	pattern := snapshotColumn(snap.Route, "model_pattern")
	if pattern == "" {
		return 0, unreadable
	}
	var existing int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM routes WHERE model_pattern = ?`, pattern).Scan(&existing); err != nil {
		return 0, fmt.Errorf("unify rebuild check: %w", err)
	}
	if existing > 0 {
		return 0, &UnifyValidationError{Message: "cannot rebuild: a route with this model name already exists"}
	}
	if id := snapshotID(snap.Route); id > 0 {
		var idTaken int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM routes WHERE id = ?`, id).Scan(&idTaken); err != nil {
			return 0, fmt.Errorf("unify rebuild check: %w", err)
		}
		if idTaken > 0 {
			return 0, &UnifyValidationError{Message: "cannot rebuild: the original route id has been reused"}
		}
	}
	routeID, err := insertSnapshotRow(tx, "routes", snap.Route, pinOverrides(snap.Route))
	if err != nil {
		return 0, err
	}
	for _, member := range snap.Members {
		if len(member.Columns) == 0 || len(member.Values) == 0 {
			continue
		}
		// A member is only useful while its channel exists; restore the route
		// without it and say so, rather than failing on a foreign key.
		if channelID := snapshotIDOf(member, "channel_id"); channelID > 0 {
			var exists int
			if err := tx.QueryRow(`SELECT COUNT(*) FROM channels WHERE id = ?`, channelID).Scan(&exists); err != nil {
				return 0, fmt.Errorf("unify rebuild member check: %w", err)
			}
			if exists == 0 {
				return 0, &UnifyValidationError{Message: fmt.Sprintf("cannot rebuild: channel %d no longer exists; re-create the member by hand", channelID)}
			}
		}
		if _, err := insertSnapshotRow(tx, "route_members", member, nil); err != nil {
			return 0, err
		}
	}
	if pinned, ok := snapshotNullValue(snap.Route, "single_member_id"); ok {
		if _, err := tx.Exec(`UPDATE routes SET single_member_id = ? WHERE id = ?`, pinned, routeID); err != nil {
			return 0, fmt.Errorf("unify rebuild pin: %w", err)
		}
	}
	return routeID, nil
}

// pinOverrides strips a route's pinned member while it is being inserted: the
// member does not exist yet, and with foreign keys enforced SQLite would reject
// the insert ordering. The pin is written back once the members are in place.
func pinOverrides(snap tableSnapshot) map[string]any {
	if _, ok := snapshotNullValue(snap, "single_member_id"); !ok {
		return nil
	}
	return map[string]any{"single_member_id": nil}
}

func insertSnapshotRow(tx *sql.Tx, table string, snap tableSnapshot, overrides map[string]any) (int64, error) {
	if len(snap.Columns) != len(snap.Values) {
		return 0, &UnifyValidationError{Message: "unify: the recorded snapshot is malformed"}
	}
	quoted := make([]string, len(snap.Columns))
	placeholders := make([]string, len(snap.Columns))
	values := make([]any, len(snap.Columns))
	for i, column := range snap.Columns {
		quoted[i] = `"` + column + `"`
		placeholders[i] = "?"
		values[i] = snap.Values[i]
	}
	for column, value := range overrides {
		for i, name := range snap.Columns {
			if name == column {
				values[i] = value
			}
		}
	}
	res, err := tx.Exec(`INSERT INTO `+table+` (`+strings.Join(quoted, ", ")+`) VALUES (`+strings.Join(placeholders, ", ")+`)`, values...)
	if err != nil {
		return 0, fmt.Errorf("unify rebuild %s: %w", table, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("unify rebuild %s id: %w", table, err)
	}
	return id, nil
}

// snapshotID reads the captured primary key, 0 when it is absent.
func snapshotID(snap tableSnapshot) int64 {
	return snapshotIDOf(snap, "id")
}

// snapshotColumn returns one captured value by column name, "" when missing.
func snapshotColumn(snap tableSnapshot, column string) string {
	if value, ok := snapshotNullValue(snap, column); ok {
		if text, isText := value.(string); isText {
			return text
		}
	}
	return ""
}

// snapshotNullValue returns one captured value, reporting false both for a
// missing column and for a SQL NULL.
func snapshotNullValue(snap tableSnapshot, column string) (any, bool) {
	for i, name := range snap.Columns {
		if name == column && i < len(snap.Values) {
			if snap.Values[i] == nil {
				return nil, false
			}
			return snap.Values[i], true
		}
	}
	return nil, false
}

// snapshotIDOf reads a numeric column out of a captured row.
func snapshotIDOf(snap tableSnapshot, column string) int64 {
	value, ok := snapshotNullValue(snap, column)
	if !ok {
		return 0
	}
	text, isText := value.(string)
	if !isText {
		return 0
	}
	var n int64
	if _, err := fmt.Sscanf(text, "%d", &n); err != nil {
		return 0
	}
	return n
}

// routeExistsByID reports whether a route row is still present, used to tell a
// deleted original apart from one that is merely parked.
func routeExistsByID(ex sqlExecutor, routeID int64) (bool, error) {
	var n int
	if err := ex.QueryRow(`SELECT COUNT(*) FROM routes WHERE id = ?`, routeID).Scan(&n); err != nil {
		return false, fmt.Errorf("unify route presence: %w", err)
	}
	return n > 0, nil
}

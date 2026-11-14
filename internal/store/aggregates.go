package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
)

// Aggregate is one console aggregate model: a public name that fans out to the
// members of several existing models. The route and its members live in the
// ordinary route tables — this row only records that the name was created as an
// aggregate and links back to it, so the console can list aggregates without
// guessing which routes are aggregates.
type Aggregate struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	RouteID   int64     `json:"route_id,omitempty"`
	Note      string    `json:"note,omitempty"`
	CreatedAt time.Time `json:"created_at,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

// CreateAggregate records an aggregate whose route has just been created. The
// unique name index rejects a second aggregate with the same public name; the
// caller surfaces that as a conflict.
func (db *DB) CreateAggregate(name string, routeID int64, note string) (int64, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, fmt.Errorf("aggregate name required")
	}
	res, err := db.Exec(`INSERT INTO model_aggregates(name, route_id, note) VALUES(?,?,?)`,
		name, routeID, note)
	if err != nil {
		return 0, fmt.Errorf("aggregate create: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("aggregate create id: %w", err)
	}
	return id, nil
}

// UpdateAggregate renames/re-notes an aggregate. The route is untouched: the
// route's model_pattern only changes through the aggregate-create flow (which
// sets both atomically) or the plain route editor.
func (db *DB) UpdateAggregate(id int64, name, note string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("aggregate name required")
	}
	res, err := db.Exec(`UPDATE model_aggregates SET name=?, note=?, updated_at=datetime('now') WHERE id=?`,
		name, note, id)
	if err != nil {
		return fmt.Errorf("aggregate update: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// SetAggregateRoute re-points the aggregate at a (new) route id, after a member
// rebuild or a re-attach. routeID 0 clears the link (stored as NULL so the
// foreign key holds).
func (db *DB) SetAggregateRoute(id, routeID int64) error {
	var res sql.Result
	var err error
	if routeID > 0 {
		res, err = db.Exec(`UPDATE model_aggregates SET route_id=?, updated_at=datetime('now') WHERE id=?`,
			routeID, id)
	} else {
		res, err = db.Exec(`UPDATE model_aggregates SET route_id=NULL, updated_at=datetime('now') WHERE id=?`, id)
	}
	if err != nil {
		return fmt.Errorf("aggregate set route: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// DeleteAggregate removes the record; the route (if any) is the caller's
// decision — deleting the route deletes its members, which is what the console
// confirms separately.
func (db *DB) DeleteAggregate(id int64) error {
	res, err := db.Exec(`DELETE FROM model_aggregates WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("aggregate delete: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// AggregateByRoute finds the aggregate record behind a route, if the route was
// created as an aggregate. Not found is (nil, nil).
func (db *DB) AggregateByRoute(routeID int64) (*Aggregate, error) {
	if routeID <= 0 {
		return nil, nil
	}
	row := db.QueryRow(`SELECT id,name,COALESCE(route_id,0),note,created_at,updated_at
		FROM model_aggregates WHERE route_id=?`, routeID)
	agg, err := scanAggregateRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return agg, nil
}

// AggregateByID returns one aggregate, or nil when the id is unknown.
func (db *DB) AggregateByID(id int64) (*Aggregate, error) {
	row := db.QueryRow(`SELECT id,name,COALESCE(route_id,0),note,created_at,updated_at
		FROM model_aggregates WHERE id=?`, id)
	var a Aggregate
	var createdAt, updatedAt string
	err := row.Scan(&a.ID, &a.Name, &a.RouteID, &a.Note, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("aggregate get: %w", err)
	}
	a.CreatedAt = parseFlexibleTime(createdAt)
	a.UpdatedAt = parseFlexibleTime(updatedAt)
	return &a, nil
}

// CreateRouteMember inserts one member through the DB handle — the admin
// handlers build members outside a store method that owns a transaction (the
// aggregate create fans out several members around its own route insert).
func (db *DB) CreateRouteMember(r *domain.RouteMember) (int64, error) {
	return (&RouteMemberStore{db: db.DB}).Create(r)
}

// ListAggregates returns every aggregate, oldest first. RouteID may point at a
// route that has since been deleted; the handler resolves liveness per row.
func (db *DB) ListAggregates() ([]Aggregate, error) {
	rows, err := db.Query(`SELECT id,name,COALESCE(route_id,0),note,created_at,updated_at
		FROM model_aggregates ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("aggregate list: %w", err)
	}
	defer rows.Close()
	var out []Aggregate
	for rows.Next() {
		agg, err := scanAggregate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *agg)
	}
	return out, rows.Err()
}

func scanAggregate(rows *sql.Rows) (*Aggregate, error) {
	var a Aggregate
	var createdAt, updatedAt string
	if err := rows.Scan(&a.ID, &a.Name, &a.RouteID, &a.Note, &createdAt, &updatedAt); err != nil {
		return nil, fmt.Errorf("aggregate scan: %w", err)
	}
	finalizeAggregate(&a, createdAt, updatedAt)
	return &a, nil
}

func scanAggregateRow(row *sql.Row) (*Aggregate, error) {
	var a Aggregate
	var createdAt, updatedAt string
	if err := row.Scan(&a.ID, &a.Name, &a.RouteID, &a.Note, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	finalizeAggregate(&a, createdAt, updatedAt)
	return &a, nil
}

func finalizeAggregate(a *Aggregate, createdAt, updatedAt string) {
	// datetime('now') writes UTC; the seed columns may also carry RFC3339 from
	// older tooling, so both shapes parse rather than zeroing silently.
	a.CreatedAt = parseFlexibleTime(createdAt)
	a.UpdatedAt = parseFlexibleTime(updatedAt)
}

// parseFlexibleTime reads the two timestamp shapes the store writes
// (YYYY-MM-DD HH:MM:SS and RFC3339). Unparseable text becomes the zero time —
// these columns are metadata, never gating logic.
func parseFlexibleTime(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	if t, err := time.Parse("2006-01-02 15:04:05", raw); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t
	}
	return time.Time{}
}

// --- Console pins -----------------------------------------------------------

// AddPin pins one row of a board (kind) to the top. Re-pinning is a no-op so a
// double click cannot error.
func (db *DB) AddPin(kind, ref string) error {
	if kind == "" || ref == "" {
		return fmt.Errorf("pin kind and ref required")
	}
	_, err := db.Exec(`INSERT INTO console_pins(kind, ref) VALUES(?,?)
		ON CONFLICT(kind, ref) DO NOTHING`, kind, ref)
	if err != nil {
		return fmt.Errorf("pin add: %w", err)
	}
	return nil
}

// RemovePin unpins. Unpinning something not pinned is a no-op.
func (db *DB) RemovePin(kind, ref string) error {
	_, err := db.Exec(`DELETE FROM console_pins WHERE kind=? AND ref=?`, kind, ref)
	if err != nil {
		return fmt.Errorf("pin remove: %w", err)
	}
	return nil
}

// Pins lists every pinned ref for a board, oldest first (stable display order).
func (db *DB) Pins(kind string) ([]string, error) {
	rows, err := db.Query(`SELECT ref FROM console_pins WHERE kind=? ORDER BY id`, kind)
	if err != nil {
		return nil, fmt.Errorf("pin list: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			return nil, fmt.Errorf("pin scan: %w", err)
		}
		out = append(out, ref)
	}
	return out, rows.Err()
}

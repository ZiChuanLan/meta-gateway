package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lan/meta-gateway/internal/domain"
)

// UnifyApplyVariant is one channel's original model name folded into a group.
type UnifyApplyVariant struct {
	ChannelID int64
	ModelName string
}

// UnifyApplyGroup is one canonical name plus the per-channel originals that
// should answer to it.
type UnifyApplyGroup struct {
	Canonical string
	Variants  []UnifyApplyVariant
}

// UnifyApplyOutcome reports what an apply did across all groups.
type UnifyApplyOutcome struct {
	RoutesCreated  int                 `json:"routes_created"`
	MembersCreated int                 `json:"members_created"`
	MembersSkipped int                 `json:"members_skipped"`
	RoutesArchived int                 `json:"routes_archived"`
	Batches        []domain.UnifyBatch `json:"batches"`
}

// UnifyValidationError marks an apply input the caller must fix, as opposed to
// a storage failure. Handlers map it to 400.
type UnifyValidationError struct{ Message string }

func (e *UnifyValidationError) Error() string { return e.Message }

// ApplyUnify creates the alias routes and per-channel mappings for every group
// in a single transaction, optionally archiving the original routes each group
// supersedes.
//
// Everything is recorded per group as a batch so UndoBatch can reverse it:
// routes and members created here are deleted, routes archived here are
// restored to the state they had before. Without those records an operator
// would have to guess which members came from unification — the old
// (auto=1 AND manual_override=1) fingerprint is indistinguishable from a
// manually pinned member.
//
// A route is only archived when the group provably covers it: the route must
// not be a wildcard and every one of its members must belong to a channel that
// this group binds to the canonical name. Anything else is left alone rather
// than risk hiding a binding nobody asked to retire.
func (db *DB) ApplyUnify(groups []UnifyApplyGroup, archiveOriginals bool) (*UnifyApplyOutcome, error) {
	out := &UnifyApplyOutcome{}
	tx, err := db.Begin()
	if err != nil {
		return nil, fmt.Errorf("unify apply begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	routes := &RouteStore{db: db.DB}
	members := &RouteMemberStore{db: db.DB}

	for _, group := range groups {
		canonical := strings.TrimSpace(group.Canonical)
		if canonical == "" || len(canonical) > 200 {
			return nil, &UnifyValidationError{Message: "invalid unify group"}
		}
		if len(group.Variants) == 0 {
			return nil, &UnifyValidationError{Message: "invalid unify variant"}
		}

		// GetByModelAnyTx, not GetByModel: a disabled route with this name
		// still exists, and inserting a second row would make
		// ListEnabledPatterns report the model twice.
		route, err := routes.GetByModelAnyTx(tx, canonical)
		if err != nil {
			return nil, err
		}
		routeID := int64(0)
		if route != nil {
			routeID = route.ID
		} else {
			routeID, err = routes.CreateTx(tx, &domain.Route{ModelPattern: canonical, Enabled: true})
			if err != nil {
				return nil, err
			}
			out.RoutesCreated++
		}

		batchID, err := createUnifyBatch(tx, canonical, routeID)
		if err != nil {
			return nil, err
		}
		seq := 0
		if route == nil {
			if err := addUnifyOp(tx, batchID, &seq, &domain.UnifyOp{
				Op:      domain.UnifyOpRouteCreated,
				RouteID: routeID,
			}); err != nil {
				return nil, err
			}
		}

		existing, err := members.ListByRouteTx(tx, routeID)
		if err != nil {
			return nil, err
		}
		for _, variant := range group.Variants {
			if variant.ChannelID <= 0 || strings.TrimSpace(variant.ModelName) == "" {
				return nil, &UnifyValidationError{Message: "invalid unify variant"}
			}
			// Skip when this channel already serves this exact upstream model
			// on the alias route. The real name, not just membership, decides:
			// a member pointing at a different model must not count as covered,
			// or the alias would gain a second binding and split traffic.
			skip := false
			for _, member := range existing {
				if member.ChannelID != variant.ChannelID {
					continue
				}
				real := mappingRealName(member.MappingJSON)
				if real == "" {
					real = canonical
				}
				if real == variant.ModelName {
					skip = true
					break
				}
			}
			if skip {
				out.MembersSkipped++
				continue
			}
			mapping, err := jsonMarshalRealName(variant.ModelName)
			if err != nil {
				return nil, err
			}
			memberID, err := members.CreateTx(tx, &domain.RouteMember{
				RouteID:        routeID,
				ChannelID:      variant.ChannelID,
				Priority:       0,
				Weight:         100,
				Enabled:        true,
				Auto:           true,
				ManualOverride: true,
				MappingJSON:    mapping,
			})
			if err != nil {
				return nil, err
			}
			if err := addUnifyOp(tx, batchID, &seq, &domain.UnifyOp{
				Op:       domain.UnifyOpMemberCreated,
				RouteID:  routeID,
				MemberID: &memberID,
			}); err != nil {
				return nil, err
			}
			out.MembersCreated++
		}

		if archiveOriginals {
			archived, err := archiveSupersededRoutes(tx, routes, members, batchID, &seq, canonical, routeID, group.Variants)
			if err != nil {
				return nil, err
			}
			out.RoutesArchived += archived
		}

		batch := domain.UnifyBatch{
			ID:             batchID,
			Canonical:      canonical,
			RouteID:        routeID,
			RoutesArchived: archivedCount(tx, batchID),
		}
		if route == nil {
			batch.RoutesCreated = 1
		}
		batch.MembersCreated = countOps(tx, batchID, domain.UnifyOpMemberCreated)
		if err := finalizeUnifyBatch(tx, &batch); err != nil {
			return nil, err
		}
		out.Batches = append(out.Batches, batch)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("unify apply commit: %w", err)
	}
	return out, nil
}

// archiveSupersededRoutes disables the original routes that this group fully
// covers, recording each one so undo can bring it back.
func archiveSupersededRoutes(tx *sql.Tx, routes *RouteStore, members *RouteMemberStore, batchID int64, seq *int, canonical string, routeID int64, variants []UnifyApplyVariant) (int, error) {
	// Channels bound to each original name by this group.
	covered := make(map[string]map[int64]struct{})
	for _, v := range variants {
		name := strings.TrimSpace(v.ModelName)
		if covered[name] == nil {
			covered[name] = make(map[int64]struct{})
		}
		covered[name][v.ChannelID] = struct{}{}
	}

	done := make(map[int64]struct{})
	archived := 0
	for name, channels := range covered {
		if name == canonical || strings.ContainsAny(name, "*?") {
			continue
		}
		original, err := getExactRoute(tx, name, false)
		if err != nil || original == nil || original.ID == routeID {
			continue
		}
		if _, ok := done[original.ID]; ok {
			continue
		}
		done[original.ID] = struct{}{}

		// Only archive when every member of the original is covered.
		existing, err := members.ListByRouteTx(tx, original.ID)
		if err != nil {
			return archived, err
		}
		uncovered := false
		for _, member := range existing {
			if _, ok := channels[member.ChannelID]; !ok {
				uncovered = true
				break
			}
		}
		if uncovered || len(existing) == 0 {
			continue
		}
		if !original.Enabled {
			// Already hidden; nothing to restore later.
			continue
		}
		prev := original.Enabled
		if err := routes.SetEnabledTx(tx, original.ID, false); err != nil {
			return archived, err
		}
		if err := addUnifyOp(tx, batchID, seq, &domain.UnifyOp{
			Op:          domain.UnifyOpRouteArchived,
			RouteID:     original.ID,
			PrevEnabled: &prev,
		}); err != nil {
			return archived, err
		}
		archived++
	}
	return archived, nil
}

// UndoBatch reverses a batch: members and routes it created are deleted again,
// routes it archived are restored. Operations already undone, or whose target
// has since been deleted by hand, are skipped rather than aborting — a partial
// revert still beats none.
func (db *DB) UndoBatch(batchID int64) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("unify undo begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	batch, err := getUnifyBatch(tx, batchID)
	if err != nil {
		return err
	}
	if batch == nil {
		return &UnifyValidationError{Message: "unify batch not found"}
	}
	if batch.UndoneAt != nil {
		return tx.Commit()
	}

	ops, err := listUnifyOps(tx, batchID)
	if err != nil {
		return err
	}
	routes := &RouteStore{db: db.DB}
	members := &RouteMemberStore{db: db.DB}
	for i := len(ops) - 1; i >= 0; i-- {
		op := ops[i]
		if op.Undone {
			continue
		}
		switch op.Op {
		case domain.UnifyOpRouteCreated:
			if err := routes.DeleteTx(tx, op.RouteID); err != nil {
				return err
			}
		case domain.UnifyOpMemberCreated:
			if op.MemberID == nil {
				break
			}
			if err := members.DeleteTx(tx, *op.MemberID); err != nil {
				return err
			}
		case domain.UnifyOpRouteArchived:
			if op.PrevEnabled == nil {
				break
			}
			if err := routes.SetEnabledTx(tx, op.RouteID, *op.PrevEnabled); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`UPDATE model_unify_ops SET undone = 1 WHERE id = ?`, op.ID); err != nil {
			return fmt.Errorf("unify op mark undone: %w", err)
		}
	}
	// The alias route itself may have been created by the batch and already
	// deleted above; only mark the batch undone, never touch the row again.
	if _, err := tx.Exec(`UPDATE model_unify_batches SET undone_at = datetime('now') WHERE id = ?`, batchID); err != nil {
		return fmt.Errorf("unify batch mark undone: %w", err)
	}
	return tx.Commit()
}

// RestoreArchivedRoute re-enables one route a batch archived and marks that
// single operation undone, leaving the rest of the batch in place. Use this to
// bring one original name back without discarding the whole alias.
func (db *DB) RestoreArchivedRoute(routeID int64) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("unify restore begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var opID int64
	var prev int
	err = tx.QueryRow(`SELECT id, prev_enabled FROM model_unify_ops
		WHERE op = ? AND route_id = ? AND undone = 0 ORDER BY seq LIMIT 1`,
		domain.UnifyOpRouteArchived, routeID).Scan(&opID, &prev)
	if err == sql.ErrNoRows {
		return &UnifyValidationError{Message: "archived route not found"}
	}
	if err != nil {
		return fmt.Errorf("unify restore lookup: %w", err)
	}
	routes := &RouteStore{db: db.DB}
	if err := routes.SetEnabledTx(tx, routeID, prev != 0); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE model_unify_ops SET undone = 1 WHERE id = ?`, opID); err != nil {
		return fmt.Errorf("unify restore mark undone: %w", err)
	}
	return tx.Commit()
}

// ListUnifyBatches returns the most recent batches, undone ones included.
func (db *DB) ListUnifyBatches(limit int) ([]domain.UnifyBatch, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := db.Query(`SELECT id, canonical, route_id, routes_created, members_created, routes_archived, created_at, undone_at
		FROM model_unify_batches ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("unify batch list: %w", err)
	}
	defer rows.Close()
	var result []domain.UnifyBatch
	for rows.Next() {
		var b domain.UnifyBatch
		if err := rows.Scan(&b.ID, &b.Canonical, &b.RouteID, &b.RoutesCreated, &b.MembersCreated,
			&b.RoutesArchived, scanTime(&b.CreatedAt), scanNullTime(&b.UndoneAt)); err != nil {
			return nil, fmt.Errorf("unify batch scan: %w", err)
		}
		result = append(result, b)
	}
	return result, rows.Err()
}

// UnifyBatchOps returns the recorded changes of a batch in application order.
func (db *DB) UnifyBatchOps(batchID int64) ([]domain.UnifyOp, error) {
	return listUnifyOps(db.DB, batchID)
}

// UnifyArchivedRoutes lists routes currently hidden by an active unification
// batch, newest first. These are the names an operator can bring back without
// undoing the whole group.
func (db *DB) UnifyArchivedRoutes() ([]domain.ArchivedRoute, error) {
	rows, err := db.Query(`SELECT o.batch_id, b.canonical, o.route_id, r.model_pattern, b.created_at
		FROM model_unify_ops o
		JOIN model_unify_batches b ON b.id = o.batch_id
		JOIN routes r ON r.id = o.route_id
		WHERE o.op = ? AND o.undone = 0 AND b.undone_at IS NULL
		ORDER BY b.id DESC, o.seq`, domain.UnifyOpRouteArchived)
	if err != nil {
		return nil, fmt.Errorf("unify archived list: %w", err)
	}
	defer rows.Close()
	var result []domain.ArchivedRoute
	for rows.Next() {
		var a domain.ArchivedRoute
		if err := rows.Scan(&a.BatchID, &a.Canonical, &a.RouteID, &a.ModelName, scanTime(&a.ArchivedAt)); err != nil {
			return nil, fmt.Errorf("unify archived scan: %w", err)
		}
		result = append(result, a)
	}
	return result, rows.Err()
}

// --- helpers ---------------------------------------------------------------

func createUnifyBatch(tx *sql.Tx, canonical string, routeID int64) (int64, error) {
	res, err := tx.Exec(`INSERT INTO model_unify_batches (canonical, route_id) VALUES (?, ?)`, canonical, routeID)
	if err != nil {
		return 0, fmt.Errorf("unify batch create: %w", err)
	}
	return res.LastInsertId()
}

func finalizeUnifyBatch(tx *sql.Tx, b *domain.UnifyBatch) error {
	if _, err := tx.Exec(`UPDATE model_unify_batches SET routes_created = ?, members_created = ?, routes_archived = ? WHERE id = ?`,
		b.RoutesCreated, b.MembersCreated, b.RoutesArchived, b.ID); err != nil {
		return fmt.Errorf("unify batch finalize: %w", err)
	}
	return nil
}

func addUnifyOp(tx *sql.Tx, batchID int64, seq *int, op *domain.UnifyOp) error {
	*seq++
	var memberID any
	if op.MemberID != nil {
		memberID = *op.MemberID
	}
	var prev any
	if op.PrevEnabled != nil {
		prev = boolInt(*op.PrevEnabled)
	}
	if _, err := tx.Exec(`INSERT INTO model_unify_ops (batch_id, seq, op, route_id, member_id, prev_enabled) VALUES (?, ?, ?, ?, ?, ?)`,
		batchID, *seq, op.Op, op.RouteID, memberID, prev); err != nil {
		return fmt.Errorf("unify op record: %w", err)
	}
	return nil
}

func listUnifyOps(ex sqlExecutor, batchID int64) ([]domain.UnifyOp, error) {
	rows, err := ex.Query(`SELECT id, batch_id, seq, op, route_id, member_id, prev_enabled, undone
		FROM model_unify_ops WHERE batch_id = ? ORDER BY seq`, batchID)
	if err != nil {
		return nil, fmt.Errorf("unify op list: %w", err)
	}
	defer rows.Close()
	var result []domain.UnifyOp
	for rows.Next() {
		var op domain.UnifyOp
		var memberID, prev sql.NullInt64
		var undone int
		if err := rows.Scan(&op.ID, &op.BatchID, &op.Seq, &op.Op, &op.RouteID, &memberID, &prev, &undone); err != nil {
			return nil, fmt.Errorf("unify op scan: %w", err)
		}
		if memberID.Valid {
			v := memberID.Int64
			op.MemberID = &v
		}
		if prev.Valid {
			v := prev.Int64 != 0
			op.PrevEnabled = &v
		}
		op.Undone = undone != 0
		result = append(result, op)
	}
	return result, rows.Err()
}

func getUnifyBatch(ex sqlExecutor, id int64) (*domain.UnifyBatch, error) {
	var b domain.UnifyBatch
	err := ex.QueryRow(`SELECT id, canonical, route_id, routes_created, members_created, routes_archived, created_at, undone_at
		FROM model_unify_batches WHERE id = ?`, id).
		Scan(&b.ID, &b.Canonical, &b.RouteID, &b.RoutesCreated, &b.MembersCreated,
			&b.RoutesArchived, scanTime(&b.CreatedAt), scanNullTime(&b.UndoneAt))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("unify batch get: %w", err)
	}
	return &b, nil
}

func countOps(ex sqlExecutor, batchID int64, op string) int {
	var n int
	_ = ex.QueryRow(`SELECT COUNT(*) FROM model_unify_ops WHERE batch_id = ? AND op = ?`, batchID, op).Scan(&n)
	return n
}

func archivedCount(ex sqlExecutor, batchID int64) int {
	return countOps(ex, batchID, domain.UnifyOpRouteArchived)
}

// mappingRealName extracts {"real": "..."} from a member's mapping JSON. An
// empty result means the member serves the route's own name unchanged, which
// is how plain (non-alias) members are stored.
func mappingRealName(mappingJSON string) string {
	if strings.TrimSpace(mappingJSON) == "" {
		return ""
	}
	var mapping struct {
		Real string `json:"real"`
	}
	if err := json.Unmarshal([]byte(mappingJSON), &mapping); err != nil {
		return ""
	}
	return strings.TrimSpace(mapping.Real)
}

// jsonMarshalRealName builds the per-member alias redirect that unification
// stores, so one alias route can reach a differently named model per channel.
func jsonMarshalRealName(real string) (string, error) {
	encoded, err := json.Marshal(map[string]string{"real": real})
	if err != nil {
		return "", fmt.Errorf("unify mapping encode: %w", err)
	}
	return string(encoded), nil
}

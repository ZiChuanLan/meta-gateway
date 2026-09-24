package domain

import "time"

// Unify op kinds recorded in model_unify_ops. Undo replays them in reverse.
const (
	UnifyOpRouteCreated  = "route_created"
	UnifyOpMemberCreated = "member_created"
	// UnifyOpRouteArchived is the pre-deletion form of UnifyOpRouteDeleted:
	// batches applied before route removal existed parked the superseded route
	// disabled (PrevEnabled keeps its state). Undo and rebuild still understand
	// them, so an old batch stays reversible after the upgrade.
	UnifyOpRouteArchived = "route_archived"
	// UnifyOpRouteEnabled records an apply that switched a pre-existing
	// disabled route with the canonical name back on — without it the alias
	// would silently never serve. Undo flips it back to PrevEnabled.
	UnifyOpRouteEnabled = "route_enabled"
	// UnifyOpRouteDeleted records that a superseded original was removed
	// outright, together with a snapshot of the rows that went away.
	UnifyOpRouteDeleted = "route_deleted"
)

// unifyRemovalOps are the op kinds that removed a superseded original route,
// newest scheme first. Both stay listed in the history so a batch applied
// before the switch to deletion can still be undone.
func IsUnifyRemovalOp(op string) bool {
	return op == UnifyOpRouteDeleted || op == UnifyOpRouteArchived
}

// UnifyBatch is one applied unification group. UndoneAt is nil while the batch
// is still in effect; set once it has been reverted.
type UnifyBatch struct {
	ID             int64      `json:"id"`
	Canonical      string     `json:"canonical"`
	RouteID        int64      `json:"route_id"`
	RoutesCreated  int        `json:"routes_created"`
	MembersCreated int        `json:"members_created"`
	RoutesDeleted  int        `json:"routes_deleted"`
	CreatedAt      time.Time  `json:"created_at"`
	UndoneAt       *time.Time `json:"undone_at,omitempty"`
}

// Active reports whether the batch is still in effect.
func (b UnifyBatch) Active() bool { return b.UndoneAt == nil }

// DeletedRoute is an original model name that an active unification batch
// removed. Rebuilding it restores the route and its members from the batch
// snapshot without discarding the alias. Rebuilt entries stay listed (greyed
// out in the UI) so a single rebuild leaves a visible trace in the history
// instead of silently vanishing.
type DeletedRoute struct {
	BatchID   int64     `json:"batch_id"`
	Canonical string    `json:"canonical"`
	RouteID   int64     `json:"route_id"`
	ModelName string    `json:"model_name"`
	DeletedAt time.Time `json:"deleted_at"`
	// Parked marks the older scheme: the batch switched the original off
	// instead of deleting it, so the row still exists and reverting the op
	// turns it back on (no snapshot involved).
	Parked bool `json:"parked,omitempty"`
	// Rebuilt is true when this removal has been reverted (single rebuild or
	// batch undo), i.e. the original route exists again.
	Rebuilt bool `json:"rebuilt"`
}

// UnifyOp is a single recorded mutation belonging to a batch.
type UnifyOp struct {
	ID       int64  `json:"id"`
	BatchID  int64  `json:"batch_id"`
	Seq      int    `json:"seq"`
	Op       string `json:"op"`
	RouteID  int64  `json:"route_id"`
	MemberID *int64 `json:"member_id,omitempty"`
	// PrevEnabled is set only for UnifyOpRouteArchived: the route's enabled
	// state before archiving, restored verbatim on undo.
	PrevEnabled *bool `json:"prev_enabled,omitempty"`
	// ModelName is the removed route's pattern. Kept on the op because the
	// route row itself is gone after a deletion, and the history must still be
	// able to name what went away.
	ModelName string `json:"model_name,omitempty"`
	// Snapshot is the verbatim copy of the deleted route and member rows. It is
	// internal plumbing (large, only ever read back by a rebuild), so it stays
	// out of the JSON the console sees.
	Snapshot string `json:"-"`
	Undone   bool   `json:"undone"`
}

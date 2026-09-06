package domain

import "time"

// Unify op kinds recorded in model_unify_ops. Undo replays them in reverse.
const (
	UnifyOpRouteCreated  = "route_created"
	UnifyOpMemberCreated = "member_created"
	UnifyOpRouteArchived = "route_archived"
	// UnifyOpRouteEnabled records an apply that switched a pre-existing
	// disabled route with the canonical name back on — without it the alias
	// would silently never serve. Undo flips it back to PrevEnabled.
	UnifyOpRouteEnabled = "route_enabled"
)

// UnifyBatch is one applied unification group. UndoneAt is nil while the batch
// is still in effect; set once it has been reverted.
type UnifyBatch struct {
	ID             int64      `json:"id"`
	Canonical      string     `json:"canonical"`
	RouteID        int64      `json:"route_id"`
	RoutesCreated  int        `json:"routes_created"`
	MembersCreated int        `json:"members_created"`
	RoutesArchived int        `json:"routes_archived"`
	CreatedAt      time.Time  `json:"created_at"`
	UndoneAt       *time.Time `json:"undone_at,omitempty"`
}

// Active reports whether the batch is still in effect.
func (b UnifyBatch) Active() bool { return b.UndoneAt == nil }

// ArchivedRoute is an original model name that an active unification batch
// hid. Restoring it re-enables the route without discarding the alias.
// Restored entries stay listed (greyed out in the UI) so a single restore
// leaves a visible trace in the history instead of silently vanishing.
type ArchivedRoute struct {
	BatchID    int64     `json:"batch_id"`
	Canonical  string    `json:"canonical"`
	RouteID    int64     `json:"route_id"`
	ModelName  string    `json:"model_name"`
	ArchivedAt time.Time `json:"archived_at"`
	// Restored is true when this archive has been reverted (single restore or
	// batch undo), i.e. the name is not currently hidden.
	Restored bool `json:"restored"`
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
	Undone      bool  `json:"undone"`
}

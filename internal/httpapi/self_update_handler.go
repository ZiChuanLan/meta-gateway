// Admin self-update surfaces: availability/status for the console's one-click
// container update and the apply endpoint that starts the handoff. All of it
// lives behind the admin token; apply actions land in the audit log.
package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/lan/meta-gateway/internal/backup"
	"github.com/lan/meta-gateway/internal/buildinfo"
	"github.com/lan/meta-gateway/internal/selfupdate"
	"github.com/lan/meta-gateway/internal/store"
	"github.com/lan/meta-gateway/internal/updatecheck"
)

type SelfUpdateHandler struct {
	updater     *selfupdate.Service
	updateCheck *updatecheck.Service
	backups     *backup.Service
	db          *store.DB
}

func NewSelfUpdateHandler(updater *selfupdate.Service, updateCheck *updatecheck.Service, backups *backup.Service, db *store.DB) *SelfUpdateHandler {
	return &SelfUpdateHandler{updater: updater, updateCheck: updateCheck, backups: backups, db: db}
}

func (h *SelfUpdateHandler) Register(r chi.Router) {
	r.Get("/self-update", h.status)
	r.Post("/self-update/apply", h.apply)
}

func (h *SelfUpdateHandler) status(w http.ResponseWriter, r *http.Request) {
	if !teamOwner(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, h.updater.Status())
}

func (h *SelfUpdateHandler) apply(w http.ResponseWriter, r *http.Request) {
	if !teamOwner(w, r) {
		return
	}
	var req struct {
		Target string `json:"target"`
	}
	if err := decodeJSON(w, r, &req, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	target := req.Target

	// The target must be a newer release than the running build, and when the
	// update check has a cached latest it must agree — the button exists to
	// install what the console showed, not an arbitrary tag.
	if !updatecheck.IsNewer(target, buildinfo.Version) {
		h.audit(r, target, "rejected")
		writeError(w, http.StatusBadRequest, "target must be a newer release than the running version")
		return
	}
	checked := h.updateCheck.Refresh(r.Context())
	if checked.Err != "" || !checked.HasUpdate {
		h.audit(r, target, "rejected")
		writeError(w, 409, "no verified update available; check channel and update settings")
		return
	}
	if latest := checked.Latest; latest == "" || latest != target {
		h.audit(r, target, "rejected")
		writeError(w, http.StatusConflict, "target does not match the latest release ("+latest+"); re-run the check first")
		return
	}

	// Everything that can reject this request has rejected it; the last step
	// before the handoff is the snapshot. An upgrade runs migrations, migrations
	// are one-way, and this is the final moment the current data exists in its old
	// shape — so the click takes the backup instead of asking the operator to copy
	// a volume first, which is how a one-click update turns into a documentation
	// exercise. A backup that cannot be taken (or verified) stops the update.
	backupName, err := h.preUpdateBackup(r.Context())
	if err != nil {
		h.audit(r, target, "backup_failed")
		writeError(w, http.StatusConflict, "update not started: "+err.Error())
		return
	}

	if err := h.updater.StartTarget(target); err != nil {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, selfupdate.ErrUnavailable):
			status = http.StatusConflict
		case errors.Is(err, selfupdate.ErrAlreadyRuning):
			status = http.StatusConflict
		case errors.Is(err, selfupdate.ErrTrackMismatch):
			status = http.StatusConflict
		case errors.Is(err, selfupdate.ErrNoContainer):
			status = http.StatusConflict
		}
		h.audit(r, target, "rejected")
		writeError(w, status, err.Error())
		return
	}
	h.audit(r, target, "accepted")
	// The handoff pulls the image and starts the successor; this container
	// stops as soon as the successor tears it down. The console watches
	// /healthz for the new version from here on.
	writeJSON(w, http.StatusAccepted, map[string]any{
		"started": true,
		"target":  target,
		"backup":  backupName,
	})
}

// preUpdateBackup snapshots the database into the backup directory and returns
// the snapshot's name. It refuses when no directory is configured: the caller's
// next step is a one-way migration, so "no backup available" is not a warning.
func (h *SelfUpdateHandler) preUpdateBackup(ctx context.Context) (string, error) {
	if h.backups == nil || h.backups.Dir() == "" {
		return "", errors.New("BACKUP_DIR is not configured, and an upgrade cannot be undone without a snapshot")
	}
	record, err := h.backups.Create(ctx)
	if err != nil {
		return "", err
	}
	return record.Name, nil
}

func (h *SelfUpdateHandler) audit(r *http.Request, target, outcome string) {
	if h.db == nil {
		return
	}
	requestID, _ := r.Context().Value(chimw.RequestIDKey).(string)
	_ = h.db.AuditEvent.Insert(&store.AuditEvent{
		RequestID:  requestID,
		ActorKind:  "admin",
		Action:     "self_update.apply",
		Outcome:    outcome,
		Category:   "target=" + target,
		StatusCode: http.StatusOK,
	})
}

// Admin self-update surfaces: availability/status for the console's one-click
// container update and the apply endpoint that starts the handoff. All of it
// lives behind the admin token; apply actions land in the audit log.
package httpapi

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/lan/meta-gateway/internal/buildinfo"
	"github.com/lan/meta-gateway/internal/selfupdate"
	"github.com/lan/meta-gateway/internal/store"
	"github.com/lan/meta-gateway/internal/updatecheck"
)

type SelfUpdateHandler struct {
	updater     *selfupdate.Service
	updateCheck *updatecheck.Service
	db          *store.DB
}

func NewSelfUpdateHandler(updater *selfupdate.Service, updateCheck *updatecheck.Service, db *store.DB) *SelfUpdateHandler {
	return &SelfUpdateHandler{updater: updater, updateCheck: updateCheck, db: db}
}

func (h *SelfUpdateHandler) Register(r chi.Router) {
	r.Get("/self-update", h.status)
	r.Post("/self-update/apply", h.apply)
}

func (h *SelfUpdateHandler) status(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.updater.Status())
}

func (h *SelfUpdateHandler) apply(w http.ResponseWriter, r *http.Request) {
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
	if latest := h.updateCheck.Status().Latest; latest != "" && latest != target {
		h.audit(r, target, "rejected")
		writeError(w, http.StatusConflict, "target does not match the latest release ("+latest+"); re-run the check first")
		return
	}

	if err := h.updater.Start(); err != nil {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, selfupdate.ErrUnavailable):
			status = http.StatusConflict
		case errors.Is(err, selfupdate.ErrAlreadyRuning):
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
	})
}

func (h *SelfUpdateHandler) audit(r *http.Request, target, outcome string) {
	if h.db == nil {
		return
	}
	_ = h.db.AuditEvent.Insert(&store.AuditEvent{
		RequestID:  r.Context().Value(chimw.RequestIDKey).(string),
		ActorKind:  "admin",
		Action:     "self_update.apply",
		Outcome:    outcome,
		Category:   "target=" + target,
		StatusCode: http.StatusOK,
	})
}

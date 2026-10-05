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
	r.Get("/update-channel", h.getChannel)
	r.Put("/update-channel", h.saveChannel)
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
	})
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

func (h *SelfUpdateHandler) getChannel(w http.ResponseWriter, r *http.Request) {
	if !teamOwner(w, r) {
		return
	}
	p, err := h.db.OperatorPreferences()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	mode := h.updater.Mode()
	writeJSON(w, 200, map[string]any{"channel": p.UpdateChannel, "mode": mode, "tracking_tag": selfupdate.TrackingTag()})
}
func (h *SelfUpdateHandler) saveChannel(w http.ResponseWriter, r *http.Request) {
	if !teamOwner(w, r) {
		return
	}
	var req struct {
		Channel string `json:"channel"`
	}
	if err := decodeJSON(w, r, &req, 0, false); err != nil {
		return
	}
	if req.Channel != "stable" && req.Channel != "beta" {
		writeError(w, 400, "invalid update channel")
		return
	}
	if h.updater.Status().Running {
		writeError(w, 409, "update already running")
		return
	}
	if _, err := h.db.Exec(`UPDATE operator_preferences SET update_channel=? WHERE id=1`, req.Channel); err != nil {
		writeStoreError(w, err)
		return
	}
	h.audit(r, req.Channel, "channel_changed")
	h.getChannel(w, r)
}

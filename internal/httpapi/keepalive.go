package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/keepalive"
	"github.com/lan/meta-gateway/internal/store"
)

// KeepaliveHandler exposes the keepalive state and its two manual actions.
//
// The state view resolves each channel's window exactly the way the runner does
// (one shared query), so what this page shows and what the next round does cannot
// disagree — the failure an operator would notice only after a ban.
type KeepaliveHandler struct {
	db      *store.DB
	service *keepalive.Service
}

func NewKeepaliveHandler(db *store.DB, service *keepalive.Service) *KeepaliveHandler {
	return &KeepaliveHandler{db: db, service: service}
}

func (h *KeepaliveHandler) Register(r chi.Router) {
	r.Get("/keepalive", h.status)
	r.Post("/keepalive/run", h.run)
	r.Post("/keepalive/channels/{id}/send", h.send)
	r.Get("/keepalive/events", h.events)
	r.Put("/keepalive/sites/{id}", h.updateSite)
}

// keepaliveTargetView is one row of the console's keepalive list: the resolved
// window, how long the account has really been idle, and what would happen now.
type keepaliveTargetView struct {
	domain.KeepaliveTarget
	SiteName          string  `json:"site_name"`
	IdleDays          float64 `json:"idle_days"`
	RemainingDays     float64 `json:"remaining_days"`
	Ready             bool    `json:"ready"`
	LastCallAtDisplay string  `json:"last_call_at,omitempty"`
	NeverCalled       bool    `json:"never_called"`
}

type keepaliveStatus struct {
	Targets []keepaliveTargetView `json:"targets"`
	// Now is the server's clock, so the console does not have to guess the zone
	// the day counts (and quiet hours) are computed in.
	Now time.Time `json:"now"`
}

func (h *KeepaliveHandler) status(w http.ResponseWriter, _ *http.Request) {
	targets, err := h.service.Targets()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	now := time.Now()
	views := make([]keepaliveTargetView, 0, len(targets))
	for _, target := range targets {
		idle := target.IdleDays(now)
		views = append(views, keepaliveTargetView{
			KeepaliveTarget: clientTarget(target),
			SiteName:        target.SiteName,
			IdleDays:        idle,
			// Negative means the window is already past; the console shows that
			// rather than clamping it to zero, because "overdue" is the state
			// that needs attention.
			RemainingDays: target.Config.RemainingDays(idle),
			Ready:         target.Config.Ready(idle) && target.SkipReason == "",
			NeverCalled:   target.LastCallAt == nil,
		})
	}
	writeJSON(w, http.StatusOK, keepaliveStatus{Targets: views, Now: now})
}

// clientTarget strips the resolved window down to what the console needs and
// keeps the credential id out of the payload: the id is plumbing, the site and
// channel names are the facts an operator reads.
func clientTarget(target domain.KeepaliveTarget) domain.KeepaliveTarget {
	target.CredentialID = 0
	return target
}

func (h *KeepaliveHandler) run(w http.ResponseWriter, r *http.Request) {
	// Not r.Context(): a round can outlive the response, and the console should
	// not cancel it by navigating away.
	round, err := h.service.RunOnce(context.Background())
	if err != nil && !errors.Is(err, context.Canceled) {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, round)
}

func (h *KeepaliveHandler) send(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	event, err := h.service.SendNow(context.Background(), id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, event)
}

func (h *KeepaliveHandler) events(w http.ResponseWriter, r *http.Request) {
	limit := int(queryInt64Param(r, "limit"))
	if limit <= 0 {
		limit = 50
	}
	events, err := h.db.Channel.RecentKeepaliveEvents(limit)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, events)
}

type siteKeepaliveRequest struct {
	CallPolicy       string `json:"call_policy"`
	Enabled          bool   `json:"keepalive_enabled"`
	IdleDays         int    `json:"keepalive_idle_days"`
	SafetyMarginDays int    `json:"keepalive_safety_margin_days"`
	Model            string `json:"keepalive_model"`
	Prompt           string `json:"keepalive_prompt"`
	MaxTokens        int    `json:"keepalive_max_tokens"`
	DailyCap         int    `json:"keepalive_daily_cap"`
	QuietHours       string `json:"keepalive_quiet_hours"`
}

// updateSite writes one site's call policy and keepalive window.
//
// It is a separate endpoint from the site form on purpose (the store method has
// the same reason): that form does not show these columns, so a save from it must
// not be able to blank them.
func (h *KeepaliveHandler) updateSite(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var request siteKeepaliveRequest
	if err := decodeJSON(w, r, &request, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if request.IdleDays < 0 || request.IdleDays > 3650 {
		writeError(w, http.StatusBadRequest, "keepalive_idle_days must be between 0 and 3650")
		return
	}
	if request.SafetyMarginDays < 0 || request.SafetyMarginDays > 90 {
		writeError(w, http.StatusBadRequest, "keepalive_safety_margin_days must be between 0 and 90")
		return
	}
	if request.MaxTokens < 0 || request.MaxTokens > 32000 {
		writeError(w, http.StatusBadRequest, "keepalive_max_tokens must be between 0 and 32000")
		return
	}
	// The quiet-hours string is parsed by the scheduler; a typo would silently
	// disable the window, so it is rejected here where the operator can see it.
	if !domain.ValidQuietHours(request.QuietHours) {
		writeError(w, http.StatusBadRequest, "keepalive_quiet_hours must look like 22:00-07:00")
		return
	}
	site, err := h.db.Site.GetByID(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if site == nil {
		writeError(w, http.StatusNotFound, "site not found")
		return
	}
	site.CallPolicy = domain.NormalizeCallPolicy(request.CallPolicy)
	site.KeepaliveEnabled = request.Enabled
	site.KeepaliveIdleDays = request.IdleDays
	site.KeepaliveSafetyMarginDays = request.SafetyMarginDays
	site.KeepaliveModel = request.Model
	site.KeepalivePrompt = request.Prompt
	site.KeepaliveMaxTokens = request.MaxTokens
	site.KeepaliveDailyCap = request.DailyCap
	site.KeepaliveQuietHours = request.QuietHours
	if err := h.db.Site.UpdateCallPolicy(site); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, site)
}

// validQuietHours used to live here; the parser it wrapped is domain.ValidQuietHours
// so the console and the scheduler cannot disagree about what a valid window is.

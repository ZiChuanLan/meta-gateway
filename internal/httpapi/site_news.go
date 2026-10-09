package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/lan/meta-gateway/internal/sitenews"
)

// siteNewsRefreshBudget bounds one manual refresh. A round reads a handful of
// public /api/status documents in parallel, so this is generous; it exists so a
// site that accepts the connection and then stalls cannot pin the request open.
const siteNewsRefreshBudget = 60 * time.Second

// SiteNewsHandler exposes what upstream sites publish on their own notice
// boards: the one piece of context the gateway cannot infer from traffic.
type SiteNewsHandler struct {
	service   *sitenews.Service
	scheduler *sitenews.Scheduler
}

func NewSiteNewsHandler(service *sitenews.Service, scheduler *sitenews.Scheduler) *SiteNewsHandler {
	return &SiteNewsHandler{service: service, scheduler: scheduler}
}

func (h *SiteNewsHandler) Register(r chi.Router) {
	r.Get("/site-news", h.feed)
	r.Post("/site-news/refresh", h.refresh)
}

// feed returns the stored announcements. The console renders exactly this: the
// fetch happens in the background, so opening the dashboard never waits on 22
// upstream sites.
func (h *SiteNewsHandler) feed(w http.ResponseWriter, r *http.Request) {
	limit := 60
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil {
			limit = value
		}
	}
	feed, err := h.service.Feed(limit)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, feed)
}

// refresh reads every readable site now and answers with what the round did.
// The console's button shows that answer ("22 个站点，新增 3 条"), so the request
// waits for the round instead of queueing one and returning.
func (h *SiteNewsHandler) refresh(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), siteNewsRefreshBudget)
	defer cancel()
	result, err := h.scheduler.RefreshNow(ctx)
	if errors.Is(err, sitenews.ErrBusy) {
		writeError(w, http.StatusConflict, "a refresh is already running")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

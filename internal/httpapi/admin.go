package httpapi

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/go-chi/chi/v5"
	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/maintenance"
	"github.com/lan/meta-gateway/internal/routing"
	"github.com/lan/meta-gateway/internal/store"
)

// AdminHandler serves management endpoints under /admin.
type AdminHandler struct {
	db     *store.DB
	enc    *crypto.Encrypter
	router *routing.Selector
	sticky atomic.Pointer[routing.StickyStore]
	// httpClient is used for outbound site-detection probes; it is the shared
	// SSRF-policy client so admin input can never reach private/loopback space.
	httpClient *http.Client
	// modelsCache is invalidated on route/channel writes so /v1/models never
	// serves stale ids after admin changes.
	modelsCache *modelsCache
	// gcService drives scheduled + manual database maintenance (may be nil).
	gcService *maintenance.GCService
	// validateProxyURL validates a per-channel proxy URL against the outbound
	// SSRF policy (nil skips validation — admin handlers without a policy).
	validateProxyURL func(raw string) error
	// connectionMu serializes the site-reuse/create/rollback sequence. Without
	// it, two simultaneous creates for a new base URL could let one failed
	// rollback delete the other request's newly attached credentials/channels.
	connectionMu sync.Mutex
}

func NewAdminHandler(db *store.DB, enc *crypto.Encrypter, selector *routing.Selector, sticky *routing.StickyStore, outboundClient *http.Client, modelsCache *modelsCache) *AdminHandler {
	h := &AdminHandler{
		db:          db,
		enc:         enc,
		router:      selector,
		httpClient:  outboundClient,
		modelsCache: modelsCache,
	}
	h.sticky.Store(sticky)
	return h
}

// SetProxyValidator wires the outbound-policy proxy URL validator (nil
// disables per-channel proxy validation).
func (h *AdminHandler) SetProxyValidator(fn func(raw string) error) {
	h.validateProxyURL = fn
}

// SetGCService wires the database-maintenance service (may be nil).
func (h *AdminHandler) SetGCService(s *maintenance.GCService) {
	h.gcService = s
}

// SetSticky hot-swaps the sticky-session store backing the admin read-only
// stats endpoint (nil = disabled). Used by the runtime-settings hot reload.
func (h *AdminHandler) SetSticky(store *routing.StickyStore) {
	h.sticky.Store(store)
}

func (h *AdminHandler) Register(r chi.Router) {
	// Sites
	r.Get("/sites", h.listSites)
	r.Post("/sites", h.createSite)
	r.Get("/sites/{id}", h.getSite)
	r.Put("/sites/{id}", h.updateSite)
	r.Delete("/sites/{id}", h.deleteSite)
	// Site-type detection (AAH chain) for the connection editor.
	r.Get("/site-type", h.detectSiteType)
	// One-shot connection creation: site + credential + channel, with site
	// reuse by normalized URL and rollback of partially created rows.
	r.Post("/connections", h.createConnection)

	// Credentials
	r.Get("/sites/{siteId}/credentials", h.listCredentials)
	r.Post("/sites/{siteId}/credentials", h.createCredential)
	r.Put("/credentials/{id}", h.updateCredential)
	r.Delete("/credentials/{id}", h.deleteCredential)
	// POST so the admin audit trail records plaintext secret reveals.
	r.Post("/sites/{siteId}/credentials/{id}/reveal", h.revealCredentialSecret)

	// Channels
	r.Get("/channels", h.listChannels)
	r.Get("/search", h.globalSearch)
	r.Get("/channels/overview", h.listChannelOverviews)
	r.Post("/channels", h.createChannel)
	r.Post("/channels/{id}/duplicate", h.duplicateChannel)
	r.Post("/reset", h.factoryReset)
	r.Get("/channels/{id}", h.getChannel)
	r.Put("/channels/{id}", h.updateChannel)
	r.Delete("/channels/{id}", h.deleteChannel)
	r.Post("/channels/{id}/ping", h.pingChannel)

	// Routes
	r.Get("/routes", h.listRoutes)
	r.Get("/routes/overview", h.listRouteOverviews)
	r.Get("/routes/explain", h.explainRoute)
	r.Post("/routes", h.createRoute)
	r.Get("/routes/{id}", h.getRoute)
	r.Put("/routes/{id}", h.updateRoute)
	r.Delete("/routes/{id}", h.deleteRoute)

	// Route members
	r.Get("/routes/{routeId}/members", h.listRouteMembers)
	r.Post("/routes/{routeId}/members", h.createRouteMember)
	r.Put("/route-members/{id}", h.updateRouteMember)
	r.Post("/route-members/{id}/clear-health", h.clearRouteMemberHealth)
	r.Delete("/route-members/{id}", h.deleteRouteMember)

	// Downstream keys
	r.Get("/downstream-keys", h.listDownstreamKeys)
	r.Post("/downstream-keys", h.createDownstreamKey)
	r.Put("/downstream-keys/{id}", h.updateDownstreamKey)
	r.Delete("/downstream-keys/{id}", h.deleteDownstreamKey)
	// POST verbs so the admin audit trail records plaintext reveals and token
	// rotations (GET requests are not audited).
	r.Post("/downstream-keys/{id}/reveal", h.revealDownstreamKeyToken)
	r.Post("/downstream-keys/{id}/rotate", h.rotateDownstreamKeyToken)

	// Usage / simple billing
	r.Get("/usage/summary", h.usageSummary)
	r.Get("/usage", h.listUsage)
	// Billing ratios (model markup; 1.0 = no markup)
	r.Get("/ratios", h.listModelRatios)
	r.Put("/ratios/{model}", h.setModelRatio)
	// Tenant groups (multi-tenant quotas / rate limits)
	r.Get("/groups", h.listGroups)
	r.Put("/groups/{name}", h.upsertGroup)
	r.Delete("/groups/{name}", h.deleteGroup)
	// Bulk channel operations by tag
	r.Patch("/channels/tag/{tag}", h.patchChannelsByTag)

	// Sticky session routing (available when enabled at boot)
	r.Get("/sticky", h.stickyStats)

	// Proxy logs
	r.Get("/proxy-logs", h.listProxyLogs)
	r.Get("/proxy-logs/latency-histogram", h.latencyHistogram)
	// Routing decision audit trail.
	r.Get("/decision-snapshot", h.decisionSnapshot)
	// Model-not-found blacklist.
	r.Get("/model-blocks", h.listModelBlocks)
	r.Delete("/model-blocks", h.deleteModelBlock)
	// Redemption codes (quota top-up vouchers for downstream keys).
	r.Post("/redemption-codes", h.createRedemptionCodes)
	r.Get("/redemption-codes", h.listRedemptionCodes)
	r.Delete("/redemption-codes/{id}", h.deleteRedemptionCode)
	// Model metadata library (per-model capability annotations).
	r.Get("/model-metadata", h.listModelMetadata)
	r.Put("/model-metadata/{name}", h.upsertModelMetadata)
	r.Delete("/model-metadata/{name}", h.deleteModelMetadata)
	// Channel health history + availability summaries.
	r.Get("/health-history", h.listHealthHistory)
	r.Get("/health-history/summary", h.healthHistorySummary)
	// Alert rules (metric → webhook).
	r.Get("/alert-rules", h.listAlertRules)
	r.Post("/alert-rules", h.createAlertRule)
	r.Put("/alert-rules/{id}", h.updateAlertRule)
	r.Delete("/alert-rules/{id}", h.deleteAlertRule)
	// Sensitive prompt guard rules (regex → mask/reject/exclude).
	r.Get("/prompt-guards", h.listPromptGuards)
	r.Post("/prompt-guards", h.createPromptGuard)
	r.Put("/prompt-guards/{id}", h.updatePromptGuard)
	r.Delete("/prompt-guards/{id}", h.deletePromptGuard)
	// Database maintenance (orphan GC + VACUUM).
	r.Post("/db/gc", h.runDBGC)
	r.Get("/db/gc", h.lastDBGC)
	// Error passthrough rules (status/keyword → passthrough/rewrite/ignore).
	r.Get("/error-rules", h.listErrorRules)
	r.Post("/error-rules", h.createErrorRule)
	r.Put("/error-rules/{id}", h.updateErrorRule)
	r.Delete("/error-rules/{id}", h.deleteErrorRule)
}

func writeStoreError(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "resource not found")
		return
	}
	if strings.Contains(strings.ToLower(err.Error()), "unique constraint failed") {
		writeError(w, http.StatusConflict, "resource already exists")
		return
	}
	writeError(w, http.StatusInternalServerError, "database operation failed")
}

// latencyHistogram returns the latency distribution over the newest proxy
// logs (AAH-style 10-bucket histogram; slow = >= 5s).
func (h *AdminHandler) latencyHistogram(w http.ResponseWriter, r *http.Request) {
	sample := 1000
	if raw := r.URL.Query().Get("sample"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 10000 {
			sample = parsed
		}
	}
	hist, err := h.db.ProxyLog.LatencyHistogram(sample)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, hist)
}

func (h *AdminHandler) listProxyLogs(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	limit := 100
	if raw := query.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 500 {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and 500")
			return
		}
		limit = parsed
	}
	siteID, ok := optionalPositiveQueryID(w, query.Get("site_id"), "site_id")
	if !ok {
		return
	}
	channelID, ok := optionalPositiveQueryID(w, query.Get("channel_id"), "channel_id")
	if !ok {
		return
	}
	beforeID, ok := optionalPositiveQueryID(w, query.Get("before_id"), "before_id")
	if !ok {
		return
	}
	model := strings.TrimSpace(query.Get("model"))
	upstreamRequestID := strings.TrimSpace(query.Get("upstream_request_id"))
	var status *int
	failedOnly := false
	if raw := strings.TrimSpace(query.Get("status")); raw != "" {
		if raw == "failed" {
			failedOnly = true
		} else {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed < 100 || parsed > 599 {
				writeError(w, http.StatusBadRequest, "invalid status")
				return
			}
			status = &parsed
		}
	}
	logs, err := h.db.ProxyLog.ListFilter(store.ProxyLogFilter{
		SiteID:            siteID,
		ChannelID:         channelID,
		Model:             model,
		Status:            status,
		FailedOnly:        failedOnly,
		UpstreamRequestID: upstreamRequestID,
		BeforeID:          beforeID,
		Limit:             limit,
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, logs)
}

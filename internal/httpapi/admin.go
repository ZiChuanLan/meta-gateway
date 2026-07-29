package httpapi

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/lan/meta-gateway/internal/auth"
	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/routing"
	"github.com/lan/meta-gateway/internal/store"
	usagepkg "github.com/lan/meta-gateway/internal/usage"
)

// AdminHandler serves management endpoints under /admin.
type AdminHandler struct {
	db     *store.DB
	enc    *crypto.Encrypter
	router *routing.Selector
}

func NewAdminHandler(db *store.DB, enc *crypto.Encrypter, selector *routing.Selector) *AdminHandler {
	return &AdminHandler{db: db, enc: enc, router: selector}
}

func (h *AdminHandler) Register(r chi.Router) {
	// Sites
	r.Get("/sites", h.listSites)
	r.Post("/sites", h.createSite)
	r.Get("/sites/{id}", h.getSite)
	r.Put("/sites/{id}", h.updateSite)
	r.Delete("/sites/{id}", h.deleteSite)

	// Credentials
	r.Get("/sites/{siteId}/credentials", h.listCredentials)
	r.Post("/sites/{siteId}/credentials", h.createCredential)
	r.Put("/credentials/{id}", h.updateCredential)
	r.Delete("/credentials/{id}", h.deleteCredential)

	// Channels
	r.Get("/channels", h.listChannels)
	r.Get("/channels/overview", h.listChannelOverviews)
	r.Post("/channels", h.createChannel)
	r.Get("/channels/{id}", h.getChannel)
	r.Put("/channels/{id}", h.updateChannel)
	r.Delete("/channels/{id}", h.deleteChannel)

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

	// Usage / simple billing
	r.Get("/usage/summary", h.usageSummary)
	r.Get("/usage", h.listUsage)

	// Proxy logs
	r.Get("/proxy-logs", h.listProxyLogs)
}

func (h *AdminHandler) explainRoute(w http.ResponseWriter, r *http.Request) {
	model := r.URL.Query().Get("model")
	if model == "" {
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}
	explanation, err := h.router.Explain(r.Context(), model)
	if err != nil {
		if errors.Is(err, routing.ErrRouteNotFound) {
			writeError(w, http.StatusNotFound, "route not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to explain route")
		return
	}
	writeJSON(w, http.StatusOK, explanation)
}

// ---------------------------------------------------------------------------
// Sites
// ---------------------------------------------------------------------------

func (h *AdminHandler) listSites(w http.ResponseWriter, r *http.Request) {
	sites, err := h.db.Site.List()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sites)
}

func (h *AdminHandler) createSite(w http.ResponseWriter, r *http.Request) {
	var site domain.Site
	if err := decodeJSON(w, r, &site, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if site.Status == "" {
		site.Status = domain.StatusEnabled
	}
	id, err := h.db.Site.Create(&site)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	created, err := h.db.Site.GetByID(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if created == nil {
		writeJSON(w, http.StatusCreated, map[string]any{"id": id})
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *AdminHandler) getSite(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
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
	writeJSON(w, http.StatusOK, site)
}

func (h *AdminHandler) updateSite(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var site domain.Site
	if err := decodeJSON(w, r, &site, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	site.ID = id
	if err := h.db.Site.Update(&site); err != nil {
		writeStoreError(w, err)
		return
	}
	updated, _ := h.db.Site.GetByID(id)
	writeJSON(w, http.StatusOK, updated)
}

func (h *AdminHandler) deleteSite(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.db.Site.Delete(id); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ---------------------------------------------------------------------------
// Credentials
// ---------------------------------------------------------------------------

func (h *AdminHandler) listCredentials(w http.ResponseWriter, r *http.Request) {
	siteID, err := strconv.ParseInt(chi.URLParam(r, "siteId"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid siteId")
		return
	}
	creds, err := h.db.Credential.ListBySite(siteID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	// Never expose secret_enc in JSON responses.
	type safeCred struct {
		ID             int64  `json:"id"`
		SiteID         int64  `json:"site_id"`
		Kind           string `json:"kind"`
		HasSecret      bool   `json:"has_secret"`
		MetaJSON       string `json:"meta_json,omitempty"`
		Status         string `json:"status"`
		CheckinEnabled bool   `json:"checkin_enabled"`
	}
	result := make([]safeCred, 0, len(creds))
	for _, c := range creds {
		result = append(result, safeCred{
			ID:             c.ID,
			SiteID:         c.SiteID,
			Kind:           c.Kind,
			HasSecret:      len(c.SecretEnc) > 0,
			MetaJSON:       c.MetaJSON,
			Status:         c.Status,
			CheckinEnabled: c.CheckinEnabled,
		})
	}
	writeJSON(w, http.StatusOK, result)
}

type createCredentialRequest struct {
	Kind     string `json:"kind"`
	Secret   string `json:"secret"`
	MetaJSON string `json:"meta_json,omitempty"`
	Status   string `json:"status,omitempty"`
}

func (h *AdminHandler) createCredential(w http.ResponseWriter, r *http.Request) {
	siteID, err := strconv.ParseInt(chi.URLParam(r, "siteId"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid siteId")
		return
	}
	var req createCredentialRequest
	if err := decodeJSON(w, r, &req, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Secret == "" {
		writeError(w, http.StatusBadRequest, "secret is required")
		return
	}
	encSecret, err := h.enc.Encrypt([]byte(req.Secret))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "encryption failed")
		return
	}
	if req.Status == "" {
		req.Status = domain.StatusEnabled
	}
	cred := &domain.Credential{
		SiteID:    siteID,
		Kind:      req.Kind,
		SecretEnc: []byte(encSecret),
		MetaJSON:  req.MetaJSON,
		Status:    req.Status,
	}
	id, err := h.db.Credential.Create(cred)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	created, _ := h.db.Credential.GetByID(id)
	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"id":              created.ID,
		"site_id":         created.SiteID,
		"kind":            created.Kind,
		"has_secret":      true,
		"meta_json":       created.MetaJSON,
		"status":          created.Status,
		"checkin_enabled": created.CheckinEnabled,
		"created_at":      created.CreatedAt,
	})
}

type updateCredentialRequest struct {
	Kind     string `json:"kind,omitempty"`
	Secret   string `json:"secret,omitempty"` // empty keeps existing secret
	MetaJSON string `json:"meta_json,omitempty"`
	Status   string `json:"status,omitempty"`
}

func (h *AdminHandler) updateCredential(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	existing, err := h.db.Credential.GetByID(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if existing == nil {
		writeError(w, http.StatusNotFound, "credential not found")
		return
	}
	var req updateCredentialRequest
	if err := decodeJSON(w, r, &req, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Kind != "" {
		existing.Kind = req.Kind
	}
	if req.Status != "" {
		existing.Status = req.Status
	}
	if req.MetaJSON != "" {
		existing.MetaJSON = req.MetaJSON
	}
	if strings.TrimSpace(req.Secret) != "" {
		encSecret, err := h.enc.Encrypt([]byte(req.Secret))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "encryption failed")
			return
		}
		existing.SecretEnc = []byte(encSecret)
		// Replacing secret invalidates any import fingerprint identity.
		existing.ImportFingerprint = ""
	}
	if err := h.db.Credential.Update(existing); err != nil {
		writeStoreError(w, err)
		return
	}
	updated, _ := h.db.Credential.GetByID(id)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":              updated.ID,
		"site_id":         updated.SiteID,
		"kind":            updated.Kind,
		"has_secret":      len(updated.SecretEnc) > 0,
		"meta_json":       updated.MetaJSON,
		"status":          updated.Status,
		"checkin_enabled": updated.CheckinEnabled,
		"created_at":      updated.CreatedAt,
		"updated_at":      updated.UpdatedAt,
	})
}

func (h *AdminHandler) deleteCredential(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.db.Credential.Delete(id); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ---------------------------------------------------------------------------
// Channels
// ---------------------------------------------------------------------------

func (h *AdminHandler) listChannels(w http.ResponseWriter, r *http.Request) {
	channels, err := h.db.Channel.List()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, channels)
}

func (h *AdminHandler) listChannelOverviews(w http.ResponseWriter, r *http.Request) {
	channels, err := h.db.Channel.ListOverviews(time.Now())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, channels)
}

func (h *AdminHandler) createChannel(w http.ResponseWriter, r *http.Request) {
	var ch domain.Channel
	if err := decodeJSON(w, r, &ch, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if ch.Status == "" {
		ch.Status = domain.StatusEnabled
	}
	if err := h.validateChannel(&ch); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	id, err := h.db.Channel.Create(&ch)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	created, _ := h.db.Channel.GetByID(id)
	writeJSON(w, http.StatusCreated, created)
}

func (h *AdminHandler) getChannel(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	ch, err := h.db.Channel.GetByID(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if ch == nil {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}
	writeJSON(w, http.StatusOK, ch)
}

func (h *AdminHandler) updateChannel(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	existing, err := h.db.Channel.GetByID(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if existing == nil {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}
	var patch domain.Channel
	if err := decodeJSON(w, r, &patch, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	// Merge onto the stored row so clients that omit site_id (or send 0) cannot
	// break credential ownership validation when rebinding API keys.
	ch := *existing
	if name := strings.TrimSpace(patch.Name); name != "" {
		ch.Name = name
	}
	// BaseURL may intentionally be cleared to inherit site URL.
	if patch.BaseURL != existing.BaseURL {
		ch.BaseURL = strings.TrimSpace(patch.BaseURL)
	}
	if group := strings.TrimSpace(patch.GroupName); group != "" {
		ch.GroupName = group
	}
	ch.Priority = patch.Priority
	ch.Weight = patch.Weight
	if patch.Status == domain.StatusEnabled || patch.Status == domain.StatusDisabled {
		ch.Status = patch.Status
	}
	if hint := strings.TrimSpace(patch.TypeHint); hint != "" {
		ch.TypeHint = hint
	}
	if patch.ModelsCSV != "" {
		ch.ModelsCSV = patch.ModelsCSV
	}
	// Only accept a new site_id when it is a positive id; never wipe ownership.
	if patch.SiteID != nil && *patch.SiteID > 0 {
		ch.SiteID = patch.SiteID
	}
	if patch.CredentialID != nil && *patch.CredentialID > 0 {
		ch.CredentialID = patch.CredentialID
	}
	ch.ID = id
	if err := h.validateChannel(&ch); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.db.Channel.Update(&ch); err != nil {
		writeStoreError(w, err)
		return
	}
	updated, _ := h.db.Channel.GetByID(id)
	writeJSON(w, http.StatusOK, updated)
}

func (h *AdminHandler) deleteChannel(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.db.Channel.Delete(id); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *AdminHandler) validateChannel(ch *domain.Channel) error {
	ch.Name = strings.TrimSpace(ch.Name)
	ch.BaseURL = strings.TrimSpace(ch.BaseURL)
	ch.GroupName = strings.TrimSpace(ch.GroupName)
	ch.TypeHint = strings.TrimSpace(ch.TypeHint)
	if ch.Name == "" {
		return errors.New("name is required")
	}
	if ch.Weight < 0 {
		return errors.New("weight must be non-negative")
	}
	if ch.Status != domain.StatusEnabled && ch.Status != domain.StatusDisabled {
		return errors.New("invalid channel status")
	}
	if ch.SiteID == nil || *ch.SiteID <= 0 {
		return errors.New("site_id is required")
	}
	site, err := h.db.Site.GetByID(*ch.SiteID)
	if err != nil || site == nil {
		return errors.New("site not found")
	}
	if ch.CredentialID == nil || *ch.CredentialID <= 0 {
		return errors.New("credential_id is required")
	}
	credential, err := h.db.Credential.GetByID(*ch.CredentialID)
	if err != nil || credential == nil || credential.SiteID != *ch.SiteID {
		return errors.New("credential does not belong to site")
	}
	return nil
}

// ---------------------------------------------------------------------------
// Routes
// ---------------------------------------------------------------------------

func (h *AdminHandler) listRoutes(w http.ResponseWriter, r *http.Request) {
	routes, err := h.db.Route.List()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, routes)
}

func (h *AdminHandler) listRouteOverviews(w http.ResponseWriter, r *http.Request) {
	routes, err := h.db.RouteMember.ListRouteOverviews()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, routes)
}

func (h *AdminHandler) createRoute(w http.ResponseWriter, r *http.Request) {
	var rt domain.Route
	if err := decodeJSON(w, r, &rt, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if rt.ModelPattern == "" {
		writeError(w, http.StatusBadRequest, "model_pattern is required")
		return
	}
	id, err := h.db.Route.Create(&rt)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	created, _ := h.db.Route.GetByID(id)
	writeJSON(w, http.StatusCreated, created)
}

func (h *AdminHandler) getRoute(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	rt, err := h.db.Route.GetByID(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if rt == nil {
		writeError(w, http.StatusNotFound, "route not found")
		return
	}
	writeJSON(w, http.StatusOK, rt)
}

func (h *AdminHandler) updateRoute(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var rt domain.Route
	if err := decodeJSON(w, r, &rt, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	rt.ID = id
	if rt.ModelPattern == "" {
		writeError(w, http.StatusBadRequest, "model_pattern is required")
		return
	}
	if err := h.db.Route.Update(&rt); err != nil {
		writeStoreError(w, err)
		return
	}
	updated, _ := h.db.Route.GetByID(id)
	writeJSON(w, http.StatusOK, updated)
}

func (h *AdminHandler) deleteRoute(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.db.Route.Delete(id); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ---------------------------------------------------------------------------
// Route Members
// ---------------------------------------------------------------------------

func (h *AdminHandler) listRouteMembers(w http.ResponseWriter, r *http.Request) {
	routeID, err := strconv.ParseInt(chi.URLParam(r, "routeId"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid routeId")
		return
	}
	members, err := h.db.RouteMember.ListByRoute(routeID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, members)
}

func (h *AdminHandler) createRouteMember(w http.ResponseWriter, r *http.Request) {
	routeID, err := strconv.ParseInt(chi.URLParam(r, "routeId"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid routeId")
		return
	}
	var rm domain.RouteMember
	if err := decodeJSON(w, r, &rm, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	rm.RouteID = routeID
	if rm.Weight < 0 {
		writeError(w, http.StatusBadRequest, "weight must be non-negative")
		return
	}
	id, err := h.db.RouteMember.Create(&rm)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	created, _ := h.db.RouteMember.GetByID(id)
	writeJSON(w, http.StatusCreated, created)
}

func (h *AdminHandler) updateRouteMember(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var rm domain.RouteMember
	if err := decodeJSON(w, r, &rm, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	rm.ID = id
	if rm.Weight < 0 {
		writeError(w, http.StatusBadRequest, "weight must be non-negative")
		return
	}
	if err := h.db.RouteMember.Update(&rm); err != nil {
		writeStoreError(w, err)
		return
	}
	updated, _ := h.db.RouteMember.GetByID(id)
	writeJSON(w, http.StatusOK, updated)
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

func (h *AdminHandler) clearRouteMemberHealth(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.db.RouteMember.ClearHealth(id); err != nil {
		writeStoreError(w, err)
		return
	}
	updated, err := h.db.RouteMember.GetByID(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *AdminHandler) deleteRouteMember(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.db.RouteMember.Delete(id); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ---------------------------------------------------------------------------
// Downstream Keys
// ---------------------------------------------------------------------------

func (h *AdminHandler) listDownstreamKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := h.db.DownstreamKey.List()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	// Never expose token_hash.
	type safeKey struct {
		ID                   int64   `json:"id"`
		Name                 string  `json:"name"`
		Enabled              bool    `json:"enabled"`
		Scopes               string  `json:"scopes,omitempty"`
		QuotaTotalTokens     int64   `json:"quota_total_tokens"`
		QuotaUsedTokens      int64   `json:"quota_used_tokens"`
		PricePromptPer1k     float64 `json:"price_prompt_per_1k"`
		PriceCompletionPer1k float64 `json:"price_completion_per_1k"`
		EstimatedCost        float64 `json:"estimated_cost"`
		CreatedAt            string  `json:"created_at"`
	}
	result := make([]safeKey, 0, len(keys))
	for _, k := range keys {
		// Cost estimate uses used tokens split is unknown at key level; charge all used as prompt-equivalent mixed average.
		estimated := 0.0
		if k.QuotaUsedTokens > 0 {
			// Prefer average of prompt/completion unit prices when both set; else whichever is set.
			unit := 0.0
			if k.PricePromptPer1k > 0 && k.PriceCompletionPer1k > 0 {
				unit = (k.PricePromptPer1k + k.PriceCompletionPer1k) / 2
			} else if k.PricePromptPer1k > 0 {
				unit = k.PricePromptPer1k
			} else {
				unit = k.PriceCompletionPer1k
			}
			if unit > 0 {
				estimated = (float64(k.QuotaUsedTokens) / 1000.0) * unit
			}
		}
		result = append(result, safeKey{
			ID:                   k.ID,
			Name:                 k.Name,
			Enabled:              k.Enabled,
			Scopes:               k.Scopes,
			QuotaTotalTokens:     k.QuotaTotalTokens,
			QuotaUsedTokens:      k.QuotaUsedTokens,
			PricePromptPer1k:     k.PricePromptPer1k,
			PriceCompletionPer1k: k.PriceCompletionPer1k,
			EstimatedCost:        estimated,
			CreatedAt:            k.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		})
	}
	writeJSON(w, http.StatusOK, result)
}

type createKeyResponse struct {
	ID                   int64   `json:"id"`
	Name                 string  `json:"name"`
	Token                string  `json:"token"`
	Enabled              bool    `json:"enabled"`
	Scopes               string  `json:"scopes,omitempty"`
	QuotaTotalTokens     int64   `json:"quota_total_tokens"`
	QuotaUsedTokens      int64   `json:"quota_used_tokens"`
	PricePromptPer1k     float64 `json:"price_prompt_per_1k"`
	PriceCompletionPer1k float64 `json:"price_completion_per_1k"`
	CreatedAt            string  `json:"created_at"`
}

func (h *AdminHandler) createDownstreamKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
		// Token is optional. When empty, the server generates an mg-… secret.
		// When set, the provided secret is stored as a hash only (raw never re-readable).
		Token                string  `json:"token,omitempty"`
		Scopes               string  `json:"scopes,omitempty"`
		QuotaTotalTokens     int64   `json:"quota_total_tokens"`
		PricePromptPer1k     float64 `json:"price_prompt_per_1k"`
		PriceCompletionPer1k float64 `json:"price_completion_per_1k"`
	}
	if err := decodeJSON(w, r, &req, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	normalizedScopes, scopeErr := auth.NormalizeScopes(req.Scopes)
	if scopeErr != nil {
		writeError(w, http.StatusBadRequest, scopeErr.Error())
		return
	}
	req.Scopes = auth.FormatScopes(normalizedScopes)

	raw := auth.NormalizeDownstreamToken(req.Token)
	var hash string
	if raw == "" {
		var genErr error
		hash, raw, genErr = auth.NewToken()
		if genErr != nil {
			writeError(w, http.StatusInternalServerError, "token generation failed")
			return
		}
	} else {
		if err := validateCustomDownstreamToken(raw); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		hash = auth.HashToken(raw)
		existing, lookupErr := h.db.DownstreamKey.GetByHash(hash)
		if lookupErr != nil {
			writeStoreError(w, lookupErr)
			return
		}
		if existing != nil {
			writeError(w, http.StatusConflict, "token already exists")
			return
		}
	}

	if req.QuotaTotalTokens < 0 {
		writeError(w, http.StatusBadRequest, "quota_total_tokens must be >= 0")
		return
	}
	if req.PricePromptPer1k < 0 || req.PriceCompletionPer1k < 0 {
		writeError(w, http.StatusBadRequest, "prices must be >= 0")
		return
	}
	key := &domain.DownstreamKey{
		TokenHash:            hash,
		Name:                 req.Name,
		Enabled:              true,
		Scopes:               req.Scopes,
		QuotaTotalTokens:     req.QuotaTotalTokens,
		PricePromptPer1k:     req.PricePromptPer1k,
		PriceCompletionPer1k: req.PriceCompletionPer1k,
	}
	id, err := h.db.DownstreamKey.Create(key)
	if err != nil {
		writeStoreError(w, err)
		return
	}

	createdAt := ""
	if created, err := h.db.DownstreamKey.GetByID(id); err == nil && created != nil {
		createdAt = created.CreatedAt.Format("2006-01-02T15:04:05Z07:00")
	}

	writeJSON(w, http.StatusCreated, createKeyResponse{
		ID:                   id,
		Name:                 req.Name,
		Token:                raw,
		Enabled:              true,
		Scopes:               req.Scopes,
		QuotaTotalTokens:     req.QuotaTotalTokens,
		QuotaUsedTokens:      0,
		PricePromptPer1k:     req.PricePromptPer1k,
		PriceCompletionPer1k: req.PriceCompletionPer1k,
		CreatedAt:            createdAt,
	})
}

func (h *AdminHandler) updateDownstreamKey(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	existing, err := h.db.DownstreamKey.GetByID(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if existing == nil {
		writeError(w, http.StatusNotFound, "key not found")
		return
	}
	var req struct {
		Name                 *string  `json:"name"`
		Enabled              *bool    `json:"enabled"`
		Scopes               *string  `json:"scopes"`
		QuotaTotalTokens     *int64   `json:"quota_total_tokens"`
		PricePromptPer1k     *float64 `json:"price_prompt_per_1k"`
		PriceCompletionPer1k *float64 `json:"price_completion_per_1k"`
		ResetUsed            bool     `json:"reset_used"`
	}
	if err := decodeJSON(w, r, &req, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			writeError(w, http.StatusBadRequest, "name is required")
			return
		}
		existing.Name = name
	}
	if req.Enabled != nil {
		existing.Enabled = *req.Enabled
	}
	if req.Scopes != nil {
		normalizedScopes, scopeErr := auth.NormalizeScopes(*req.Scopes)
		if scopeErr != nil {
			writeError(w, http.StatusBadRequest, scopeErr.Error())
			return
		}
		existing.Scopes = auth.FormatScopes(normalizedScopes)
	}
	if req.QuotaTotalTokens != nil {
		if *req.QuotaTotalTokens < 0 {
			writeError(w, http.StatusBadRequest, "quota_total_tokens must be >= 0")
			return
		}
		existing.QuotaTotalTokens = *req.QuotaTotalTokens
	}
	if req.PricePromptPer1k != nil {
		if *req.PricePromptPer1k < 0 {
			writeError(w, http.StatusBadRequest, "price_prompt_per_1k must be >= 0")
			return
		}
		existing.PricePromptPer1k = *req.PricePromptPer1k
	}
	if req.PriceCompletionPer1k != nil {
		if *req.PriceCompletionPer1k < 0 {
			writeError(w, http.StatusBadRequest, "price_completion_per_1k must be >= 0")
			return
		}
		existing.PriceCompletionPer1k = *req.PriceCompletionPer1k
	}
	if err := h.db.DownstreamKey.Update(existing); err != nil {
		writeStoreError(w, err)
		return
	}
	if req.ResetUsed {
		if _, err := h.db.Exec(`UPDATE downstream_keys SET quota_used_tokens = 0 WHERE id = ?`, id); err != nil {
			writeStoreError(w, err)
			return
		}
		existing.QuotaUsedTokens = 0
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":                      existing.ID,
		"name":                    existing.Name,
		"enabled":                 existing.Enabled,
		"scopes":                  existing.Scopes,
		"quota_total_tokens":      existing.QuotaTotalTokens,
		"quota_used_tokens":       existing.QuotaUsedTokens,
		"price_prompt_per_1k":     existing.PricePromptPer1k,
		"price_completion_per_1k": existing.PriceCompletionPer1k,
		"created_at":              existing.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	})
}

func (h *AdminHandler) usageSummary(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	keyID, ok := optionalPositiveQueryID(w, query.Get("downstream_key_id"), "downstream_key_id")
	if !ok {
		return
	}
	summary, err := h.db.Usage.Summary(keyID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	// Attach estimated cost from exact prompt/completion sums using key prices when filtering one key.
	if keyID != nil {
		key, err := h.db.DownstreamKey.GetByID(*keyID)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		if key != nil {
			summary.EstimatedCost = usagepkg.EstimateCost(
				int(summary.PromptTokens),
				int(summary.CompletionTokens),
				key.PricePromptPer1k,
				key.PriceCompletionPer1k,
			)
		}
	}
	writeJSON(w, http.StatusOK, summary)
}

func (h *AdminHandler) listUsage(w http.ResponseWriter, r *http.Request) {
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
	keyID, ok := optionalPositiveQueryID(w, query.Get("downstream_key_id"), "downstream_key_id")
	if !ok {
		return
	}
	channelID, ok := optionalPositiveQueryID(w, query.Get("channel_id"), "channel_id")
	if !ok {
		return
	}
	model := strings.TrimSpace(query.Get("model"))
	rows, err := h.db.Usage.List(store.UsageFilter{
		DownstreamKeyID: keyID,
		ChannelID:       channelID,
		Model:           model,
		Limit:           limit,
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (h *AdminHandler) deleteDownstreamKey(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.db.DownstreamKey.Delete(id); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func validateCustomDownstreamToken(token string) error {
	if strings.ContainsAny(token, " \t\r\n") {
		return errors.New("token must not contain whitespace")
	}
	// Allow operator-chosen secrets (OpenAI-style sk-…, random strings, etc.).
	if len(token) < 16 {
		return errors.New("token must be at least 16 characters")
	}
	if len(token) > 256 {
		return errors.New("token must be at most 256 characters")
	}
	for _, r := range token {
		if r < 32 || r == 127 {
			return errors.New("token contains invalid control characters")
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Proxy Logs
// ---------------------------------------------------------------------------

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
		SiteID:     siteID,
		ChannelID:  channelID,
		Model:      model,
		Status:     status,
		FailedOnly: failedOnly,
		BeforeID:   beforeID,
		Limit:      limit,
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, logs)
}

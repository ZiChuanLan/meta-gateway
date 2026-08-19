package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/lan/meta-gateway/internal/checkin"
	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

const externalCheckinPlatform = "external-checkin"

type CheckinHandler struct {
	db      *store.DB
	service *checkin.Service
	enc     *crypto.Encrypter
}

func NewCheckinHandler(db *store.DB, service *checkin.Service, enc *crypto.Encrypter) *CheckinHandler {
	return &CheckinHandler{db: db, service: service, enc: enc}
}

func (h *CheckinHandler) Register(r chi.Router) {
	r.Post("/checkin/credentials/{id}/run", h.runCredential)
	r.Post("/checkin/run", h.runAll)
	r.Get("/checkin/logs", h.listLogs)
	r.Put("/credentials/{id}/checkin", h.setCredentialEnabled)
	// Generic external check-in sites (non-New-API, cookie-authenticated).
	r.Get("/checkin/external", h.listExternal)
	r.Post("/checkin/external", h.createExternal)
	r.Put("/checkin/external/{id}", h.updateExternal)
	r.Delete("/checkin/external/{id}", h.deleteExternal)
}

// externalCheckinMeta is the non-sensitive endpoint config stored in
// credential meta_json (the cookie itself lives in cookie_enc).
type externalCheckinMeta struct {
	CheckinPath   string            `json:"checkin_path,omitempty"`
	CheckinMethod string            `json:"checkin_method,omitempty"`
	Headers       map[string]string `json:"headers,omitempty"`
}

// externalCheckinView is the admin-safe list/update shape (never the cookie).
type externalCheckinView struct {
	SiteID         int64             `json:"site_id"`
	CredentialID   int64             `json:"credential_id"`
	Name           string            `json:"name"`
	BaseURL        string            `json:"base_url"`
	CheckinPath    string            `json:"checkin_path,omitempty"`
	CheckinMethod  string            `json:"checkin_method,omitempty"`
	Headers        map[string]string `json:"headers,omitempty"`
	CheckinEnabled bool              `json:"checkin_enabled"`
	HasCookie      bool              `json:"has_cookie"`
}

// normalizeExternalHeaders validates and canonicalizes user-supplied request
// headers: keys must be valid token/id/。- names, values small and free of CR/LF.
func normalizeExternalHeaders(raw map[string]string) (map[string]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(raw))
	for key, value := range raw {
		key = strings.TrimSpace(key)
		if key == "" || len(key) > 128 {
			return nil, errors.New("invalid header name")
		}
		for _, r := range key {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
				continue
			}
			return nil, errors.New("invalid header name")
		}
		if strings.ContainsAny(value, "\r\n") || len(value) > 512 {
			return nil, errors.New("invalid header value")
		}
		out[key] = value
	}
	return out, nil
}

func (h *CheckinHandler) listExternal(w http.ResponseWriter, r *http.Request) {
	sites, err := h.db.Site.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list external check-in sites")
		return
	}
	out := make([]externalCheckinView, 0)
	for _, site := range sites {
		if site.Platform != externalCheckinPlatform {
			continue
		}
		view := externalCheckinView{
			SiteID:  site.ID,
			Name:    site.Name,
			BaseURL: site.BaseURL,
		}
		credentials, err := h.db.Credential.ListBySite(site.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list check-in credentials")
			return
		}
		for _, c := range credentials {
			kind := strings.ToLower(strings.TrimSpace(c.Kind))
			if kind != "session" && kind != "access_token" {
				continue
			}
			view.CredentialID = c.ID
			view.CheckinEnabled = c.CheckinEnabled
			view.HasCookie = len(c.CookieEnc) > 0
			var meta externalCheckinMeta
			if json.Unmarshal([]byte(c.MetaJSON), &meta) == nil {
				view.CheckinPath = meta.CheckinPath
				view.CheckinMethod = meta.CheckinMethod
				view.Headers = meta.Headers
			}
			break
		}
		out = append(out, view)
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *CheckinHandler) createExternal(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name          string            `json:"name"`
		BaseURL       string            `json:"base_url"`
		CheckinPath   string            `json:"checkin_path"`
		CheckinMethod string            `json:"checkin_method"`
		Headers       map[string]string `json:"headers"`
		Cookie        string            `json:"cookie"`
		Enabled       *bool             `json:"enabled"`
	}
	if err := decodeJSON(w, r, &req, 64<<10, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	baseURL := normalizeBaseURL(req.BaseURL)
	if baseURL == "" {
		writeError(w, http.StatusBadRequest, "base_url is required")
		return
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
		writeError(w, http.StatusBadRequest, "invalid base_url")
		return
	}
	method := strings.ToUpper(strings.TrimSpace(req.CheckinMethod))
	if method != "" && method != http.MethodPost && method != http.MethodGet {
		writeError(w, http.StatusBadRequest, "checkin_method must be POST or GET")
		return
	}
	cookie := strings.TrimSpace(req.Cookie)
	if cookie == "" {
		writeError(w, http.StatusBadRequest, "cookie is required")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = hostLabel(baseURL)
	}
	headers, err := normalizeExternalHeaders(req.Headers)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid headers: "+err.Error())
		return
	}
	meta, err := json.Marshal(externalCheckinMeta{
		CheckinPath:   strings.TrimSpace(req.CheckinPath),
		CheckinMethod: method,
		Headers:       headers,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encode metadata")
		return
	}
	// Reuse an existing external site with the same base URL.
	siteID, err := h.externalSiteID(baseURL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to look up site")
		return
	}
	createdSite := false
	if siteID == 0 {
		siteID, err = h.db.Site.Create(&domain.Site{
			Name: name, BaseURL: baseURL, Platform: externalCheckinPlatform, Status: domain.StatusEnabled,
		})
		if err != nil {
			writeStoreError(w, err)
			return
		}
		createdSite = true
	}
	encCookie, err := h.enc.Encrypt([]byte(cookie))
	if err != nil {
		if createdSite {
			_ = h.db.Site.Delete(siteID)
		}
		writeError(w, http.StatusInternalServerError, "encryption failed")
		return
	}
	enabled := false
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	credID, err := h.db.Credential.Create(&domain.Credential{
		SiteID:         siteID,
		Kind:           "session",
		AuthMode:       "cookie",
		CookieEnc:      []byte(encCookie),
		MetaJSON:       string(meta),
		Status:         domain.StatusEnabled,
		CheckinEnabled: enabled,
	})
	if err != nil {
		if createdSite {
			_ = h.db.Site.Delete(siteID)
		}
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, externalCheckinView{
		SiteID:         siteID,
		CredentialID:   credID,
		Name:           name,
		BaseURL:        baseURL,
		CheckinPath:    strings.TrimSpace(req.CheckinPath),
		CheckinMethod:  method,
		Headers:        headers,
		CheckinEnabled: enabled,
		HasCookie:      true,
	})
}

func (h *CheckinHandler) updateExternal(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		Name          string            `json:"name"`
		BaseURL       string            `json:"base_url"`
		CheckinPath   string            `json:"checkin_path"`
		CheckinMethod string            `json:"checkin_method"`
		Headers       map[string]string `json:"headers"`
		Cookie        string            `json:"cookie"`
		ClearCookie   bool              `json:"clear_cookie"`
		Enabled       *bool             `json:"enabled"`
	}
	if err := decodeJSON(w, r, &req, 64<<10, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	site, err := h.db.Site.GetByID(id)
	if err != nil || site == nil || site.Platform != externalCheckinPlatform {
		writeError(w, http.StatusNotFound, "external check-in site not found")
		return
	}
	baseURL := normalizeBaseURL(req.BaseURL)
	if baseURL != "" {
		parsed, parseErr := url.Parse(baseURL)
		if parseErr != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
			writeError(w, http.StatusBadRequest, "invalid base_url")
			return
		}
	}
	method := strings.ToUpper(strings.TrimSpace(req.CheckinMethod))
	if method != "" && method != http.MethodPost && method != http.MethodGet {
		writeError(w, http.StatusBadRequest, "checkin_method must be POST or GET")
		return
	}
	updated := *site
	if name := strings.TrimSpace(req.Name); name != "" {
		updated.Name = name
	}
	if baseURL != "" {
		updated.BaseURL = baseURL
	}
	if err := h.db.Site.Update(&updated); err != nil {
		writeStoreError(w, err)
		return
	}

	credentials, err := h.db.Credential.ListBySite(site.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list check-in credentials")
		return
	}
	var credential *domain.Credential
	for i := range credentials {
		kind := strings.ToLower(strings.TrimSpace(credentials[i].Kind))
		if kind == "session" || kind == "access_token" {
			credential = &credentials[i]
			break
		}
	}
	if credential == nil {
		writeError(w, http.StatusNotFound, "check-in credential not found")
		return
	}
	changed := false
	if req.ClearCookie {
		credential.CookieEnc = nil
		changed = true
	} else if cookie := strings.TrimSpace(req.Cookie); cookie != "" {
		encCookie, encErr := h.enc.Encrypt([]byte(cookie))
		if encErr != nil {
			writeError(w, http.StatusInternalServerError, "encryption failed")
			return
		}
		credential.CookieEnc = []byte(encCookie)
		changed = true
	}
	path := strings.TrimSpace(req.CheckinPath)
	headers, headerErr := normalizeExternalHeaders(req.Headers)
	if headerErr != nil {
		writeError(w, http.StatusBadRequest, "invalid headers: "+headerErr.Error())
		return
	}
	if method != "" || path != "" || headers != nil {
		var meta externalCheckinMeta
		if json.Unmarshal([]byte(credential.MetaJSON), &meta) != nil {
			meta = externalCheckinMeta{}
		}
		if path != "" {
			meta.CheckinPath = path
		}
		if method != "" {
			meta.CheckinMethod = method
		}
		if headers != nil {
			meta.Headers = headers
		}
		body, marshalErr := json.Marshal(meta)
		if marshalErr != nil {
			writeError(w, http.StatusInternalServerError, "failed to encode metadata")
			return
		}
		credential.MetaJSON = string(body)
		changed = true
	}
	if changed {
		if err := h.db.Credential.Update(credential); err != nil {
			writeStoreError(w, err)
			return
		}
	}
	if req.Enabled != nil {
		if err := h.db.Credential.SetCheckinEnabled(credential.ID, *req.Enabled); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update check-in configuration")
			return
		}
	}
	var meta externalCheckinMeta
	_ = json.Unmarshal([]byte(credential.MetaJSON), &meta)
	enabled := credential.CheckinEnabled
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	writeJSON(w, http.StatusOK, externalCheckinView{
		SiteID:         site.ID,
		CredentialID:   credential.ID,
		Name:           updated.Name,
		BaseURL:        updated.BaseURL,
		CheckinPath:    meta.CheckinPath,
		CheckinMethod:  meta.CheckinMethod,
		Headers:        meta.Headers,
		CheckinEnabled: enabled,
		HasCookie:      len(credential.CookieEnc) > 0,
	})
}

func (h *CheckinHandler) deleteExternal(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	site, err := h.db.Site.GetByID(id)
	if err != nil || site == nil || site.Platform != externalCheckinPlatform {
		writeError(w, http.StatusNotFound, "external check-in site not found")
		return
	}
	if err := h.db.Site.Delete(id); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *CheckinHandler) externalSiteID(baseURL string) (int64, error) {
	sites, err := h.db.Site.List()
	if err != nil {
		return 0, err
	}
	for _, site := range sites {
		if site.Platform == externalCheckinPlatform && normalizeBaseURL(site.BaseURL) == baseURL {
			return site.ID, nil
		}
	}
	return 0, nil
}

func (h *CheckinHandler) runCredential(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	result, err := h.service.RunCredential(r.Context(), id, checkin.SourceManual, false)
	if err != nil {
		writeCheckinError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *CheckinHandler) runAll(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.RunAll(r.Context(), checkin.SourceManual)
	if err != nil {
		writeCheckinError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *CheckinHandler) listLogs(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	limit := 100
	if raw := query.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 || parsed > 500 {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and 500")
			return
		}
		limit = parsed
	}
	credentialID, ok := optionalPositiveQueryID(w, query.Get("credential_id"), "credential_id")
	if !ok {
		return
	}
	siteID, ok := optionalPositiveQueryID(w, query.Get("site_id"), "site_id")
	if !ok {
		return
	}
	status := query.Get("status")
	if status != "" && status != checkin.StatusSuccess && status != checkin.StatusFailed && status != checkin.StatusSkipped {
		writeError(w, http.StatusBadRequest, "invalid status")
		return
	}
	source := query.Get("source")
	if source != "" && source != checkin.SourceManual && source != checkin.SourceScheduled {
		writeError(w, http.StatusBadRequest, "invalid source")
		return
	}
	logs, err := h.db.CheckinLog.List(store.CheckinLogFilter{
		CredentialID: credentialID,
		SiteID:       siteID,
		Status:       status,
		Source:       source,
		Limit:        limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list check-in logs")
		return
	}
	writeJSON(w, http.StatusOK, logs)
}

func (h *CheckinHandler) setCredentialEnabled(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var request struct {
		Enabled *bool `json:"enabled"`
	}
	if err := decodeJSON(w, r, &request, 0, true); err != nil || request.Enabled == nil {
		writeError(w, http.StatusBadRequest, "invalid check-in configuration")
		return
	}
	if err := h.db.Credential.SetCheckinEnabled(id, *request.Enabled); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "credential not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update check-in configuration")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"credential_id": id, "checkin_enabled": *request.Enabled})
}

func optionalPositiveQueryID(w http.ResponseWriter, raw, name string) (*int64, bool) {
	if raw == "" {
		return nil, true
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid "+name)
		return nil, false
	}
	return &id, true
}

func writeCheckinError(w http.ResponseWriter, err error) {
	var checkinErr *checkin.Error
	if errors.As(err, &checkinErr) && checkinErr.Kind == checkin.ErrorNotFound {
		writeError(w, http.StatusNotFound, checkinErr.Category)
		return
	}
	writeError(w, http.StatusInternalServerError, "check-in failed")
}
